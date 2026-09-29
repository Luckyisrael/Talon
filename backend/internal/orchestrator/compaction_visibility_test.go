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
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/aitest"
	"github.com/luckyisrael/talon/backend/internal/events"
)

// FM-CMPa — written first (TEST_RULES §3). The failure: the round loop
// crosses MaxContextTokens and calls CompactWithSummary straight — no
// log line, no frame. The summarizer is a full model round over the
// whole to-be-dropped span (found live: Run-4 round 57,
// prep=2m15.649s, 18:05:22 → 18:07:37), during which the wire carries
// only heartbeats — which FM-L8a already ruled cannot count as
// progress. S12 (max gap ≤ 90 s between real frames) grades that
// silence as a breach of a required row, and the serverlog has no line
// to explain it afterwards: a working turn is indistinguishable from
// a dead server.
//
// The law: every wait longer than one frame is labeled *before* it
// starts — a log line and a `compact` running frame at compaction
// entry, paired with the completed frame already emitted at exit. The
// running/terminal pairing is the emitter's own contract, so the
// scorecard's tool mix still counts exactly one `compact` call; only
// the gap clock moves.

// checkpointReply is a minimal shape-valid checkpoint in the guide
// shape (looksLikeCheckpoint only requires the opening section in the
// head, but the fixture must exercise the acceptance path the new
// instruction actually asks for — FM-CMPb).
const checkpointReply = `## Objective
- build the dashboard

## Current Work State
### Completed
- (none)
### Active
- compaction test fixture
### Blocked
- (none)
### Next Move
- (none)`

// TestCompactionCheckpointIsOnTheWireBeforeTheModelCall has the fake
// summarizer wait for the compact `running` frame at the exact moment
// its request arrives: the frame must precede the call, so when it
// exists it is already on the wire and the wait ends in milliseconds.
// Today it never exists until the call *returns* — the wait times out
// and the 133k-token model call ran invisible.
//
// The fixture guard matters: if compaction never happens, the frame
// never can — "never compacted" must not pass as "compacted visibly".
func TestCompactionCheckpointIsOnTheWireBeforeTheModelCall(t *testing.T) {
	var summarizerHits atomic.Int32
	var runningSeen atomic.Bool

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "This is an automatically generated checkpoint") {
			summarizerHits.Add(1)
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && !runningSeen.Load() {
				time.Sleep(10 * time.Millisecond)
			}
			aitest.ServeRaw(w, body, map[string]any{
				"content":     []map[string]any{{"type": "text", "text": checkpointReply}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		// Round traffic: prose, end_turn, the turn is over.
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

	// Drain the emitter and record the compaction's running frame.
	go func() {
		for {
			select {
			case ev, ok := <-sess.Emitter.Events():
				if !ok {
					return
				}
				if ev.Type == events.ToolCallEvent && ev.Tool == "compact" && ev.Status == "running" {
					runningSeen.Store(true)
				}
			case <-sess.Emitter.Done():
				return
			}
		}
	}()

	// History shaped for PlanCompaction: the first turn is always kept;
	// four small turns plus one giant span make up the dropped set
	// (≥ minDroppedToSummarize = 4); five turns after the giant — the
	// first of them a plain user message, the clean boundary the backward
	// walk snaps to — leave something to keep. The giant alone puts
	// EstimateTokens over MaxContextTokens — sized const-tracking
	// (chars = const×3 + margin ⇒ tokens > const, since approx is len/3)
	// so a ceiling change cannot silently disarm this fixture, which is
	// what the guard below exists to catch.
	giant := strings.Repeat("x", MaxContextTokens*3+60_000) // ≈220k tokens at len/3
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

	if summarizerHits.Load() == 0 {
		t.Fatal("fixture never reached compaction — the giant history did not trigger the summarizer; this test proves nothing")
	}
	if !runningSeen.Load() {
		t.Error("the summarizer model call ran with no `compact` running frame on the wire — " +
			"a two-minute wait labeled only after it ended (FM-CMPa)")
	}
}
