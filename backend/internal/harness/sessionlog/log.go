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
	"log"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
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
	mu    sync.Mutex
	store Store
	key   string
	rows  []Event
	// repairOnLoad closes an interrupted final turn when the log is loaded —
	// the integrity gate for derivation (W2.4): the projection is only ever
	// taken from a repaired tail, so a crash can never hand the provider an
	// unanswered tool_use or a turn missing its terminal marker, and legacy
	// and derived reads agree after a restart. It is on by default; tests
	// that want to inspect a crashed tail construct a log with it off. See
	// crash.go.
	repairOnLoad bool
	// shadowSeq, when > 0, is the highest seq shadowed for derivation: every
	// surface event at or before it is replaced by the summary in
	// shadowSummary for DeriveMessages. The rows themselves stay.
	shadowSeq     int64
	shadowSummary string
	// derived caches the projection; invalidated by any append or shadow
	// change so the next DeriveMessages re-derives.
	derived      []DerivedMessage
	derivedValid bool
}

// Store is the persistence seam: whatever database the host composition
// chooses. It sees only (key, seq, kind, payload, at) rows — the log owns the
// projection logic, and a backend swap changes no caller.
// Store is the persistence backend for one log. AppendEvent and EventsFrom are
// the append/read path; Sync is the durability barrier — it forces everything
// recorded so far onto stable storage so the log survives a hard crash (power
// loss, OS panic), not just a process exit. SQLite in WAL with synchronous=NORMAL
// is durable across process crashes but not across power loss until a checkpoint
// runs; the checkpoint policy calls Sync at the semantic boundaries.
type Store interface {
	AppendEvent(key string, kind events.Kind, payload []byte, at time.Time) (seq int64, err error)
	EventsFrom(key string, fromSeq int64) ([]Event, error)
	Sync() error
}

// New creates a log backed by store, loading any persisted events under key so
// a restarted process resumes a live session's history rather than starting a
// second one alongside it.
func New(store Store, key string) (*Log, error) {
	l := &Log{store: store, key: key, repairOnLoad: true}
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
	// Close an interrupted final turn before anyone derives from the log: a
	// crash leaves tool requests with no answers, and replaying those would
	// hand the provider a conversation it must reject. See crash.go.
	if l.repairOnLoad {
		if rep, err := l.Repair(); err != nil {
			return nil, fmt.Errorf("sessionlog: could not close the interrupted turn: %w", err)
		} else if rep.ClosedTurn || rep.UnknownResults > 0 || rep.NotStartedResults > 0 {
			log.Printf("sessionlog %s: recovered a crashed turn (%d unknown results, %d not-started, turn closed=%t)",
				key, rep.UnknownResults, rep.NotStartedResults, rep.ClosedTurn)
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

// Sync forces everything recorded so far onto stable storage. It is the
// durability barrier the checkpoint policy uses: a non-nil error means the log
// is not trustworthy and a side effect recorded but not synced must not run.
func (l *Log) Sync() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.store.Sync()
}

// Seq returns the current log length.
func (l *Log) Seq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.rows))
}

// Key is the log's stable storage key — the identifier lineage events record
// as a fork's origin or child. The key never changes after construction, so
// it needs no lock.
func (l *Log) Key() string { return l.key }

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
	if _, err := l.Append(events.SessionCompacted, events.CompactionSummaryPayload{Summary: summary}); err != nil {
		return err
	}
	return nil
}

// Retain is the W1.1 replacement for destructive storage trimming: it keeps the
// newest keep surface messages derivation-visible by shadowing everything
// before them, without deleting anything. It scans Replay backward counting
// UserMessage/AssistantMessage rows; the boundary is the seq of the row just
// before the oldest kept message, so shadowing at or before it leaves that
// message and every newer one visible to DeriveMessages while Replay still
// holds the full history.
//
// It writes no marker — returning 0, nil — when there is nothing to shadow
// (fewer than keep messages, or the oldest kept message is the first row) or
// when the existing shadow already covers the boundary, which makes a repeated
// Retain with the same keep idempotent: no duplicate markers pile up. The
// returned boundary is non-zero exactly when a marker landed, so the caller
// can audit only real shadows.
func (l *Log) Retain(keep int, summary, reason string) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	rows := l.Replay()
	oldestKept := -1
	for i, counted := len(rows)-1, 0; i >= 0; i-- {
		if rows[i].Kind == events.UserMessage || rows[i].Kind == events.AssistantMessage {
			counted++
			if counted == keep {
				oldestKept = i
				break
			}
		}
	}
	if oldestKept <= 0 {
		// -1: fewer than keep surface messages — all of them stay visible.
		// 0: the kept window starts at the first row, nothing precedes it.
		return 0, nil
	}
	boundary := rows[oldestKept-1].Seq

	l.mu.Lock()
	covered := boundary <= l.shadowSeq
	l.mu.Unlock()
	if covered {
		return 0, nil
	}
	if err := l.Shadow(boundary, summary, reason); err != nil {
		return 0, err
	}
	return boundary, nil
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
	Type         string
	Text         string
	ID           string
	Name         string
	Input        json.RawMessage
	Content      string
	IsError      bool
	Signature    string
	Plan         json.RawMessage
	AttachmentID int64
	Mime         string
	// turn_end marker fields — parity with the legacy row's status row.
	Status string
	Error  string
	Rounds int
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
			db := DerivedBlock{Type: b.Type, Text: b.Text, ID: b.ID, Name: b.Name, Content: b.Content, IsError: b.IsError, Signature: b.Signature, AttachmentID: b.AttachmentID, Mime: b.Mime, Status: b.Status, Error: b.Error, Rounds: b.Rounds}
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
				Type: "tool_result", ID: p.CallID, Name: p.Name, Content: p.Content, IsError: p.IsError,
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
