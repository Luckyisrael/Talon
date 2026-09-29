package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/aitest"
	"github.com/luckyisrael/talon/backend/internal/events"
)

// FM-CMPb — written first (TEST_RULES §3). The failure: the
// conversation ceiling is 120_000, sized for a 200K window ("leave
// room for the system prompt, tool definitions and a full response"),
// but the models this deployment serves publish 1M windows
// (claude-opus-5: 1M/128K per Anthropic's context-window docs;
// deepseek-v4-flash: 1M per DeepSeek's release and the V4 paper). At
// 120K the compactor fires at ~6% of the window and pays a full
// summarizer model round (FM-CMPa: 2m15s) long before the window
// pressures — the ceiling, not the model, is what runs out.
//
// The law: MaxContextTokens is 200_000 conversation tokens (peak
// request ≈ 200K + ~29K overhead + response ≪ 1M), and a conversation
// under that ceiling must never compact — 167K is above the old 120K
// (the red state: it compacts today) and below the new ceiling (the
// green state: it must not).
//
// Watch (recorded in the const's comment too): Atria-Dawn-Preview
// publishes no window spec — on a window < 260K the first
// conversation past ~171K 400s before the compactor can fire; the
// signal is a provider "prompt too long" and the fix is a lower
// const, never a retry.

// TestMaxContextTokensIs200K pins the operator's ordered ceiling. The
// meter (prompts.go's ContextPressure) reads the same const, so this
// one pin moves the compactor and the occupancy bar together.
func TestMaxContextTokensIs200K(t *testing.T) {
	if MaxContextTokens != 200_000 {
		t.Errorf("MaxContextTokens = %d, want 200_000 — compaction must trigger at a 200K conversation ceiling", MaxContextTokens)
	}
}

// TestConversationUnderTheCeilingDoesNotCompact: a conversation of
// ≈167K tokens — above the old 120K, below the 200K ceiling — must
// run its round untouched: no compact frame, no summarizer call.
// Today the 120K const fires the compaction branch on this history,
// which is the red state.
func TestConversationUnderTheCeilingDoesNotCompact(t *testing.T) {
	var summarizerHits atomic.Int32
	var compactFrames atomic.Int32

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "This is an automatically generated checkpoint") {
			summarizerHits.Add(1)
			aitest.ServeRaw(w, body, map[string]any{
				"content":     []map[string]any{{"type": "text", "text": "under-ceiling fixture"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		aitest.ServeRaw(w, body, map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "done"}},
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
	defer sess.Emitter.Close()

	go func() {
		for {
			select {
			case ev, ok := <-sess.Emitter.Events():
				if !ok {
					return
				}
				if ev.Type == events.ToolCallEvent && ev.Tool == "compact" {
					compactFrames.Add(1)
				}
			case <-sess.Emitter.Done():
				return
			}
		}
	}()

	// ≈166.7k tokens at len/3, plus the small turns: over the old
	// 120K (which compacts it today — the red state) and under the
	// 200K ceiling (which must leave it alone). The tail turns give
	// PlanCompaction a clean boundary, so today's red reaches the
	// summarizer too — not just the fallback path.
	giant := strings.Repeat("x", 500_000)
	history := []ai.Message{
		ai.UserText("make the dashboard"),
		ai.AssistantText("ok"),
		ai.UserText("next"),
		ai.AssistantText("ok"),
		ai.UserText("next"),
		ai.AssistantText(giant),
		ai.UserText("continue"),
		ai.AssistantText("ok"),
		ai.UserText("thanks"),
		ai.AssistantText("done"),
	}

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      history,
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			t.Errorf("this turn must never reach tool execution, got %s", name)
			return `{}`, false
		},
		FilesTouched: func() []string { return nil },
	})
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}
	if n := compactFrames.Load(); n != 0 {
		t.Errorf("a conversation under the ceiling compacted anyway: %d compact frame(s)", n)
	}
	if n := summarizerHits.Load(); n != 0 {
		t.Errorf("a conversation under the ceiling reached the summarizer: %d call(s)", n)
	}
}
