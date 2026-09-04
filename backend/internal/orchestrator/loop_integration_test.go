package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/seekersstudio/backend/internal/ai"
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
		case "run_lint":
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		}
		return `{"ok":true}`, false
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
