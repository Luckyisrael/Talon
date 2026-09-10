// Package sessionlog is the event-sourced session record: an append-only log
// that is the single source of truth for everything an agent turn does. The
// model-visible message history is DERIVED from the log (DeriveMessages),
// never stored — replay is re-derivation from the same events, so a resumed
// session reconstructs exactly what the model saw. Compaction shadows older
// surface events for derivation without deleting a single log row.
//
// The design rule carried over from the DSH session contract: surface events
// (user/message, assistant/message, tool/result, question/*) declare how they
// join the ordered surface; every other event kind is a durable audit record
// that never produces a message. Appends freeze their payload — what was
// written is what will be read back, byte for byte.
package sessionlog

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/seekersstudio/backend/internal/harness/events"
)

// Event is one accepted, immutable log row.
type Event struct {
	Seq     int64
	Kind    events.Kind
	Payload []byte // frozen JSON; never re-marshalled
	At      time.Time
}

// Log is one session's append-only event log. Safe for concurrent use: the
// mutex orders appends and guards the derivation cache.
type Log struct {
	mu      sync.Mutex
	store   Store
	key     string
	rows    []Event
	// shadowSeq, when > 0, is the highest seq shadowed for derivation: every
	// surface event at or before it is replaced by the summary in
	// shadowSummary for DeriveMessages. The rows themselves stay.
	shadowSeq    int64
	shadowSummary string
	// derived caches the projection; invalidated by any append or shadow
	// change so the next DeriveMessages re-derives.
	derived []DerivedMessage
	derivedValid bool
}

// Store is the persistence seam: whatever database the host composition
// chooses. It sees only (key, seq, kind, payload, at) rows — the log owns the
// projection logic, and a backend swap changes no caller.
type Store interface {
	AppendEvent(key string, kind events.Kind, payload []byte, at time.Time) (seq int64, err error)
	EventsFrom(key string, fromSeq int64) ([]Event, error)
}

// New creates a log backed by store, loading any persisted events under key so
// a restarted process resumes a live session's history rather than starting a
// second one alongside it.
func New(store Store, key string) (*Log, error) {
	l := &Log{store: store, key: key}
	rows, err := store.EventsFrom(key, 0)
	if err != nil {
		return nil, err
	}
	l.rows = rows
	// Re-derive the shadow from the loaded tail so a restart preserves the
	// compaction boundary exactly.
	for i := len(l.rows) - 1; i >= 0; i-- {
		if l.rows[i].Kind == events.CompactionShadow {
			var p events.CompactionPayload
			if json.Unmarshal(l.rows[i].Payload, &p) == nil {
				l.shadowSeq = p.ShadowFromSeq
			}
			break
		}
	}
	for i := len(l.rows) - 1; i >= 0; i-- {
		if l.rows[i].Kind == events.SessionCompacted {
			var p events.CompactionSummaryPayload
			if json.Unmarshal(l.rows[i].Payload, &p) == nil && p.Summary != "" {
				l.shadowSummary = p.Summary
				break
			}
		}
	}
	return l, nil
}

// Append validates, freezes, and persists one event, returning its seq. The
// payload must be lossless JSON — a non-marshalling payload is a caller bug and
// fails the append rather than corrupting the log.
func (l *Log) Append(kind events.Kind, payload any) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("sessionlog: payload for %s does not marshal: %w", kind, err)
	}
	// Freeze immediately: unmarshal+remarshal would reorder keys and break
	// byte-stable replay comparisons.
	frozen, err := json.Marshal(json.RawMessage(raw))
	_ = frozen
	_ = err

	l.mu.Lock()
	defer l.mu.Unlock()
	seq, err := l.store.AppendEvent(l.key, kind, raw, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	l.rows = append(l.rows, Event{Seq: seq, Kind: kind, Payload: raw, At: time.Now().UTC()})
	l.derivedValid = false
	return seq, nil
}

// AppendPayload appends an event whose payload is already frozen JSON.
func (l *Log) AppendPayload(kind events.Kind, raw []byte) (int64, error) {
	var probe any
	if json.Unmarshal(raw, &probe) != nil {
		return 0, fmt.Errorf("sessionlog: payload for %s is not valid JSON", kind)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	seq, err := l.store.AppendEvent(l.key, kind, raw, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	l.rows = append(l.rows, Event{Seq: seq, Kind: kind, Payload: raw, At: time.Now().UTC()})
	l.derivedValid = false
	return seq, nil
}

// Seq returns the current log length.
func (l *Log) Seq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.rows))
}

// EventAt returns one frozen event by zero-based position.
func (l *Log) EventAt(i int) (Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i < 0 || i >= len(l.rows) {
		return Event{}, false
	}
	return l.rows[i], true
}

// SnapshotEvents returns a stable copy of the half-open range [from, to) by
// position. to <= 0 means to the end. The copy is frozen: later appends
// cannot mutate it.
func (l *Log) SnapshotEvents(from, to int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from < 0 {
		from = 0
	}
	if to <= 0 || to > len(l.rows) {
		to = len(l.rows)
	}
	if from >= to {
		return nil
	}
	out := make([]Event, to-from)
	copy(out, l.rows[from:to])
	return out
}

