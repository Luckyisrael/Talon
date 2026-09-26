package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/events"

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

// Concurrent turns record metering from many goroutines at once. The rollups
// must sum exactly — a lost UPDATE here silently under-bills. Run with -race:
// modernc/sqlite serializes internally, but our read-modify-write would not
// survive shared state, so this also guards future caching layers.
func TestMeteringRaceSafe(t *testing.T) {
	s := newTestStore(t)
	u, err := s.CreateUser("race@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := s.CreatePrompt(p.ID, "build", "make a button")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUsage(u.ID); err != nil {
		t.Fatal(err)
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RecordPromptTokens(prompt.ID, u.ID, 100, 40, 10); err != nil {
				errs <- err
				return
			}
			if err := s.RecordPromptRouting(prompt.ID, u.ID, 0.01, "m", "test"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	var in, out, cache int
	var cost float64
	var model, reason string
	err = s.db.QueryRow("SELECT input_tokens, output_tokens, cache_read_tokens, cost_usd, routed_model, route_reason FROM prompts WHERE id = ?",
		prompt.ID).Scan(&in, &out, &cache, &cost, &model, &reason)
	if err != nil {
		t.Fatal(err)
	}
	if in != 1600 || out != 640 || cache != 160 {
		t.Errorf("prompt tokens lost updates: in=%d out=%d cache=%d", in, out, cache)
	}
	if cost < 0.159 || cost > 0.161 {
		t.Errorf("prompt cost wrong: %v", cost)
	}
	usage, err := s.GetUsage(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.CostUSD < 0.159 || usage.CostUSD > 0.161 {
		t.Errorf("usage cost lost updates: %v", usage.CostUSD)
	}
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

// seedTurn writes one turn the way the loop persists it: the opener row,
// then an assistant tool_use / user tool_result pair per round, all rows
// sharing the turn's prompt_id.
func seedTurn(t *testing.T, s *Store, pid, promptID int64, opener string, rounds int) {
	t.Helper()
	if err := s.AppendMessage(pid, promptID, "user",
		`[{"type":"text","text":"`+opener+`"}]`, 0, 0); err != nil {
		t.Fatalf("append opener: %v", err)
	}
	for r := 0; r < rounds; r++ {
		call := fmt.Sprintf(`c%d_%d`, promptID, r)
		if err := s.AppendMessage(pid, promptID, "assistant",
			`[{"type":"tool_use","id":"`+call+`","name":"read_file","input":{"path":"a.tsx"}}]`, 0, 0); err != nil {
			t.Fatalf("append tool_use: %v", err)
		}
		if err := s.AppendMessage(pid, promptID, "user",
			`[{"type":"tool_result","tool_use_id":"`+call+`","content":"file body"}]`, 0, 0); err != nil {
			t.Fatalf("append tool_result: %v", err)
		}
	}
}

// The model's replay window must open on a turn's opener, never on a
// fragment of a long previous turn. The raw tail of a turn that wrote more
// rows than the budget starts mid-round; the window pulls back to the row
// where prompt_id changes so the goal the model needs is in frame.
func TestGetConversationWindowExtendsToTurnStart(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)
	seedTurn(t, s, pid, 1, "the original request", 3) // seq 1-7
	seedTurn(t, s, pid, 2, "follow up", 1)            // seq 8-10

	// The defect being fixed: a 4-row tail starts at seq 7 — mid-turn-1.
	raw, err := s.GetConversation(pid, 4)
	if err != nil {
		t.Fatalf("raw tail: %v", err)
	}
	if len(raw) != 4 || raw[0].Seq == 1 {
		t.Fatalf("fixture wrong: raw tail seqs start at %d, want 7 (mid-turn)", raw[0].Seq)
	}

	win, err := s.GetConversationWindow(pid, 4, 24)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if len(win) != 10 {
		t.Fatalf("window has %d rows, want the full history (7+3)", len(win))
	}
	if win[0].Seq != 1 || win[0].PromptID != 1 {
		t.Errorf("window opens at seq %d (prompt %d), want the turn-1 opener seq 1 (prompt 1)",
			win[0].Seq, win[0].PromptID)
	}
	if win[len(win)-1].Seq != 10 {
		t.Errorf("window tail at seq %d, want 10 (the newest row)", win[len(win)-1].Seq)
	}
}

// A window that already starts at a turn boundary is returned untouched —
// alignment must not churn the common case where the budget lands exactly
// on an opener.
func TestGetConversationWindowNoOpAtBoundary(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)
	seedTurn(t, s, pid, 1, "first", 3) // seq 1-7
	seedTurn(t, s, pid, 2, "second", 1)

	// keep=3 starts exactly at turn 2's opener (seq 8).
	raw, err := s.GetConversation(pid, 3)
	if err != nil {
		t.Fatalf("raw tail: %v", err)
	}
	win, err := s.GetConversationWindow(pid, 3, 40)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if len(win) != len(raw) {
		t.Fatalf("aligned window has %d rows, want %d (no extension at a boundary)", len(win), len(raw))
	}
	for i := range win {
		if win[i].Seq != raw[i].Seq {
			t.Fatalf("window differs from raw tail at %d: seq %d vs %d", i, win[i].Seq, raw[i].Seq)
		}
	}

	// A degenerate budget (max <= keep) is the raw tail by contract.
	bounded, err := s.GetConversationWindow(pid, 3, 3)
	if err != nil {
		t.Fatalf("bounded window: %v", err)
	}
	if len(bounded) != 3 || bounded[0].Seq != 8 {
		t.Errorf("max<=keep must be the raw tail, got %d rows from seq %d", len(bounded), bounded[0].Seq)
	}
}

// One turn longer than the whole budget: the extension fills the window up
// to `max` and stops there — bounded degradation, never unbounded growth.
func TestGetConversationWindowBoundsExtension(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)
	seedTurn(t, s, pid, 1, "a very long turn", 6) // seq 1-13, one prompt_id

	win, err := s.GetConversationWindow(pid, 4, 8)
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if len(win) != 8 {
		t.Fatalf("window has %d rows, want the max (8)", len(win))
	}
	if win[0].Seq != 6 || win[len(win)-1].Seq != 13 {
		t.Errorf("window covers seq %d..%d, want 6..13 (extended to the bound)", win[0].Seq, win[len(win)-1].Seq)
	}
}

// W1.3: search unions FTS5 and LIKE behind one signature. Each leg covers a
// class the other misses — porter stems for FTS, mid-token substrings and
// literal metacharacters for LIKE — a turn both legs match comes back
// exactly once, and the window is the newest `limit` matches in turn order.
func TestSearchConversationBlocksFTSUnion(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	for _, text := range []string{"the build is running now", "discount 100% today", "unrelated hello"} {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"`+text+`"}]`, 0, 0); err != nil {
			t.Fatalf("append %q: %v", text, err)
		}
	}

	seqsOf := func(q string, limit int) []int64 {
		t.Helper()
		turns, err := s.SearchConversationBlocks(pid, q, limit)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		seqs := make([]int64, 0, len(turns))
		for _, m := range turns {
			seqs = append(seqs, m.Seq)
		}
		return seqs
	}
	want := func(q string, got []int64, wantSeqs ...int64) {
		t.Helper()
		if len(got) != len(wantSeqs) {
			t.Fatalf("q=%q: seqs %v, want %v", q, got, wantSeqs)
		}
		for i := range wantSeqs {
			if got[i] != wantSeqs[i] {
				t.Fatalf("q=%q: seqs %v, want %v (turn order)", q, got, wantSeqs)
			}
		}
	}

	// FTS-only class: "runs" stems to "run", matching "running" — the
	// substring path never sees it.
	want("runs", seqsOf("runs", 20), 1)
	// LIKE-only class: "uild" is a mid-token substring of "build", which
	// FTS never tokenizes.
	want("uild", seqsOf("uild", 20), 1)
	// Metacharacters stay literal across both legs: "100%" is one turn,
	// not every turn.
	want("100%", seqsOf("100%", 20), 2)
	// Dedup: "today" matches both legs (token + substring) — one turn,
	// not two copies.
	want("today", seqsOf("today", 20), 2)
	// "text" hits every turn on both legs (it is in the stored JSON
	// itself): the window is the newest `limit` matches, in turn order.
	want("text", seqsOf("text", 2), 2, 3)
}

// W1.1: trim shadows derivation, never storage. With the flag on, a keep=2
// trim bounds what the model sees (summary + newest two) while every row and
// every log event survives byte-identical — and a repeat trim is idempotent:
// no duplicate markers, no second audit row (only real shadows audit).
func TestTrimConversationShadowsInsteadOfDeleting(t *testing.T) {
	t.Setenv("TALONSTUDIO_TRIM_SHADOW", "1")
	s := newTestStore(t)
	pid := seedProject(t, s)
	lg, err := s.SessionLog(fmt.Sprintf("project-%d", pid))
	if err != nil {
		t.Fatalf("session log: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := lg.Append(events.UserMessage, events.MessagePayload{
			Role: "user", Blocks: []events.BlockPayload{{Type: "text", Text: "x"}},
		}); err != nil {
			t.Fatalf("log append: %v", err)
		}
	}

	if err := s.TrimConversation(lg, pid, 2); err != nil {
		t.Fatalf("trim: %v", err)
	}

	// Storage is untouched: all five rows survive, in order.
	msgs, err := s.GetConversation(pid, 0)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("trim deleted rows: have %d messages, want 5", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != int64(i+1) {
			t.Errorf("row %d has seq %d, want %d", i, m.Seq, i+1)
		}
	}

	// The log keeps the full history (replay reproduces it) plus the two
	// additive markers; derivation alone is bounded: summary + 2 kept.
	if got := len(lg.Replay()); got != 7 {
		t.Errorf("replay after trim: %d events, want 7 (5 messages + shadow + summary)", got)
	}
	if got := len(lg.DeriveMessages()); got != 3 {
		t.Errorf("derived %d messages after trim, want 3 (summary + 2 kept)", got)
	}

	// Idempotent re-trim: boundary already covered → no marker, no audit.
	if err := s.TrimConversation(lg, pid, 2); err != nil {
		t.Fatalf("re-trim: %v", err)
	}
	if got := len(lg.Replay()); got != 7 {
		t.Errorf("re-trim appended markers: replay %d, want 7", got)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'trim_conversation'`).Scan(&audits); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if audits != 1 {
		t.Errorf("trim_conversation audit rows = %d, want 1 (only the landed shadow audits)", audits)
	}
}

