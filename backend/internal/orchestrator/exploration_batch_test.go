package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L14 pre-write mega-batch cap + FM-E2a warning timing. Failure modes
// written first (TEST_RULES §3):
//
//  1. (FM-E1a) A pre-first-write batch over the cap: only the first
//     maxPreWriteReadBatch read-class calls dispatch; the rest refuse
//     with the batch-cap text. Red before implementation: every call in
//     a 30-read batch executes.
//  2. (FM-E1a) Exemption lanes — a batch containing a write, any batch
//     after the turn's first write, and a disarmed (plan/child) turn all
//     run uncapped. Guards the cap from over-firing into lanes the
//     doctrine allows; green before and after, red only if the cap leaks.
//  3. (FM-E1b) Counting: cap-refused calls add nothing to the streak —
//     after a 30-read first batch the streak is 10, so the next 8-read
//     batch lands exactly on 18: advisory note, one warning event, all 8
//     run. Red before implementation: the streak would be 40 (hard stop)
//     and none would run.
//  4. (FM-E1b) The cap's refusal text names the batch cap and never
//     "Exploration budget exhausted" — the model must know which rule
//     hit it.
//  5. (FM-E2a) A batch that jumps the streak past the hard stop emits the
//     warning event as well as refusing: seed 17 + an 8-read batch →
//     refusals AND exactly one exploration_budget event. Red before
//     implementation: the hard branch shadows the warning, no event.
func drainBudgetEvents(sess *SessionState) int {
	n := 0
	for {
		select {
		case ev := <-sess.Emitter.Events():
			if ev.Tool == "exploration_budget" {
				n++
			}
		default:
			return n
		}
	}
}

func readBatchCalls(tool string, n int) []ai.ToolUseBlock {
	calls := make([]ai.ToolUseBlock, n)
	for i := range calls {
		calls[i] = ai.ToolUseBlock{
			ID:    fmt.Sprintf("r%s%d", tool, i),
			Name:  tool,
			Input: json.RawMessage(`{}`),
		}
	}
	return calls
}

func TestMegaBatchCappedBeforeFirstWrite(t *testing.T) {
	executed := 0
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	opts := RunOptions{
		ExplorationBudget: true,
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			executed++
			return "ok", false
		},
	}

	// FM-E1a: the 30-read first batch — the mega-batch RESEARCH names —
	// dispatches exactly the first 10 and refuses the tail.
	before := executed
	got := o.executeTools(context.Background(), sess, readBatchCalls("list_files", 30), opts)
	if len(got) != 30 {
		t.Fatalf("want 30 results (10 run + 20 refused), got %d", len(got))
	}
	if executed-before != 10 {
		t.Fatalf("cap: exactly the first 10 reads must run, %d did", executed-before)
	}
	for i, b := range got {
		if i < 10 {
			if b.IsError {
				t.Errorf("dispatched read %d must run, got error %q", i, b.Content)
			}
			continue
		}
		if !b.IsError {
			t.Errorf("capped read %d must refuse", i)
			continue
		}
		// FM-E1b: the refusal names the cap, never the exhausted budget.
		if strings.Contains(b.Content, "Exploration budget exhausted") {
			t.Errorf("capped read %d hit the batch cap but says budget: %q", i, b.Content)
		}
		if !strings.Contains(b.Content, "Batch capped") {
			t.Errorf("capped read %d must explain the batch cap, got %q", i, b.Content)
		}
	}

	// FM-E1b counting: only the 10 dispatched reads entered the streak, so
	// the next 8-read batch lands exactly on 18 — all 8 run (under the
	// cap, under the hard stop), the reminder note rides the first result,
	// and the warning fires once. If refused calls had counted (40), the
	// hard stop would refuse this batch entirely.
	afterCap := executed
	got = o.executeTools(context.Background(), sess, readBatchCalls("grep_codebase", 8), opts)
	if executed-afterCap != 8 {
		t.Fatalf("streak must count dispatched reads only (10 + 8 = 18): all 8 must run, %d did", executed-afterCap)
	}
	if got[0].IsError || !strings.Contains(got[0].Content, "Exploration budget is 12") {
		t.Errorf("crossing the reminder must carry the advisory on the first result, got %q", got[0].Content)
	}
	if n := drainBudgetEvents(sess); n != 1 {
		t.Errorf("crossing 18 must emit exactly one warning event, got %d", n)
	}
}

