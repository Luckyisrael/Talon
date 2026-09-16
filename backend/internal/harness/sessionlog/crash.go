package sessionlog

// crash.go — the interrupted-turn repair.
//
// A process that dies mid-turn leaves the log with an open turn and tool calls
// whose results were never recorded. Truncating that tail would throw away work
// that was durably written before the crash; replaying it as-is hands the
// provider an assistant tool request with no answer, which every provider
// rejects. Neither is honest.
//
// Instead, on load, the log closes the orphaned tail the way DSH does: it
// appends a synthetic tool/result for every unanswered call — telling the model
// the outcome is unknown and how to decide whether to retry — then a
// turn/finished marking the turn interrupted. The events written before the
// crash are preserved byte for byte; only the missing answers are invented, and
// they are invented as missing.

import (
	"encoding/json"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
)

// RepairOutcome reports what the repair did. A zero report means the log was
// already balanced and nothing was appended.
type RepairOutcome struct {
	// UnknownResults counts synthetic tool/result events appended for calls
	// that were dispatched but never answered.
	UnknownResults int
	// NotStartedResults counts those appended for tool requests the model made
	// that the harness never recorded as dispatched.
	NotStartedResults int
	// ClosedTurn is true when a turn/finished closed an open turn.
	ClosedTurn bool
	// InterruptedPromptID is the prompt id of the closed turn, when its
	// turn/started recorded one.
	InterruptedPromptID int64
}

// crashed is the state of the log's tail while the final turn is unfinished.
type crashed struct {
	openTurn bool
	promptID int64
	// dispatched holds call ids recorded by tool/dispatch whose tool/result has
	// not landed yet.
	dispatched map[string]bool
	// requested holds tool_use block ids the model emitted that never received a
	// tool/dispatch — the model asked, the harness never started it.
	requested map[string]bool
	// toolNames remembers the tool name for a call id so a synthetic result can
	// name it.
	toolNames map[string]string
}

// newCrashed returns an initialized per-turn crash state.
func newCrashed(promptID int64) crashed {
	return crashed{
		openTurn:   true,
		promptID:   promptID,
		dispatched: map[string]bool{},
		requested:  map[string]bool{},
		toolNames:  map[string]string{},
	}
}

// scanCrash walks the log once and reports the state of its tail. Per-turn
// state resets on every turn/started so an earlier turn's calls can never leak
// into the repair of a later one — a turn is the durable replay boundary.
func (l *Log) scanCrash() crashed {
	var c crashed

	for _, e := range l.rows {
		switch e.Kind {
		case events.TurnStarted:
			var p events.TurnPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				p = events.TurnPayload{}
			}
			c = newCrashed(p.PromptID)
		case events.TurnFinished:
			c = crashed{}
		case events.AssistantMessage:
			if !c.openTurn {
				continue
			}
			var m events.MessagePayload
			if json.Unmarshal(e.Payload, &m) != nil {
				continue
			}
			for _, b := range m.Blocks {
				if b.Type == "tool_use" && b.ID != "" {
					c.requested[b.ID] = true
					c.toolNames[b.ID] = b.Name
				}
			}
		case events.ToolDispatch:
			if !c.openTurn {
				continue
			}
			var d events.ToolDispatchPayload
			if json.Unmarshal(e.Payload, &d) != nil {
				continue
			}
			// The harness started it, so it is no longer "not started", and it
			// is now a call whose result is expected.
			delete(c.requested, d.CallID)
			c.dispatched[d.CallID] = true
			c.toolNames[d.CallID] = d.Name
		case events.ToolResult:
			if !c.openTurn {
				continue
			}
			var r events.ToolResultPayload
			if json.Unmarshal(e.Payload, &r) != nil {
				continue
			}
			// One answer settles both buckets: it started and it finished.
			delete(c.dispatched, r.CallID)
			delete(c.requested, r.CallID)
		}
	}
	return c
}

// Repair closes an interrupted final turn. It is called on load, after the rows
// are read and the shadow boundary is restored, and appends only what is
// missing: a tool/result for every unanswered call, then the turn/finished that
// closes the turn. Seqs continue from the last row's and the timestamp is
// reused so a replay stays deterministic and never invents a future time.
//
// A balanced log (no open turn, or an open turn with no outstanding calls) is
// left untouched and reports zero.
func (l *Log) Repair() (RepairOutcome, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	c := l.scanCrash()
	var out RepairOutcome
	if !c.openTurn {
		return out, nil
	}
	out.InterruptedPromptID = c.promptID

	// A call the harness started but never answered: the honest verdict is
	// "unknown", with the rule for deciding whether a retry is safe.
	for callID := range c.dispatched {
		p := events.ToolResultPayload{
			CallID:  callID,
			Name:    c.toolNames[callID],
			IsError: true,
			Content: "The tool call was interrupted after it was recorded, but no result was durably " +
				"recorded. Its outcome is unknown. Decide whether to retry from the tool semantics: " +
				"retry only if the operation is read-only or idempotent; if it may have side effects, " +
				"first verify external state or ask the user. Do not retry blindly.",
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return out, err
		}
		if _, err := l.appendLocked(events.ToolResult, raw); err != nil {
			return out, err
		}
		out.UnknownResults++
	}
	// A request the harness never started: cheaper — retrying is safe because
	// nothing ran — but the model still has to want it.
	for callID := range c.requested {
		p := events.ToolResultPayload{
			CallID:  callID,
			Name:    c.toolNames[callID],
			IsError: true,
			Content: "The tool call was interrupted before the harness recorded it as started. " +
				"Retry it if it is still needed.",
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return out, err
		}
		if _, err := l.appendLocked(events.ToolResult, raw); err != nil {
			return out, err
		}
		out.NotStartedResults++
	}

	// Close the turn. "interrupted" is distinct from "cancelled" (a user
	// action) and "failed" (the model's own error): it names a crash.
	tp := events.TurnPayload{PromptID: c.promptID, Outcome: "interrupted"}
	raw, err := json.Marshal(tp)
	if err != nil {
		return out, err
	}
	if _, err := l.appendLocked(events.TurnFinished, raw); err != nil {
		return out, err
	}
	out.ClosedTurn = true
	return out, nil
}

// appendLocked is the locked append path Repair shares with Append, without
// re-taking the mutex.
func (l *Log) appendLocked(kind events.Kind, raw []byte) (int64, error) {
	at := time.Now().UTC()
	if n := len(l.rows); n > 0 {
		at = l.rows[n-1].At // deterministic: never invent a time past the crash
	}
	seq, err := l.store.AppendEvent(l.key, kind, raw, at)
	if err != nil {
		return 0, err
	}
	l.rows = append(l.rows, Event{Seq: seq, Kind: kind, Payload: raw, At: at})
	l.derivedValid = false
	return seq, nil
}
