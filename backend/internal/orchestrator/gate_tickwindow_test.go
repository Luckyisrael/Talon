package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/aitest"
	"github.com/luckyisrael/talon/backend/internal/events"
)

// FM-D1c / L22 — written first (TEST_RULES §3). The failure: both
// wall-clock gates (L3's route ladder, L17's first-artifact clock) are
// only *sampled* at round-loop ticks. Run-4's round 4 generated for
// 7m56s — ticks at +11s/+16s/+25s, next at +8m22s after the write had
// already landed — so the +6 min threshold passed inside a window with
// no tick in it: FirstWriteWarnDue was never asked, the one-shot latch
// was never claimed, and the run's first_write evidence reads 0 while
// S1 fails at 11m49s. The scorecard grades minutes; the gate was
// grading rounds.
//
// The law: the due-checks run on their own wall-clock ticker for the
// life of the turn as well as at round ticks — same due functions,
// same latches, same event shape. Whichever caller observes the
// threshold first claims the event.
//
// The test shapes Run-4's window: the threshold is set to cross ~2 s
// into one model call, and that call holds the round open until the
// watch-tool warning reaches the wire (or a 5 s cap). The turn then
// answers end_turn — there is no round 2 that could fire the event
// later, so "the event landed while the generation was in flight" is
// the only way it can land at all.

// spanGenerationGate runs one build turn whose single model call spans
// `threshold` of turn age: the gate crosses 2 s after the turn starts,
// mid-generation. It reports whether watchTool's warning event reached
// the wire while that generation was blocked.
func spanGenerationGate(t *testing.T, watchTool string, threshold time.Duration) bool {
	t.Helper()

	var warningSeen atomic.Bool
	var warningAt atomic.Int64  // unix nano the drain saw the event; 0 = never
	var genStartAt atomic.Int64 // unix nano the model call arrived
	var during atomic.Bool      // the warning landed while blocked

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		genStartAt.Store(time.Now().UnixNano())
		// The generation spans the threshold: hold the round open until
		// the warning is on the wire. The 5 s cap lets a red run finish
		// the turn instead of hanging.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && !warningSeen.Load() {
			time.Sleep(10 * time.Millisecond)
		}
		if warningSeen.Load() {
			during.Store(true)
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

	// Compress the ticker: the shape under test is "the check runs off
	// the round clock at all", not the production 15 s cadence (the
	// build heartbeat's own knob, same pattern).
	prevTick := gateTickInterval
	gateTickInterval = 50 * time.Millisecond
	t.Cleanup(func() { gateTickInterval = prevTick })

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)
	defer sess.Emitter.Close()
	// The threshold crosses 2 s into the turn — after round 1's tick
	// (which runs within the first few hundred ms of the loop) and well
	// inside the model call below.
	sess.StartedAt = time.Now().Add(-threshold + 2*time.Second)

	// Drain the emitter, recording the watch-tool warning's arrival.
	go func() {
		for {
			select {
			case ev, ok := <-sess.Emitter.Events():
				if !ok {
					return
				}
				if ev.Type == events.ToolCallEvent && ev.Tool == watchTool && ev.Status == "warning" {
					warningAt.Store(time.Now().UnixNano())
					warningSeen.Store(true)
				}
			case <-sess.Emitter.Done():
				return
			}
		}
	}()

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make the dashboard")},
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

	// Fixture guards: the model ran at all, and the warning did not
	// land *before* the generation — a round-1 claim would mean the
	// threshold never spanned anything, and the test would prove
	// nothing either way.
	if genStartAt.Load() == 0 {
		t.Fatal("the model was never called — fixture never reached a generation")
	}
	if at := warningAt.Load(); at != 0 && at < genStartAt.Load() {
		t.Fatalf("fixture: %s warning landed before the generation started — round 1 claimed it; nothing spanned the threshold", watchTool)
	}
	return during.Load()
}

func TestFirstWriteWarnFiresDuringASpanningGeneration(t *testing.T) {
	if !spanGenerationGate(t, "first_write", firstWriteWarnAt) {
		t.Error("the +6 min first-write warning never reached the wire while the generation was in flight — " +
			"Run-4's window: round ticks skipped the threshold, the latch never claimed, S1's evidence reads 0 (FM-D1c)")
	}
}

func TestRouteWarnFiresDuringASpanningGeneration(t *testing.T) {
	if !spanGenerationGate(t, "route_scaffold", routeScaffoldWarnAt) {
		t.Error("the +10 min route warning never reached the wire while the generation was in flight — " +
			"L3's route warning fired in Run-4 only because a tick happened to exist after +10 min (FM-D1c)")
	}
}