// FM-E1a exemptions: write-batch, post-write, and disarmed lanes are never
// capped — the cap guards the survey before acting, not acting itself.
func TestMegaBatchCapExemptLanes(t *testing.T) {
	o := New(nil, nil, nil)

	// (a) A batch containing a write: the write resets the budget, nothing
	// caps, all 13 calls run.
	sess := NewSession(1, 1, t.TempDir(), evn())
	executed := 0
	opts := RunOptions{
		ExplorationBudget: true,
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			executed++
			return "ok", false
		},
	}
	calls := readBatchCalls("list_files", 12)
	calls = append(calls, ai.ToolUseBlock{ID: "w0", Name: "write_file",
		Input: json.RawMessage(`{"path":"app/index.tsx","content":"x"}`)})
	for _, b := range o.executeTools(context.Background(), sess, calls, opts) {
		if b.IsError {
			t.Fatalf("write-batch lane must never cap, got error %q", b.Content)
		}
	}
	if executed != 13 {
		t.Fatalf("write-batch: all 13 calls must run, %d did", executed)
	}

	// (b) After the first write: a 15-read batch runs uncapped (it stays
	// under the hard stop, so only the cap could refuse it).
	executed = 0
	for _, b := range o.executeTools(context.Background(), sess, readBatchCalls("grep_codebase", 15), opts) {
		if b.IsError {
			t.Fatalf("post-write lane must never cap, got error %q", b.Content)
		}
		if strings.Contains(b.Content, "Batch capped") {
			t.Fatalf("post-write lane must never carry cap text, got %q", b.Content)
		}
	}
	if executed != 15 {
		t.Fatalf("post-write: all 15 reads must run, %d did", executed)
	}

	// (c) Disarmed (plan/child): a 30-read mega-batch runs clean — no
	// refusal, no cap text, no budget text at all.
	sess2 := NewSession(1, 1, t.TempDir(), evn())
	executed = 0
	disarmed := RunOptions{
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			executed++
			return "ok", false
		},
	}
	if disarmed.ExplorationBudget {
		t.Fatal("disarmed opts must not arm the budget")
	}
	for _, b := range o.executeTools(context.Background(), sess2, readBatchCalls("find_files", 30), disarmed) {
		if b.IsError {
			t.Fatalf("disarmed turn must never refuse a read, got %q", b.Content)
		}
		if strings.Contains(b.Content, "Batch capped") || strings.Contains(b.Content, "Exploration budget") {
			t.Fatalf("disarmed turn must never carry budget text, got %q", b.Content)
		}
	}
	if executed != 30 {
		t.Fatalf("disarmed: all 30 reads must run, %d did", executed)
	}
}

// FM-E2a: the warning event fires whenever the streak crosses the warning
// threshold — even when that same batch also jumps past the hard stop.
// The only user-visible step between advisory and refusal may not vanish
// exactly when the jump makes it most needed.
func TestWarningFiresWhenBatchJumpsPastHardStop(t *testing.T) {
	executed := 0
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	opts := RunOptions{
		ExplorationBudget: true,
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			executed++
			return "ok", false
		},
	}

	// Seed the streak just under the warning: this batch crosses 18 and
	// 24 at once (17 + 8 = 25).
	sess.NoteExplorationReads(17)

	before := executed
	got := o.executeTools(context.Background(), sess, readBatchCalls("find_files", 8), opts)
	if executed != before {
		t.Fatalf("hard stop: no read may reach the executor, %d did", executed-before)
	}
	if len(got) != 8 {
		t.Fatalf("want 8 refused results, got %d", len(got))
	}
	for i, b := range got {
		if !b.IsError || !strings.Contains(b.Content, "Exploration budget exhausted") {
			t.Errorf("result %d must be the hard-stop refusal, got (err=%v, %q)", i, b.IsError, b.Content)
		}
	}
	if n := drainBudgetEvents(sess); n != 1 {
		t.Fatalf("crossing the warning inside a hard-stop jump must still emit exactly one warning event (FM-E2a), got %d", n)
	}
}
