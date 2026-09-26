package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// The full generate → verify → heal → verify loop against a scripted fake
// provider: round one writes a file, the checker fails it, the loop feeds the
// error back, round two stops calling tools, the checker passes, the turn
// verifies. No network beyond localhost, no tokens, deterministic.
func TestAgentLoopGenerateVerifyHeal(t *testing.T) {
	var mu sync.Mutex
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		modelCalls++
		n := modelCalls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "call-1",
					"name":  "write_file",
					"input": map[string]any{"path": "app/index.tsx", "content": "x"},
				}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "fixed it"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)

	var mu2 sync.Mutex
	writes := 0
	checks := 0
	executor := func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
		mu2.Lock()
		defer mu2.Unlock()
		switch name {
		case "write_file":
			writes++
			return `{"ok":true}`, false
		case "run_typecheck":
			checks++
			if checks == 1 {
				out, _ := json.Marshal(map[string]any{
					"passed":  false,
					"errors":  "app/index.tsx(1,1): error TS2307: cannot find module",
					"summary": "1 type error",
				})
				return string(out), false
			}
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		default:
			// Every other gate (lint, bundle, gradle, boot, vision, interact)
			// answers the VerifyResult schema explicitly: a bare {"ok":true}
			// parses with passed=false and would fail the ladder.
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		}
	}

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute:      executor,
		FilesTouched: func() []string { return []string{"app/index.tsx"} },
	})
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}
	if !outcome.Verified {
		t.Error("expected the healed turn to verify")
	}
	if writes != 1 {
		t.Errorf("expected exactly 1 write, got %d", writes)
	}
	if checks < 2 {
		t.Errorf("expected fail-then-pass checks, got %d typecheck call(s)", checks)
	}
	mu.Lock()
	defer mu.Unlock()
	// Three consultations: write, stop-after-write (verify fails), stop-after-heal.
	if modelCalls != 3 {
		t.Errorf("expected 3 model calls (write, stop, post-heal stop), got %d", modelCalls)
	}
	if got := sess.GetPhase(); got != PhaseDone {
		t.Errorf("expected phase DONE, got %s", got)
	}
}

// A model emitting full-length prose with zero tool calls, repeatedly, is a
// ramble loop: each round burns ~35K tokens and minutes for zero progress.
// The breaker ends the turn as stalled after maxRambleRounds instead of
// riding all 60 rounds out. Scripted fake provider, no network, deterministic.
func TestAgentLoopRambleBreaker(t *testing.T) {
	var mu sync.Mutex
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		modelCalls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "prose dump with no tool calls"}},
			"stop_reason": "max_tokens",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)

	executor := func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
		t.Errorf("ramble turn must never reach tool execution, got %s", name)
		return `{}`, false
	}

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute:      executor,
		FilesTouched: func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("stalled turn returns nil error, got %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}
	if !outcome.Stalled {
		t.Error("expected the ramble turn to end stalled")
	}
	if outcome.Verified {
		t.Error("a stalled turn must never read as verified")
	}
	mu.Lock()
	defer mu.Unlock()
	if modelCalls != maxRambleRounds {
		t.Errorf("expected exactly %d model calls (breaker trips on the %drd), got %d",
			maxRambleRounds, maxRambleRounds, modelCalls)
	}
}

// Counter-proof: interleaved tool calls reset the ramble counter, so a
// working turn that occasionally truncates is never cut off. Script:
// tool_use, max_tokens, max_tokens, tool_use, max_tokens x3 → stalled on
// the 7th call, not the 3rd.
func TestAgentLoopRambleCounterResets(t *testing.T) {
	var mu sync.Mutex
	modelCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		modelCalls++
		n := modelCalls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 || n == 4 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "call-1",
					"name":  "write_file",
					"input": map[string]any{"path": "app/index.tsx", "content": "x"},
				}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "prose"}},
			"stop_reason": "max_tokens",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)

	executor := func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
		out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
		return string(out), false
	}

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute:      executor,
		FilesTouched: func() []string { return []string{"app/index.tsx"} },
	})
	if err != nil {
		t.Fatalf("stalled turn returns nil error, got %v", err)
	}
	if outcome == nil || !outcome.Stalled {
		t.Fatalf("expected stalled outcome, got %+v", outcome)
	}
	mu.Lock()
	defer mu.Unlock()
	if modelCalls != 7 {
		t.Errorf("expected 7 calls (reset on tool rounds 1 and 4), got %d", modelCalls)
	}
}

// The turn state card must reach the provider on the WIRE — regenerated
// every round from this turn's own activity — and never leak into the
// persisted transcript. The script is the observed failure shape: four
// reads with no writes (the read spiral), then one write, then end. The
// fake provider captures each request body, so the assertions are about
// what the model actually saw, not about internal state.
func TestAgentLoopTurnStateCardOnWire(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, string(body))
		n := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		call := func(id, name string, input map[string]any) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    id,
					"name":  name,
					"input": input,
				}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
		}
		switch {
		case n <= 4: // the spiral: read, read, read, read — nothing written
			call(fmt.Sprintf("read-%d", n), "read_file",
				map[string]any{"path": fmt.Sprintf("app/file%d.tsx", n)})
		case n == 5: // the directive lands, the model finally edits
			call("write-1", "write_file",
				map[string]any{"path": "app/index.tsx", "content": "x"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":     []map[string]any{{"type": "text", "text": "done"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
		}
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)

	executor := func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
		switch name {
		case "read_file":
			return "export const x = 1", false
		case "write_file":
			return `{"ok":true}`, false
		default:
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		}
	}

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute:      executor,
		FilesTouched: func() []string { return []string{"app/index.tsx"} },
	})
	if err != nil {
		t.Fatalf("loop failed: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 6 {
		t.Fatalf("expected 6 model requests (4 reads, 1 write, 1 finish), got %d", len(requests))
	}

	// Round 1: the card rides the first request with the turn's goal.
	if !strings.Contains(requests[0], "## Turn state") ||
		!strings.Contains(requests[0], "Goal: make a screen") {
		t.Errorf("first request missing the turn state card:\n%s", requests[0])
	}
	// Round 2: before the idle threshold the directive stays soft.
	if strings.Contains(requests[1], "No file writes") {
		t.Errorf("round 2 fired the idle directive early:\n%s", requests[1])
	}
	// Round 4: four rounds, zero writes — the read-spiral case must say so,
	// with the read counts that justify it (3 reads so far).
	if !strings.Contains(requests[3], "Round 4/60") ||
		!strings.Contains(requests[3], "No file writes after 4 rounds") ||
		!strings.Contains(requests[3], "reads: 3") {
		t.Errorf("round 4 request missing the idle directive:\n%s", requests[3])
	}
	// After the write the card reports it and the directive moves on.
	if !strings.Contains(requests[5], "writes: 1 (app/index.tsx)") {
		t.Errorf("post-write request missing the write count:\n%s", requests[5])
	}

	// The card is wire-only: nothing in the persisted transcript carries it.
	for _, m := range outcome.Messages {
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "## Turn state") ||
				strings.Contains(b.Content, "## Turn state") {
				t.Errorf("card leaked into the persisted transcript: role=%s", m.Role)
			}
		}
	}
}