// Flag dark (the shipped default): trim is a complete no-op — no markers, no
// audit, nothing bounded. This is what keeps the behavior rollback-free.
func TestTrimConversationFlagDarkNoop(t *testing.T) {
	t.Setenv("TALONSTUDIO_TRIM_SHADOW", "")
	s := newTestStore(t)
	pid := seedProject(t, s)
	lg, err := s.SessionLog(fmt.Sprintf("project-%d", pid))
	if err != nil {
		t.Fatalf("session log: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
		if _, err := lg.Append(events.UserMessage, events.MessagePayload{
			Role: "user", Blocks: []events.BlockPayload{{Type: "text", Text: "x"}},
		}); err != nil {
			t.Fatalf("log append: %v", err)
		}
	}

	if err := s.TrimConversation(lg, pid, 2); err != nil {
		t.Fatalf("trim: %v", err)
	}
	if got := len(lg.Replay()); got != 5 {
		t.Errorf("flag-dark trim wrote events: replay %d, want 5", got)
	}
	if got := len(lg.DeriveMessages()); got != 5 {
		t.Errorf("flag-dark trim bounded derivation: %d, want 5", got)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'trim_conversation'`).Scan(&audits); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if audits != 0 {
		t.Errorf("flag-dark trim audited %d rows, want 0", audits)
	}
}

// W1.1: rows a legacy build archived (the write-only table its destructive
// trim filled) come home on the next boot, and the table retires — the archive
// finally gains a reader. Recovery restores order: seqs 1..5 continuous.
func TestMigrateRecoversArchive(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)
	for i := 0; i < 5; i++ {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	simulateLegacyTrim(t, s, pid, 3)

	if err := s.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	msgs, err := s.GetConversation(pid, 0)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("recovered %d rows, want 5", len(msgs))
	}
	for i, m := range msgs {
		if m.Seq != int64(i+1) {
			t.Errorf("row %d has seq %d, want %d (recovery must restore order)", i, m.Seq, i+1)
		}
	}
	if archiveExists(t, s) {
		t.Error("conversation_archive survived recovery; it should be dropped")
	}

	// A second boot is a clean no-op, not a duplicate insert.
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := countMessages(t, s, pid); got != 5 {
		t.Errorf("second recovery pass: %d rows, want 5", got)
	}
}

// A wipe outranks recovery: archived rows from before a clear_conversation
// must not resurrect history the user explicitly destroyed — but the table
// still retires (an empty copy is a successful copy).
func TestMigratePurgesClearedArchive(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)
	for i := 0; i < 4; i++ {
		if err := s.AppendMessage(pid, 1, "user", `[{"type":"text","text":"x"}]`, 0, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	simulateLegacyTrim(t, s, pid, 2)
	// Backdate the archive write so the clear below is unambiguously later —
	// CURRENT_TIMESTAMP only has second granularity.
	if _, err := s.db.Exec(`UPDATE conversation_archive SET archived_at = '2020-01-01 00:00:00' WHERE project_id = ?`, pid); err != nil {
		t.Fatalf("backdate archive: %v", err)
	}
	if err := s.ClearConversation(pid); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if err := s.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := countMessages(t, s, pid); got != 0 {
		t.Errorf("recovery resurrected %d cleared rows, want 0", got)
	}
	if archiveExists(t, s) {
		t.Error("conversation_archive survived a purge-only recovery")
	}
}

// simulateLegacyTrim rebuilds the on-disk state the pre-W1.1 TrimConversation
// produced: rows at or below boundarySeq copied into conversation_archive,
// then deleted from conversation_messages.
func simulateLegacyTrim(t *testing.T, s *Store, pid int64, boundarySeq int64) {
	t.Helper()
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS conversation_archive (
		id INTEGER PRIMARY KEY,
		project_id INTEGER NOT NULL,
		user_id INTEGER,
		prompt_id INTEGER,
		seq INTEGER NOT NULL,
		role TEXT NOT NULL,
		blocks TEXT NOT NULL,
		input_tokens INTEGER,
		output_tokens INTEGER,
		created_at DATETIME,
		archived_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create legacy archive: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO conversation_archive
		(id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at)
		SELECT id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
		FROM conversation_messages WHERE project_id = ? AND seq <= ?`, pid, boundarySeq); err != nil {
		t.Fatalf("archive copy: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM conversation_messages WHERE project_id = ? AND seq <= ?`, pid, boundarySeq); err != nil {
		t.Fatalf("archive delete: %v", err)
	}
}

func archiveExists(t *testing.T, s *Store) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='conversation_archive'`,
	).Scan(&n); err != nil {
		t.Fatalf("archive lookup: %v", err)
	}
	return n > 0
}

func countMessages(t *testing.T, s *Store, pid int64) int {
	t.Helper()
	msgs, err := s.GetConversation(pid, 0)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	return len(msgs)
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

// ── Notifications (E) ───────────────────────────────────────────────────────

func TestNotificationRecordAndList(t *testing.T) {
	s := newTestStore(t)
	u, err := s.UpsertOAuthUser("github", "9012", "notify@example.com", "Notify Test", "")
	if err != nil {
		t.Fatalf("UpsertOAuthUser: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.RecordNotification(u.ID, "build_success", "Build finished", "verified: true"); err != nil {
			t.Fatalf("RecordNotification %d: %v", i, err)
		}
	}
	items, err := s.ListNotifications(u.ID, 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("rows = %d (want 3)", len(items))
	}
	// Newest first: the last recorded is the first listed.
	if items[0].Title != "Build finished" || items[0].Kind != "build_success" {
		t.Fatalf("first row = %+v", items[0])
	}
	if items[0].ReadAt != nil {
		t.Fatal("a fresh notification is already read")
	}

	// Mark one read — exactly once.
	if err := s.MarkNotificationRead(u.ID, items[0].ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	again, _ := s.ListNotifications(u.ID, 10)
	if again[0].ReadAt == nil {
		t.Fatal("read_at did not stamp")
	}
	if again[1].ReadAt != nil || again[2].ReadAt != nil {
		t.Fatal("mark-read leaked to other rows")
	}
}

func TestPushTokenRoundTrip(t *testing.T) {
	s := newTestStore(t)
	u, err := s.UpsertOAuthUser("github", "9013", "push@example.com", "Push Test", "")
	if err != nil {
		t.Fatalf("UpsertOAuthUser: %v", err)
	}

	id, err := s.GetOneSignalID(u.ID)
	if err != nil || id != "" {
		t.Fatalf("fresh user push target = %q, %v (want empty)", id, err)
	}
	if err := s.SetOneSignalID(u.ID, "os-ext-123"); err != nil {
		t.Fatalf("SetOneSignalID: %v", err)
	}
	id, err = s.GetOneSignalID(u.ID)
	if err != nil || id != "os-ext-123" {
		t.Fatalf("push target after set = %q, %v", id, err)
	}
}
