package store

import (
	"path/filepath"
	"testing"

	// The sqlite driver registers itself on import; the store package relies on
	// the main binary for this, so the test binary must pull it in directly.
	_ "modernc.org/sqlite"
)

// newTestStore opens a real sqlite file (New chmods the path, so :memory: won't
// do) in a temp dir and runs migrations.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// seedProject creates the user+project a conversation hangs off of. Foreign keys
// are ON, so a message needs a real project to reference.
func seedProject(t *testing.T, s *Store) int64 {
	t.Helper()
	u, err := s.CreateUser("dev@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p, err := s.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID
}

// The stored conversation is the agent's memory, replayed verbatim each turn.
// Sequence numbers must be strictly increasing and dense so replay order is the
// order things happened — a follow-up like "now make it blue" depends on it.
func TestAppendMessageSequencing(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	roles := []string{"user", "assistant", "user", "assistant"}
	for _, r := range roles {
		if err := s.AppendMessage(pid, 1, r, `[{"type":"text","text":"hi"}]`, 0, 0); err != nil {
			t.Fatalf("append %s: %v", r, err)
		}
	}

	msgs, err := s.GetConversation(pid, 0)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(msgs) != len(roles) {
		t.Fatalf("stored %d messages, want %d", len(msgs), len(roles))
	}
	for i, m := range msgs {
		if m.Seq != int64(i+1) {
			t.Errorf("message %d has seq %d, want %d (dense, 1-based)", i, m.Seq, i+1)
		}
		if m.Role != roles[i] {
			t.Errorf("message %d is %q, want %q — chronological order broken", i, m.Role, roles[i])
		}
	}
}

// A limited read returns the *tail* of the conversation in chronological order:
// the query pulls newest-first to apply LIMIT, then flips back. Getting that flip
// wrong would replay the conversation backwards.
func TestGetConversationTailInOrder(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	for i := 0; i < 5; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := s.AppendMessage(pid, 1, role, `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	msgs, err := s.GetConversation(pid, 2)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("limit=2 returned %d messages", len(msgs))
	}
	// Newest two are seq 4 then 5, in chronological order.
	if msgs[0].Seq != 4 || msgs[1].Seq != 5 {
		t.Errorf("tail out of order: got seqs %d,%d want 4,5", msgs[0].Seq, msgs[1].Seq)
	}
}

func TestTrimConversationKeepsNewest(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	for i := 0; i < 5; i++ {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	if err := s.TrimConversation(pid, 2); err != nil {
		t.Fatalf("trim: %v", err)
	}
	msgs, err := s.GetConversation(pid, 0)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("after trim to 2, have %d messages", len(msgs))
	}
	if msgs[0].Seq != 4 || msgs[1].Seq != 5 {
		t.Errorf("trim kept the wrong messages: seqs %d,%d want 4,5", msgs[0].Seq, msgs[1].Seq)
	}
}

// Token spend is attributed to the prompt and rolled up onto the user's usage
// row in one call. The token columns are added to both tables by ALTER TABLE in
// migrate() rather than the CREATE TABLE, so this also guards that migration:
// if the columns went missing the UPDATEs would error against the real schema.
func TestRecordPromptTokensRollsUp(t *testing.T) {
	s := newTestStore(t)
	u, err := s.CreateUser("dev@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p, err := s.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	prompt, err := s.CreatePrompt(p.ID, "build", "make a button")
	if err != nil {
		t.Fatalf("create prompt: %v", err)
	}
	// The usage row is created lazily; production hits GetUsage (dashboard) well
	// before the first prompt, so the rollup UPDATE has a row to touch.
	if _, err := s.GetUsage(u.ID); err != nil {
		t.Fatalf("seed usage row: %v", err)
	}

	if err := s.RecordPromptTokens(prompt.ID, u.ID, 100, 40, 10); err != nil {
		t.Fatalf("record tokens: %v", err)
	}
	if err := s.RecordPromptTokens(prompt.ID, u.ID, 50, 20, 5); err != nil {
		t.Fatalf("record tokens (2): %v", err)
	}

	// Neither the Usage struct nor GetPrompt surfaces token totals, so verify the
	// accounting against the columns directly (white-box, same package).
	var pin, pout, pcache int
	if err := s.db.QueryRow(
		"SELECT input_tokens, output_tokens, cache_read_tokens FROM prompts WHERE id = ?", prompt.ID,
	).Scan(&pin, &pout, &pcache); err != nil {
		t.Fatalf("read prompt tokens: %v", err)
	}
	if pin != 150 || pout != 60 || pcache != 15 {
		t.Errorf("prompt rollup wrong: in=%d out=%d cache=%d want 150/60/15", pin, pout, pcache)
	}

	var uin, uout int
	if err := s.db.QueryRow(
		"SELECT input_tokens, output_tokens FROM usage WHERE user_id = ?", u.ID,
	).Scan(&uin, &uout); err != nil {
		t.Fatalf("read usage tokens: %v", err)
	}
	if uin != 150 || uout != 60 {
		t.Errorf("usage rollup wrong: in=%d out=%d want 150/60", uin, uout)
	}
}