// Shadow marks every surface event at or before fromSeq as shadowed for
// derivation, with summary as the replacement content. It appends a
// compaction/shadow event (the boundary itself is durable — replay must see
// where history was condensed) and clears the derivation cache. Rows before
// the boundary are NOT deleted; Replay still sees them.
func (l *Log) Shadow(fromSeq int64, summary, reason string) error {
	l.mu.Lock()
	l.shadowSeq = fromSeq
	l.shadowSummary = summary
	l.derivedValid = false
	l.mu.Unlock()
	if _, err := l.Append(events.CompactionShadow, events.CompactionPayload{ShadowFromSeq: fromSeq, Reason: reason}); err != nil {
		return err
	}
	_, err := l.Append(events.SessionCompacted, events.CompactionSummaryPayload{Summary: summary})
	return err
}

// ── Derivation ────────────────────────────────────────

// DerivedMessage is one model-visible message produced by projecting the log.
type DerivedMessage struct {
	Role   string
	Blocks []DerivedBlock
	// Interrupted marks an assistant anchor that carries only the delivered
	// prefix of a cancelled stream.
	Interrupted bool
}

// DerivedBlock is one content block of a derived message.
type DerivedBlock struct {
	Type      string
	Text      string
	ID        string
	Name      string
	Input     json.RawMessage
	Content   string
	IsError   bool
	Signature string
	Plan      json.RawMessage
}

// DeriveMessages projects the log into the ordered, model-visible surface:
// the message list a provider request carries. Shadowed surface events are
// replaced by the compaction summary as a single user message; surface events
// after the boundary project normally. Non-surface events never produce
// messages. The projection is cached until the next append or shadow change.
func (l *Log) DeriveMessages() []DerivedMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.derivedValid {
		return l.derived
	}
	var out []DerivedMessage
	appendPayload := func(raw []byte) DerivedMessage {
		var m DerivedMessage
		var p events.MessagePayload
		if json.Unmarshal(raw, &p) != nil {
			return m
		}
		m.Role = p.Role
		m.Interrupted = p.Interrupted
		m.Blocks = make([]DerivedBlock, 0, len(p.Blocks))
		for _, b := range p.Blocks {
			db := DerivedBlock{Type: b.Type, Text: b.Text, ID: b.ID, Name: b.Name, Content: b.Content, IsError: b.IsError, Signature: b.Signature}
			if b.Input != nil {
				db.Input = json.RawMessage(*b.Input)
			}
			if b.Plan != nil {
				db.Plan = json.RawMessage(*b.Plan)
			}
			m.Blocks = append(m.Blocks, db)
		}
		return m
	}

	for i, e := range l.rows {
		if l.shadowSeq > 0 && e.Seq <= l.shadowSeq {
			continue // shadowed for derivation; the row stays for replay
		}
		switch e.Kind {
		case events.UserMessage, events.AssistantMessage:
			if m := appendPayload(e.Payload); m.Role != "" {
				out = append(out, m)
			}
		case events.ToolResult:
			var p events.ToolResultPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			out = append(out, DerivedMessage{Role: "user", Blocks: []DerivedBlock{{
				Type: "tool_result", ID: p.CallID, Content: p.Content, IsError: p.IsError,
			}}})
		case events.QuestionAsked:
			var p events.QuestionPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			// A question is an assistant tool_use on the surface.
			input, _ := json.Marshal(map[string]any{"question": p.Text, "choices": p.Choices})
			out = append(out, DerivedMessage{Role: "assistant", Blocks: []DerivedBlock{{
				Type: "tool_use", ID: p.QuestionID, Name: "ask_user", Input: input,
			}}})
		case events.QuestionAnswered:
			var p events.QuestionAnswerPayload
			if json.Unmarshal(e.Payload, &p) != nil {
				continue
			}
			out = append(out, DerivedMessage{Role: "user", Blocks: []DerivedBlock{{
				Type: "tool_result", ID: p.QuestionID, Content: p.Answer,
			}}})
		}
		_ = i
	}

	// The compaction summary rides at the head, as the first thing the model
	// sees after the boundary: it stands in for everything shadowed.
	if l.shadowSeq > 0 && l.shadowSummary != "" {
		summary := DerivedMessage{Role: "user", Blocks: []DerivedBlock{{
			Type: "text",
			Text: "[earlier conversation condensed] " + l.shadowSummary,
		}}}
		out = append([]DerivedMessage{summary}, out...)
	}

	l.derived = out
	l.derivedValid = true
	return out
}

// Replay returns every log row — shadowed or not — for replay, audit, and
// byte-stable comparisons. DeriveMessages decides what the model sees; the
// log itself never lies about what happened.
func (l *Log) Replay() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.rows))
	copy(out, l.rows)
	return out
}

// Fork clones the log into a child key carrying the parent's events up to and
// including fromSeq — a stable prefix. Later appends to the parent never reach
// the child, and the child's own appends go to its own key. The store seam
// handles the physical copy.
func (l *Log) Fork(store Store, childKey string, fromSeq int64) (*Log, error) {
	l.mu.Lock()
	prefix := make([]Event, 0)
	for _, e := range l.rows {
		if fromSeq > 0 && e.Seq > fromSeq {
			break
		}
		prefix = append(prefix, e)
	}
	l.mu.Unlock()

	child := &Log{store: store, key: childKey, rows: make([]Event, 0, len(prefix))}
	for _, e := range prefix {
		if _, err := store.AppendEvent(childKey, e.Kind, e.Payload, e.At); err != nil {
			return nil, err
		}
		child.rows = append(child.rows, e)
	}
	// A fork inherits the parent's shadow boundary so its derivation starts
	// from the same condensed state.
	if l.shadowSeq > 0 {
		child.shadowSeq = l.shadowSeq
		child.shadowSummary = l.shadowSummary
		child.derivedValid = false
	}
	return child, nil
}
