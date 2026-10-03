package api

// FM-NOTIFa — a push may claim only the state the turn is actually in.
//
// The gap: PromptHandler.Stream's terminal guard was written for ONE of the
// two pauses. `outcome.Paused` (ask_user, done status "waiting") was checked
// — "a paused turn is not terminal; the question push already fired" — but
// `outcome.Exhausted`, the round budget (done status "paused", prompt row
// `paused`, Resume continues it), also returns a nil error by design, so it
// fell through the same guard and dispatched `build_success`:
// "Build finished / verified: false" for a turn still mid-work. Live
// evidence: journey3's build hit the 60-round budget and the owner's
// notification history gained exactly that row while the prompt row read
// `paused`.
//
// TEST_RULES rule 1 — the failure modes this file enumerates, each pinned
// by one assertion:
//
// F1. Leg 1 spends the round budget. The done frame says `paused` and the
//     notification history must contain ZERO build_success rows — the push
//     is a claim of "finished" and this turn is resumable. Pre-fix: one
//     row, `Build finished / verified: false`. [budget_pause_pushes_nothing]
//
// F2. The mirror, so the fix cannot over-block: the resumed leg ends
//     `success` and must fire EXACTLY one build_success — the user who
//     waited for the build is still told it is ready. Pre-fix: two rows
//     (the false one from F1 plus the real one), so this fails too.
//     [real_finish_pushes_once]
//
// Determinism: scripted stand-in provider (liveness harness), no network,
// bare workspace — infrastructure that cannot run lands in skipped_gates.
import (
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// countBuildSuccess is the history assertion surface: how many
// build_success pushes the user's notification list actually holds.
func countBuildSuccess(t *testing.T, h *livenessHarness) []store.Notification {
	t.Helper()
	notifs, err := h.store.ListNotifications(h.userID, 100)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	var out []store.Notification
	for _, n := range notifs {
		if n.Kind == "build_success" {
			out = append(out, n)
		}
	}
	return out
}

func TestBudgetPauseIsNotBuildSuccess(t *testing.T) {
	// Round 1 writes a non-lintable file, round 2 a tool call that spends the
	// 2-round budget → Exhausted; the third request is the resume leg's stop.
	script := func(idx int, w http.ResponseWriter, r *http.Request) {
		flusher := livenessOpenStream(w)
		switch idx {
		case 1:
			_, _ = io.WriteString(w, livenessToolCallStart("call_1", "write_file"))
			_, _ = io.WriteString(w, livenessToolCallArgs(`{"path":"features/ledger.json","content":"{\"entries\": []}"}`))
			_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		case 2:
			_, _ = io.WriteString(w, livenessToolCallStart("call_2", "list_files"))
			_, _ = io.WriteString(w, livenessToolCallArgs("{}"))
			_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		case 3:
			_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		default:
			t.Errorf("unexpected build request #%d", idx)
			panic(http.ErrAbortHandler)
		}
	}
	provider := newLivenessProvider(t, script)
	h := newLivenessHarness(t, provider, map[string]string{
		"AI_MAX_ROUNDS":             "2",
		"TALONSTUDIO_VERIFY_POLICY": "",
		"TALONSTUDIO_LSP":           "",
	})
	// The harness wires no notifier, and dispatchNotification no-ops on nil —
	// but the product always has one (main.go passes notify.Get), which is
	// exactly why journey3 recorded the false row live. Noop.Configured() is
	// false and Dispatch records the in-app history BEFORE checking that, so
	// the rows are observable here precisely as in production.
	h.handler.notifier = notify.Noop{}

	// ── F1: the budget pause claims nothing ──
	out1, _ := h.runBuildTurn(t)
	frames1 := livenessParseSSE(out1)
	if errs := livenessErrorMessages(frames1); len(errs) > 0 {
		t.Fatalf("leg 1 has error events: %s", errs)
	}
	done1 := livenessLastDone(frames1)
	if done1 == nil {
		t.Fatalf("leg 1 has no done frame:\n%s", livenessTail(out1, 400))
	}
	if s := livenessDoneString(done1, "status"); s != "paused" {
		t.Fatalf("leg 1 premise broken: done status %q, want paused (the budget pause this test pins)", s)
	}
	if pushed := countBuildSuccess(t, h); len(pushed) != 0 {
		t.Errorf("FM-NOTIFa F1: the budget pause pushed %d build_success row(s) — %q says the build finished while the turn is resumable; want 0",
			len(pushed), pushed[0].Body)
	}

	// ── F2: the real finish still pushes exactly once ──
	p, err := h.store.GetLatestPrompt(h.projectID)
	if err != nil {
		t.Fatalf("GetLatestPrompt after leg 1: %v", err)
	}
	out2 := resumeBuildTurn(t, h, p.ID)
	frames2 := livenessParseSSE(out2)
	done2 := livenessLastDone(frames2)
	if done2 == nil {
		t.Fatalf("resume leg has no done frame:\n%s", livenessTail(out2, 400))
	}
	if s := livenessDoneString(done2, "status"); s != "success" {
		t.Fatalf("resume leg done status %q, want success:\n%s", s, livenessTail(out2, 400))
	}
	pushed := countBuildSuccess(t, h)
	if len(pushed) != 1 {
		t.Errorf("FM-NOTIFa F2: after a completed turn the history holds %d build_success row(s), want exactly 1 — bodies: %s",
			len(pushed), fmt.Sprintf("%q", bodies(pushed)))
	}
}

// bodies renders notification bodies for a failure message.
func bodies(ns []store.Notification) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Body)
	}
	return out
}
