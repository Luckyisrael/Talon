package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	harnessevents "github.com/luckyisrael/talon/backend/internal/harness/events"
)

// payloadLog is the AuditLog fake for the W7 gates: it records each append's
// payload so a test can pin which provider usage/record and llm/retry name —
// a kind-only fake cannot prove attribution.
type payloadLog struct {
	mu      sync.Mutex
	records []payloadRecord
}

type payloadRecord struct {
	kind    harnessevents.Kind
	payload any
}

func (l *payloadLog) Append(kind harnessevents.Kind, payload any) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, payloadRecord{kind: kind, payload: payload})
	return int64(len(l.records)), nil
}

func (l *payloadLog) Sync() error { return nil }

// count returns how many records of kind name provider. Kinds whose payload
// carries no provider never match a non-empty provider.
func (l *payloadLog) count(kind harnessevents.Kind, provider string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.records {
		if r.kind != kind {
			continue
		}
		switch p := r.payload.(type) {
		case harnessevents.RetryPayload:
			if p.Provider == provider {
				n++
			}
		case harnessevents.UsagePayload:
			if p.Provider == provider {
				n++
			}
		}
	}
	return n
}

// openaiDone answers "done" with usage — one round ends the turn.
func openaiDone(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{
			"finish_reason": "stop",
			"message":       map[string]any{"content": "done"},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
	})
}

// W7.1 gate — cross-provider failover: the primary provider dies with 500s
// (non-retriable → instant error, no backoff), the turn completes on the
// providers-block secondary, and every artifact pins the endpoint that
// actually served — the model latch, outcome.RoutedProvider, the llm/retry
// event, and usage/record. The second turn proves the breaker: with primary
// still circuit-open the round starts on secondary without rediscovering
// the outage one request at a time.
func TestRouterFailsOverAcrossProviders(t *testing.T) {
	var priMu sync.Mutex
	priCalls := 0
	pri := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		priMu.Lock()
		priCalls++
		priMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "provider exploded", "code": 500},
		})
	}))
	defer pri.Close()

	var secMu sync.Mutex
	secCalls := 0
	sec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "good-model" {
			t.Errorf("secondary must be asked for its configured model, got %q", body.Model)
		}
		secMu.Lock()
		secCalls++
		call := secCalls
		secMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			// Needs two rounds: a write, then done. The latch has to carry
			// the turn or round two re-resolves the dead primary and dies.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{{
							"id":   "call-1",
							"type": "function",
							"function": map[string]any{
								"name":      "write_file",
								"arguments": `{"path":"app/x.tsx","content":"x"}`,
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
			})
			return
		}
		openaiDone(w)
	}))
	defer sec.Close()

	// One watched settings document: extras-only (providers block), routing
	// rides the env policy — the four-section document without a routing
	// section is valid W7.4.
	raw, err := json.Marshal(map[string]any{
		"providers": []map[string]string{{
			"name":     "secondary",
			"base_url": sec.URL,
			"api_key":  "sec-key",
			"model":    "good-model",
		}},
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, raw, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	ai.AttachSettingsFile(settingsPath)
	t.Cleanup(func() { ai.AttachSettingsFile("") })

	t.Setenv("TALONSTUDIO_ROUTER", "1")
	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", pri.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "dead-model")
	t.Setenv("ROUTING_POLICY_JSON", `{"fallback_provider":"secondary"}`)

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	log := &payloadLog{}

	runTurn := func(t *testing.T) (*BuildOutcome, error) {
		t.Helper()
		return o.RunAgentLoop(context.Background(), newTestSession(t), RunOptions{
			SystemPrompt: "build it",
			Mode:         "build",
			History:      []ai.Message{ai.UserText("add a comment noting the module purpose, nothing else, keep it minimal and surgical")},
			EventLog:     log,
			Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
				if name == "write_file" {
					return `{"ok":true}`, false
				}
				out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
				return string(out), false
			},
			FilesTouched: func() []string { return []string{"app/x.tsx"} },
		})
	}

	// Turn one: primary dies, secondary saves the turn, latch pins it.
	outcome, err := runTurn(t)
	if err != nil {
		t.Fatalf("failover should save the turn, got error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome, got nil")
	}
	if outcome.RoutedProvider != "secondary" {
		t.Errorf("expected RoutedProvider secondary, got %q", outcome.RoutedProvider)
	}
	if outcome.RoutedModel != "good-model" {
		t.Errorf("expected RoutedModel good-model, got %q", outcome.RoutedModel)
	}
	priMu.Lock()
	if priCalls != 1 {
		t.Errorf("expected exactly 1 primary call (the failure), got %d", priCalls)
	}
	priMu.Unlock()
	secMu.Lock()
	if secCalls != 2 {
		t.Errorf("expected 2 secondary calls (write + done + ladder on latch), got %d", secCalls)
	}
	secMu.Unlock()
	if log.count(harnessevents.RetryLogged, "secondary") != 1 {
		t.Errorf("expected exactly one llm/retry naming provider secondary, got %d",
			log.count(harnessevents.RetryLogged, "secondary"))
	}
	if n := log.count(harnessevents.UsageRecord, "secondary"); n < 1 {
		t.Errorf("expected usage/record pinned to secondary at least once, got %d", n)
	}
	if n := log.count(harnessevents.UsageRecord, "primary"); n != 0 {
		t.Errorf("primary never served a round; usage must never pin it, got %d", n)
	}

	// Turn two, fresh session, same process: primary's circuit is open, so
	// the round starts on secondary directly — primary's count must not move.
	outcome2, err := runTurn(t)
	if err != nil {
		t.Fatalf("second turn should complete, got error: %v", err)
	}
	if outcome2.RoutedProvider != "secondary" {
		t.Errorf("expected round-start on secondary while primary is circuit-open, got %q",
			outcome2.RoutedProvider)
	}
	priMu.Lock()
	if priCalls != 1 {
		t.Errorf("open primary must be skipped at round start; primary calls grew to %d", priCalls)
	}
	priMu.Unlock()
	secMu.Lock()
	if secCalls != 3 {
		t.Errorf("expected exactly one more secondary call in turn two, got %d", secCalls)
	}
	secMu.Unlock()
}

