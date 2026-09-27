package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L2 exploration budget (ENGINEERING_STANDARDS S4). Failure modes written
// first (TEST_RULES §3):
//
//  1. Not armed — plan turns and child loops read unbounded by design; a note
//     or refusal there would strangle a read-only mode.
//  2. Armed, under the ladder — reads run clean; no advisory pretends to be
//     one, and the executor sees every call.
//  3. Crossing the reminder (12) — the batch's FIRST read result carries the
//     advisory note and the executor still runs: advisory, not a gate. The
//     streak counts CALLS (a 12-call batch crosses in one step); only the
//     carrier decision is per batch.
//  4. Crossing the warning (18) — exactly one exploration_budget event per
//     streak, not one per subsequent batch.
//  5. Crossing the hard stop (24) — read-class calls refuse WITHOUT reaching
//     the executor, as an error result that says why and how to proceed.
//  6. A write-class batch resets the streak — reads proceed again (the budget
//     demands writing, not abstinence), and the next crossing warns again.
func TestExplorationBudgetLadder(t *testing.T) {
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

	// Each batch uses a different read-class tool so the H3 same-tool repeat
	// reminder never interleaves its own note with the budget's.
	readBatch := func(tool string, n int) []ai.ToolUseBlock {
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
	run := func(tool string, n int) []ai.Block {
		return o.executeTools(context.Background(), sess, readBatch(tool, n), opts)
	}
	countWarnings := func() int {
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

	// Under the ladder: nothing consults the budget. These 2 reads seed the
	// streak — small enough that the 12-call batch below still lands inside
	// the reminder band, not the warning band.
	if got := run("list_files", 2); len(got) != 2 {
		t.Fatalf("under-budget batch: want 2 results, got %d", len(got))
	} else {
		for i, b := range got {
			if b.IsError || b.Content != "ok" {
				t.Fatalf("under-budget result %d must be a clean result, got (err=%v, %q)", i, b.IsError, b.Content)
			}
		}
	}
	if countWarnings() != 0 {
		t.Fatal("the budget must stay silent under the reminder threshold")
	}

	// A single 12-call batch crosses the reminder — the streak counts calls
	// (2 + 12 = 14).
	before := executed
	if got := run("grep_codebase", 12); len(got) != 12 {
		t.Fatalf("reminder batch: want 12 results, got %d", len(got))
	} else {
		if !strings.Contains(got[0].Content, "Exploration budget is 12") {
			t.Errorf("the advisory must ride the batch's first read result, got %q", got[0].Content)
		}
		if got[1].Content != "ok" {
			t.Errorf("only the carrier carries the note, got %q", got[1].Content)
		}
	}
	if executed-before != 12 {
		t.Fatalf("the reminder is advisory: all 12 calls must run, %d did", executed-before)
	}
	if countWarnings() != 0 {
		t.Fatal("the reminder threshold must not emit the warning event")
	}

	// Crossing the warning (14 + 6 = 20): one event, and the note still rides.
	if got := run("find_files", 6); len(got) != 6 {
		t.Fatalf("warning batch: want 6 results, got %d", len(got))
	} else if !strings.Contains(got[0].Content, "Exploration budget is 12") {
		t.Errorf("the note must keep riding once past the reminder, got %q", got[0].Content)
	}
	if countWarnings() != 1 {
		t.Fatalf("crossing 18 must emit exactly one warning event, got %d", countWarnings())
	}

	// Still over the reminder, still latched at one warning (20 + 3 = 23).
	if got := run("search_files", 3); len(got) != 3 {
		t.Fatalf("post-warning batch: want 3 results, got %d", len(got))
	} else if !strings.Contains(got[0].Content, "Exploration budget is 12") {
		t.Fatalf("every read past the reminder carries the note, got %q", got[0].Content)
	}
	if countWarnings() != 0 {
		t.Fatalf("the warning must fire once per streak, got %d more", countWarnings())
	}

	// The hard stop (23 + 5 = 28): reads refuse without reaching the executor.
	before = executed
	refused := run("inspect_package", 5)
	if executed != before {
		t.Fatalf("a refused read must not reach the executor; %d calls ran", executed-before)
	}
	for i, b := range refused {
		if !b.IsError {
			t.Errorf("refused result %d must be an error result", i)
		}
		if !strings.Contains(b.Content, "Exploration budget exhausted") {
			t.Errorf("refused result %d must say why, got %q", i, b.Content)
		}
	}

	// A write resets the streak: the write runs, reads run again clean.
	before = executed
	writes := o.executeTools(context.Background(), sess, []ai.ToolUseBlock{
		{ID: "w1", Name: "write_file", Input: json.RawMessage(`{"path":"app/new.tsx","content":"x"}`)},
	}, opts)
	if executed-before != 1 || len(writes) != 1 || writes[0].IsError {
		t.Fatalf("the write must run while the budget is exhausted, got %+v", writes)
	}
	before = executed
	if got := run("codebase_search", 3); len(got) != 3 {
		t.Fatalf("post-write batch: want 3 results, got %d", len(got))
	} else {
		for i, b := range got {
			if b.IsError || b.Content != "ok" {
				t.Fatalf("post-write result %d must be clean, got (err=%v, %q)", i, b.IsError, b.Content)
			}
		}
	}
	if executed-before != 3 {
		t.Fatalf("reads must run again after a write, %d did", executed-before)
	}

	// The latch cleared with the write: the next crossing warns again.
	run("codebase_graph", 15) // streak 3 + 15 = 18
	if countWarnings() != 1 {
		t.Fatalf("a fresh streak must warn again after its latch cleared, got %d", countWarnings())
	}
}

// Disarmed is the default: plan turns, child loops, and every caller that does
// not arm the budget read without notes, warnings, or refusals — a budget that
// leaked into a read-only mode would refuse every call after 24.
func TestExplorationBudgetDisarmedByDefault(t *testing.T) {
	executed := 0
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	opts := RunOptions{
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			executed++
			return "ok", false
		},
	}
	if opts.ExplorationBudget {
		t.Fatal("the budget must default off — plan and child loops pass no flag")
	}

	for i := 0; i < 30; i++ {
		calls := []ai.ToolUseBlock{{ID: fmt.Sprintf("d%d", i), Name: "list_files", Input: json.RawMessage(`{}`)}}
		for _, b := range o.executeTools(context.Background(), sess, calls, opts) {
			if b.IsError {
				t.Fatalf("disarmed turn must never refuse a read, got %q", b.Content)
			}
			if strings.Contains(b.Content, "Exploration budget") {
				t.Fatalf("disarmed turn must never carry budget text, got %q", b.Content)
			}
		}
	}
	if executed != 30 {
		t.Fatalf("disarmed: all 30 reads must run, %d did", executed)
	}
	for {
		select {
		case ev := <-sess.Emitter.Events():
			if ev.Tool == "exploration_budget" {
				t.Fatal("disarmed turn must never emit the warning event")
			}
		default:
			return
		}
	}
}
