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

// A model that fails must not kill the turn when a fallback is configured:
// the loop retries once on the fallback model and latches to it, so later
// rounds never flip back to the known-dead primary. The fake provider below
// rejects one model id and serves the other, and the test asserts the exact
// request order plus the recorded routing.
func TestLoopFallsBackAndLatches(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	realCalls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		seen = append(seen, body.Model)
		if body.Model == "real-model" {
			realCalls++
		}
		call := realCalls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "bogus-model" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "bogus-model is not a valid model ID", "code": 400},
			})
			return
		}
		// The fallback model needs two rounds: a write, then done. Two
		// post-failover rounds prove the latch — without it round two
		// re-resolves the dead primary and the turn dies there.
		if call == 1 {
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "done"},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "bogus-model")
	t.Setenv("ROUTING_POLICY_JSON", `{"fallback_model":"real-model"}`)

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("add a comment noting the module purpose, nothing else, keep it minimal and surgical")},
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "run_typecheck" || name == "run_lint" {
				out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
				return string(out), false
			}
			return `{"ok":true}`, false
		},
		FilesTouched: func() []string { return []string{"app/x.tsx"} },
	})
	if err != nil {
		t.Fatalf("fallback should save the turn, got error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome, got nil")
	}
	if !outcome.Verified {
		t.Error("expected the latched turn to verify")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"bogus-model", "real-model", "real-model"}
	if len(seen) != len(want) {
		t.Fatalf("expected %v, got %v", want, seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, seen)
		}
	}
	if outcome.RoutedModel != "real-model" {
		t.Errorf("expected RoutedModel real-model, got %q", outcome.RoutedModel)
	}
	if outcome.RouteReason == "" {
		t.Error("expected a recorded route reason")
	}
}