// W7.3 gate — an inflated price against a tiny per-turn cap: round one
// spends past max_turn_cost_usd, round two stops at the top of the loop
// with the spend named in the outcome and a nil error — an honest,
// deliberate stop, not a crash, and no second model request is made.
func TestRouterBudgetStopsTurnHonest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// The model keeps asking for work forever — only the budget can end
		// this turn.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"content": "",
					"tool_calls": []map[string]any{{
						"id":   "call-1",
						"type": "function",
						"function": map[string]any{
							"name":      "write_file",
							"arguments": `{"path":"app/x.tsx","content":"x"}`,
						},
					}},
				},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer srv.Close()

	t.Setenv("TALONSTUDIO_ROUTER", "1")
	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "budget-model")
	t.Setenv("ROUTING_POLICY_JSON", `{"max_turn_cost_usd":0.0001}`)
	// $1 per token: round one's 15 tokens price at $15 — far past the cap.
	t.Setenv("ROUTING_PRICING_JSON", `{"budget-model":[1000000,1000000,0]}`)

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	log := &payloadLog{}

	outcome, err := o.RunAgentLoop(context.Background(), newTestSession(t), RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("keep building the thing, round after round, never stop")},
		EventLog:     log,
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "write_file" {
				return `{"ok":true}`, false
			}
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		},
		FilesTouched: func() []string { return []string{"app/x.tsx"} },
	})
	if err != nil {
		t.Fatalf("budget stop is deliberate, not an error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome, got nil")
	}
	if !outcome.BudgetStop {
		t.Error("expected BudgetStop when spend reaches the cap")
	}
	if outcome.CostUSD < 0.0001 {
		t.Errorf("expected CostUSD >= cap, got %v", outcome.CostUSD)
	}
	if !strings.Contains(outcome.FinalText, "$") {
		t.Errorf("budget stop text must name the money, got %q", outcome.FinalText)
	}
	if !outcome.BudgetStop || strings.TrimSpace(outcome.FinalText) == "" {
		t.Error("budget stop must speak for itself")
	}
	mu.Lock()
	if requests != 1 {
		t.Errorf("the cap must stop round two before dispatch: %d requests", requests)
	}
	mu.Unlock()
}

// W7/H12 gate — a settings edit flips routing on the next turn: same
// process, same attached document, no restart. Deliberately runs with the
// router flag OFF — hot reload is the document's own discipline, not a
// router feature.
func TestSettingsEditFlipsRoutingNextTurn(t *testing.T) {
	docFor := func(model string) []byte {
		b, err := json.Marshal(map[string]any{
			"routing": map[string]any{"models": map[string]string{
				"strong": model, "medium": model, "weak": model,
			}},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, docFor("model-a"), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	ai.AttachSettingsFile(path)
	t.Cleanup(func() { ai.AttachSettingsFile("") })

	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body.Model)
		mu.Unlock()
		openaiDone(w)
	}))
	defer srv.Close()

	t.Setenv("TALONSTUDIO_ROUTER", "0")
	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "env-model")
	t.Setenv("ROUTING_POLICY_JSON", "")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))

	runTurn := func(t *testing.T) *BuildOutcome {
		t.Helper()
		outcome, err := o.RunAgentLoop(context.Background(), newTestSession(t), RunOptions{
			SystemPrompt: "build it",
			Mode:         "build",
			History:      []ai.Message{ai.UserText("build the thing")},
			Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
				out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
				return string(out), false
			},
			FilesTouched: func() []string { return []string{"app/x.tsx"} },
		})
		if err != nil {
			t.Fatalf("turn: %v", err)
		}
		return outcome
	}

	out1 := runTurn(t)
	if out1.RoutedModel != "model-a" {
		t.Fatalf("turn one should be served model-a, got %q", out1.RoutedModel)
	}

	// The edit: same path, new content, mtime pushed forward — the next
	// LoadRoutingPolicy must see it.
	if err := os.WriteFile(path, docFor("model-b"), 0o644); err != nil {
		t.Fatalf("rewrite settings: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	out2 := runTurn(t)
	if out2.RoutedModel != "model-b" {
		t.Errorf("turn two should be served model-b without a restart, got %q", out2.RoutedModel)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"model-a", "model-b"}
	if len(seen) != len(want) {
		t.Fatalf("expected one model call per turn %v, got %v", want, seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, seen)
		}
	}
}
