package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// The H3 migration gate: with the same scripted provider, legacy dispatch and
// the bounded pipeline produce the identical transcript — every round, every
// tool_result, byte for byte. The pipeline changes scheduling, never content;
// if this ever fails, the flag cannot flip.
func TestPipelineTranscriptParity(t *testing.T) {
	script := func() (*httptest.Server, *[]([]byte)) {
		var mu sync.Mutex
		var requests []([]byte)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buf := make([]byte, 1<<20)
			n, _ := r.Body.Read(buf)
			mu.Lock()
			requests = append(requests, buf[:n])
			calls++
			c := calls
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if c == 1 {
				// A read-only batch: three reads the pipeline may overlap.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"content": []map[string]any{
						{"type": "tool_use", "id": "a", "name": "list_files", "input": map[string]any{"path": ""}},
						{"type": "tool_use", "id": "b", "name": "list_files", "input": map[string]any{"path": "app"}},
						{"type": "tool_use", "id": "c", "name": "list_files", "input": map[string]any{"path": "components"}},
					},
					"stop_reason": "tool_use",
					"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":     []map[string]any{{"type": "text", "text": "done"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
		}))
		return srv, &requests
	}

	run := func() []ai.Message {
		srv, _ := script()
		defer srv.Close()
		t.Setenv("AI_PROTOCOL", "anthropic")
		t.Setenv("AI_BASE_URL", srv.URL)
		t.Setenv("AI_API_KEY", "test-key")
		t.Setenv("AI_MODEL", "test-model")

		client, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		o := New(client, nil, loadKnowledge(t))
		sess := newTestSession(t)
		defer sess.Emitter.Close()

		var opts RunOptions
		opts.Mode = "build"
		opts.History = []ai.Message{ai.UserText("list everything")}
		opts.Tools = []ai.ToolDefinition{{Name: "list_files"}}
		opts.Execute = func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			return "listed", false
		}
		opts.ReadOnlyTool = func(name string) bool { return name == "list_files" }
		outcome, err := o.RunAgentLoop(context.Background(), sess, opts)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		// The requests the provider saw ARE the transcript as sent.
		return outcome.Messages
	}

	// Legacy: explicit opt-out — sequential dispatch (the default flipped
	// ON at W8.1, so "" would now mean pipeline-on).
	t.Setenv("TALONSTUDIO_PIPELINE", "off")
	legacy := run()

	// Pipeline: flag on — bounded pool dispatch.
	t.Setenv("TALONSTUDIO_PIPELINE", "v1")
	piped := run()

	lj, _ := json.Marshal(legacy)
	pj, _ := json.Marshal(piped)
	if string(lj) != string(pj) {
		t.Errorf("transcripts differ between dispatch modes:\nlegacy   %s\npipeline %s", lj, pj)
	}
	if len(legacy) == 0 {
		t.Fatal("script produced no transcript")
	}
}
