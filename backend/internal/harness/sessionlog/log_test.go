package sessionlog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/seekersstudio/backend/internal/harness/events"
)

// memStore is an in-memory sessionlog.Store for tests: ordered rows per key,
// byte-frozen payloads, no deletion — the same contract the SQLite backend
// honours.
type memStore struct {
	rows map[string][]Event
	seq  map[string]int64
}

func newMemStore() *memStore {
	return &memStore{rows: map[string][]Event{}, seq: map[string]int64{}}
}

func (m *memStore) AppendEvent(key string, kind events.Kind, payload []byte, at time.Time) (int64, error) {
	m.seq[key]++
	m.rows[key] = append(m.rows[key], Event{Seq: m.seq[key], Kind: kind, Payload: payload, At: at})
	return m.seq[key], nil
}

func (m *memStore) EventsFrom(key string, fromSeq int64) ([]Event, error) {
	var out []Event
	for _, e := range m.rows[key] {
		if e.Seq > fromSeq {
			out = append(out, e)
		}
	}
	return out, nil
}

func msg(role string, text string) events.MessagePayload {
	return events.MessagePayload{Role: role, Blocks: []events.BlockPayload{{Type: "text", Text: text}}}
}

func newLog(t *testing.T, key string) *Log {
	t.Helper()
	l, err := New(newMemStore(), key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

// The core H1 guarantee: the same log derives the same messages twice, and a
// log rebuilt from the same persisted rows derives byte-identical messages.
// Replay is re-derivation, never a stored copy.
func TestDeriveMessagesDeterministic(t *testing.T) {
	l := newLog(t, "p1")
	if _, err := l.Append(events.UserMessage, msg("user", "build a wallet screen")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.AssistantMessage, msg("assistant", "on it")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.ToolResult, events.ToolResultPayload{CallID: "c1", Name: "read_file", Content: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.PhaseMark, events.PhasePayload{Phase: "GENERATE"}); err != nil {
		t.Fatal(err)
	}

	first := l.DeriveMessages()
	second := l.DeriveMessages()
	if len(first) != 3 {
		t.Fatalf("expected 3 derived messages, got %d", len(first))
	}
	fj, _ := json.Marshal(first)
	sj, _ := json.Marshal(second)
	if string(fj) != string(sj) {
		t.Error("two derivations of the same log differ")
	}
	// Non-surface events (phase marks) never produce messages.
	for _, m := range first {
		if m.Role == "" {
			t.Error("derived message with empty role")
		}
	}
}

// Shadowing is derivation-only: the model sees the summary plus post-boundary
// events, while Replay still returns every row — compaction never deletes
// history.
func TestShadowPreservesReplay(t *testing.T) {
	l := newLog(t, "p2")
	if _, err := l.Append(events.UserMessage, msg("user", "old turn 1")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.AssistantMessage, msg("assistant", "old reply 1")); err != nil {
		t.Fatal(err)
	}
	boundary := l.Seq()
	if _, err := l.Append(events.UserMessage, msg("user", "new turn")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.AssistantMessage, msg("assistant", "new reply")); err != nil {
		t.Fatal(err)
	}

	if err := l.Shadow(boundary, "built the wallet screen", "test"); err != nil {
		t.Fatal(err)
	}

	derived := l.DeriveMessages()
	if len(derived) != 3 {
		t.Fatalf("expected summary + 2 post-boundary messages, got %d", len(derived))
	}
	if derived[0].Blocks[0].Text != "[earlier conversation condensed] built the wallet screen" {
		t.Errorf("summary not at head: %q", derived[0].Blocks[0].Text)
	}
	if derived[1].Blocks[0].Text != "new turn" {
		t.Errorf("post-boundary user message missing: %q", derived[1].Blocks[0].Text)
	}

	all := l.Replay()
	// 4 surface events + the compaction/shadow + session/compacted markers.
	if len(all) != 6 {
		t.Errorf("shadow must not delete rows: expected 6 in replay, got %d", len(all))
	}
}

// A question round-trip derives the ask_user tool_use / tool_result pair the
// answer path needs: the unanswered tool_use is exactly the turn shape the
// resume flow must keep (the reason storedConversation skips validity trim).
func TestQuestionRoundTrip(t *testing.T) {
	l := newLog(t, "p3")
	if _, err := l.Append(events.QuestionAsked, events.QuestionPayload{QuestionID: "q1", Text: "Which wallet?", Choices: []string{"MWA", "Seed Vault"}}); err != nil {
		t.Fatal(err)
	}
	derived := l.DeriveMessages()
	if len(derived) != 1 || derived[0].Blocks[0].Type != "tool_use" || derived[0].Blocks[0].Name != "ask_user" {
		t.Fatalf("question did not derive as ask_user tool_use: %+v", derived)
	}

	if _, err := l.Append(events.QuestionAnswered, events.QuestionAnswerPayload{QuestionID: "q1", Answer: "MWA"}); err != nil {
		t.Fatal(err)
	}
	derived = l.DeriveMessages()
	if len(derived) != 2 || derived[1].Blocks[0].Type != "tool_result" || derived[1].Blocks[0].ID != "q1" {
		t.Fatalf("answer did not derive as tool_result for q1: %+v", derived)
	}
	if derived[1].Blocks[0].Content != "MWA" {
		t.Errorf("answer content mismatch: %q", derived[1].Blocks[0].Content)
	}
}

// An interrupted assistant anchor carries only the delivered prefix; the
// derivation surfaces it flagged so the resumed request includes exactly what
// the user saw, never more.
func TestInterruptedAnchor(t *testing.T) {
	l := newLog(t, "p4")
	if _, err := l.Append(events.UserMessage, msg("user", "go")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.AssistantMessage, events.MessagePayload{
		Role: "assistant", Interrupted: true,
		Blocks: []events.BlockPayload{{Type: "text", Text: "I'll start with the"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.Interrupted, events.InterruptedPayload{PromptID: 1, Reason: "user cancel", DeliveredPrefix: "I'll start with the"}); err != nil {
		t.Fatal(err)
	}

	derived := l.DeriveMessages()
	if len(derived) != 2 {
		t.Fatalf("expected user + interrupted assistant, got %d", len(derived))
	}
	if !derived[1].Interrupted {
		t.Error("interrupted flag lost in derivation")
	}
	if derived[1].Blocks[0].Text != "I'll start with the" {
		t.Errorf("delivered prefix mismatch: %q", derived[1].Blocks[0].Text)
	}
}

// Fork carries a stable prefix: the child sees the parent's events up to the
// boundary and nothing after, and the parent's later appends never reach it.
func TestForkStablePrefix(t *testing.T) {
	store := newMemStore()
	l, err := New(store, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.UserMessage, msg("user", "turn 1")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.AssistantMessage, msg("assistant", "reply 1")); err != nil {
		t.Fatal(err)
	}
	at := l.Seq()
	if _, err := l.Append(events.UserMessage, msg("user", "turn 2 (parent only)")); err != nil {
		t.Fatal(err)
	}

	child, err := l.Fork(store, "child", at)
	if err != nil {
		t.Fatal(err)
	}
	childDerived := child.DeriveMessages()
	if len(childDerived) != 2 {
		t.Fatalf("fork should carry 2 messages, got %d", len(childDerived))
	}
	for _, m := range childDerived {
		if m.Role == "user" && m.Blocks[0].Text == "turn 2 (parent only)" {
			t.Error("fork leaked parent tail past the boundary")
		}
	}

	// The child appends independently; the parent never sees it.
	if _, err := child.Append(events.UserMessage, msg("user", "child only")); err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Replay() {
		var p events.MessagePayload
		if json.Unmarshal(e.Payload, &p) == nil && p.Role == "user" {
			for _, b := range p.Blocks {
				if b.Text == "child only" {
					t.Error("parent log saw child append")
				}
			}
		}
	}
}

// A restart resumes the same log: New() over an already-populated store
// re-derives identical messages and preserves the shadow boundary.
func TestResumePreservesShadow(t *testing.T) {
	store := newMemStore()
	l1, err := New(store, "p5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l1.Append(events.UserMessage, msg("user", "before shadow")); err != nil {
		t.Fatal(err)
	}
	boundary := l1.Seq()
	if _, err := l1.Append(events.UserMessage, msg("user", "after shadow")); err != nil {
		t.Fatal(err)
	}
	if err := l1.Shadow(boundary, "summary survives restart", "test"); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(l1.DeriveMessages())

	l2, err := New(store, "p5")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(l2.DeriveMessages())
	if string(before) != string(after) {
		t.Errorf("re-opened log derives different messages:\nbefore %s\nafter  %s", before, after)
	}
	if len(l2.Replay()) != 4 { // 2 messages + shadow + summary events
		t.Errorf("re-opened log lost rows: %d", len(l2.Replay()))
	}
}

// Payloads are frozen at append: corrupt JSON is rejected at the boundary,
// never persisted to become a replay-time surprise.
func TestAppendRejectsBadPayload(t *testing.T) {
	l := newLog(t, "p6")
	if _, err := l.Append(events.UserMessage, msg("user", "fine")); err != nil {
		t.Fatal(err)
	}
	// A struct that cannot marshal (channel field) must fail the append.
	if _, err := l.Append(events.UserMessage, struct{ Ch chan int }{Ch: make(chan int)}); err == nil {
		t.Error("non-marshalling payload was accepted")
	}
	if _, err := l.AppendPayload(events.UserMessage, []byte("not json")); err == nil {
		t.Error("invalid raw JSON was accepted")
	}
	if l.Seq() != 1 {
		t.Errorf("rejected appends must not consume seqs: %d", l.Seq())
	}
}
