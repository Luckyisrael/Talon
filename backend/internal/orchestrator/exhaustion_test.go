package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seekersstudio/backend/internal/ai"
)

// A build that reaches its round budget while still working is a graceful,
// resumable pause — not a failure. RunAgentLoop must return a nil error with
// Exhausted set (so the caller records a resumable status and offers Resume),
// keep the files it changed reported for persistence, and land in
// WAITING_FOR_USER rather than FAILED. A model that calls a tool every round
// never lets the loop finish, so with the budget forced low the loop runs
// straight to exhaustion.
func TestRunAgentLoopExhaustsGracefully(t *testing.T) {
	// Always answer with a tool_use so the model never signals completion.
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{
				"type":  "tool_use",
				"id":    "call-1",
				"name":  "read_file",
				"input": map[string]any{"path": "app/index.tsx"},
			}},
			"stop_reason": "tool_use",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	// The budget knob (W4.1) doubles as the test lever: three rounds is enough to
	// prove the loop stops at its ceiling without waiting on the default sixty.
	t.Setenv("AI_MAX_ROUNDS", "3")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}

	// A nil store is safe here: RunAgentLoop only reaches the store through
	// project-context loading (an API-layer step) and the write_adr/write_memory
	// handlers, neither of which this read_file-only run triggers.
	o := New(aiClient, nil, loadKnowledge(t))
	if o.maxRounds != 3 {
		t.Fatalf("expected AI_MAX_ROUNDS=3 to set the budget, got %d", o.maxRounds)
	}

	sess := newTestSession(t)
	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute: func(_ context.Context, _ string, _ json.RawMessage) (string, bool) {
			return "ok", false
		},
		FilesTouched: func() []string { return []string{"app/index.tsx"} },
	})

	if err != nil {
		t.Fatalf("exhaustion must be a nil-error pause, got error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome on exhaustion, got nil")
	}
	if !outcome.Exhausted {
		t.Error("expected outcome.Exhausted to mark the round-budget pause")
	}
	if outcome.Paused {
		t.Error("exhaustion is not an ask_user pause; Paused must stay false")
	}
	if outcome.Rounds != 3 {
		t.Errorf("expected the loop to run the full 3-round budget, got %d", outcome.Rounds)
	}
	if got := sess.GetPhase(); got != PhaseWaiting {
		t.Errorf("expected phase WAITING_FOR_USER on a resumable pause, got %s", got)
	}
	if len(outcome.FilesTouched) != 1 || outcome.FilesTouched[0] != "app/index.tsx" {
		t.Errorf("expected the changed files preserved on pause, got %v", outcome.FilesTouched)
	}
}
