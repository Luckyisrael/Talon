package sessionlog

import (
	"encoding/json"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
)

// A crash mid-turn leaves the log with an open turn and unanswered tool calls.
// On load, the repair closes that tail honestly instead of truncating work that
// was durably written before the crash. The tests below build the crashed tail
// directly against the store, then load a fresh Log over it — the load path the
// repair runs on.

// A turn that crashed after one call was dispatched and answered, one was
// dispatched but never answered, and one was requested but never dispatched.
func TestRepairClosesCrashedTurn(t *testing.T) {
	store := newMemStore()
	// Build the crashed tail directly against the store, then load a fresh log
	// over it — that is the load path the repair runs on.
	build := func() {
		l := &Log{store: store, key: "crash", repairOnLoad: false}
		l.Append(events.TurnStarted, events.TurnPayload{PromptID: 42})
		l.Append(events.AssistantMessage, events.MessagePayload{
			Role:   "assistant",
			Blocks: []events.BlockPayload{{Type: "tool_use", ID: "c1", Name: "read_file"}},
		})
		l.Append(events.ToolDispatch, events.ToolDispatchPayload{CallID: "c1", Name: "read_file"})
		l.Append(events.ToolResult, events.ToolResultPayload{CallID: "c1", Content: "ok"})

		l.Append(events.AssistantMessage, events.MessagePayload{
			Role:   "assistant",
			Blocks: []events.BlockPayload{{Type: "tool_use", ID: "c2", Name: "write_file"}},
		})
		l.Append(events.ToolDispatch, events.ToolDispatchPayload{CallID: "c2", Name: "write_file"})
		// c2 is dispatched and then the process dies: no result.

		l.Append(events.AssistantMessage, events.MessagePayload{
			Role:   "assistant",
			Blocks: []events.BlockPayload{{Type: "tool_use", ID: "c3", Name: "run_typecheck"}},
		})
		// c3 is requested but never dispatched.
	}
	build()

	before := len(store.rows["crash"])
	l, err := New(store, "crash")
	if err != nil {
		t.Fatalf("New over a crashed tail: %v", err)
	}

	// The repair must not drop anything written before the crash.
	if got := len(l.rows); got < before {
		t.Fatalf("repair truncated the log: %d rows before, %d after", before, got)
	}

	// c2 gets an unknown-outcome result, c3 a not-started result, then the turn
	// closes. One each of the two synthetic kinds.
	var unknown, notStarted, finished int
	var unknownContent, notStartedContent string
	for _, e := range l.rows {
		switch e.Kind {
		case events.ToolResult:
			var r events.ToolResultPayload
			if json.Unmarshal(e.Payload, &r) != nil {
				continue
			}
			switch r.CallID {
			case "c2":
				unknown++
				unknownContent = r.Content
			case "c3":
				notStarted++
				notStartedContent = r.Content
			}
		case events.TurnFinished:
			finished++
		}
	}
	if unknown != 1 {
		t.Errorf("c2 (dispatched, never answered): want 1 synthetic result, got %d", unknown)
	}
	if notStarted != 1 {
		t.Errorf("c3 (requested, never dispatched): want 1 synthetic result, got %d", notStarted)
	}
	if finished != 1 {
		t.Errorf("want the turn closed by one turn/finished, got %d", finished)
	}
	if unknownContent == "" || !contains(unknownContent, "outcome is unknown") {
		t.Errorf("the unknown-outcome result must say the outcome is unknown, got %q", unknownContent)
	}
	if notStartedContent == "" || !contains(notStartedContent, "before the harness recorded it as started") {
		t.Errorf("the not-started result must say the call never started, got %q", notStartedContent)
	}

	// The closed turn must be the interrupted one, and the pre-crash answer must
	// still be there untouched.
	last := l.rows[len(l.rows)-1]
	if last.Kind != events.TurnFinished {
		t.Fatalf("the last event must be the turn/finished, got %s", last.Kind)
	}
	var tp events.TurnPayload
	if json.Unmarshal(last.Payload, &tp) != nil {
		t.Fatal("turn/finished payload must unmarshal")
	}
	if tp.Outcome != "interrupted" {
		t.Errorf("the closed turn's outcome must be interrupted, got %q", tp.Outcome)
	}
	if tp.PromptID != 42 {
		t.Errorf("the closed turn must carry the crashed turn's prompt id, got %d", tp.PromptID)
	}

	// Repairing an already-balanced log appends nothing.
	rep, err := l.Repair()
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClosedTurn || rep.UnknownResults != 0 || rep.NotStartedResults != 0 {
		t.Errorf("a balanced log must not be repaired again, got %+v", rep)
	}
}

// An earlier turn's unanswered calls must never leak into a later turn's
// repair: the turn is the replay boundary, and state resets per turn/started.
func TestRepairDoesNotLeakAcrossTurns(t *testing.T) {
	store := newMemStore()
	l := &Log{store: store, key: "crash2", repairOnLoad: false}
	l.Append(events.TurnStarted, events.TurnPayload{PromptID: 1})
	l.Append(events.AssistantMessage, events.MessagePayload{
		Role:   "assistant",
		Blocks: []events.BlockPayload{{Type: "tool_use", ID: "old", Name: "read_file"}},
	})
	l.Append(events.ToolDispatch, events.ToolDispatchPayload{CallID: "old", Name: "read_file"})
	// The first turn is left open in this log, and a second turn starts on top
	// of it — the repair must attribute "old" to whichever turn is actually open
	// at the tail, not to both.
	l.Append(events.TurnStarted, events.TurnPayload{PromptID: 2})

	loaded, err := New(store, "crash2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var tp events.TurnPayload
	if err := json.Unmarshal(loaded.rows[len(loaded.rows)-1].Payload, &tp); err != nil {
		t.Fatal(err)
	}
	if tp.PromptID != 2 {
		t.Errorf("the closed turn should be the last opened one (prompt 2), got %d", tp.PromptID)
	}
}

// A log that is balanced — no open turn — must be loaded untouched.
func TestRepairLeavesBalancedLogAlone(t *testing.T) {
	store := newMemStore()
	l := &Log{store: store, key: "balanced", repairOnLoad: false}
	l.Append(events.TurnStarted, events.TurnPayload{PromptID: 7})
	l.Append(events.UserMessage, msg("user", "hi"))
	l.Append(events.TurnFinished, events.TurnPayload{PromptID: 7, Outcome: "success"})
	before := len(store.rows["balanced"])

	if _, err := New(store, "balanced"); err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := len(store.rows["balanced"]); got != before {
		t.Errorf("a balanced log must be loaded untouched: %d before, %d after", before, got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
