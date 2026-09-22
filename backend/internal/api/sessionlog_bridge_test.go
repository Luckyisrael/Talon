package api

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// testStore opens a throwaway SQLite store with the session_events table
// migrated, exercising the real backend the bridge will run against. Each
// caller gets its own t.TempDir() so tests never share rows.
func testStore(t *testing.T) sessionStore {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "bridge.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// The H1 bridge contract: an ai.Message written through the bridge must
// derive back into the same ai.Message — byte-identical blocks, paired
// tool_use/tool_result ids, plan blocks preserved. If the round trip loses
// anything, the derivation read path cannot replace the legacy table.
func TestBridgeRoundTrip(t *testing.T) {
	cache := newSessionLogCache()
	// A nil store seam: get() logs and returns nil, and the bridge helpers
	// treat a nil log as a no-op — dual-write never fails a build.
	lg := cache.get(nilStore{}, 1)
	if lg != nil {
		t.Fatal("nil store must yield a nil log, not a panic")
	}

	// Real store-backed log for the round trip.
	lg = cache.get(testStore(t), 1)
	if lg == nil {
		t.Fatal("store-backed log is nil")
	}

	user := ai.UserText("build a wallet screen")
	appendLoggedUser(lg, user, 1)

	// Assistant turn with a tool call and its result — the shapes the loop
	// persists every round.
	toolUse := ai.Message{Role: "assistant", Blocks: []ai.Block{
		{Type: "text", Text: "reading the project first"},
		{Type: "tool_use", ID: "call_1", Name: "list_files", Input: json.RawMessage(`{"path":""}`)},
	}}
	appendLoggedAssistant(lg, toolUse, 1)
	appendLoggedUser(lg, ai.Message{Role: "user", Blocks: []ai.Block{
		{Type: "tool_result", ToolUseID: "call_1", Name: "list_files", Content: "app/ components/"},
	}}, 1)

	// A plan turn — the restore path rebuilds the plan card from it.
	planTurn := ai.AssistantPlan("Here's the plan.", json.RawMessage(`{"screens":[{"name":"Wallet"}]}`))
	appendLoggedAssistant(lg, planTurn, 1)

	derived := deriveToAIMessages(lg.DeriveMessages())
	// user prompt, assistant(text+tool_use), user tool_result, plan reply.
	if len(derived) != 4 {
		t.Fatalf("expected 4 derived messages, got %d: %+v", len(derived), derived)
	}
	if derived[0].Role != "user" || derived[0].Blocks[0].Text != "build a wallet screen" {
		t.Errorf("user turn lost: %+v", derived[0])
	}
	if derived[1].Role != "assistant" || derived[1].Blocks[0].Text != "reading the project first" {
		t.Errorf("assistant text lost: %+v", derived[1])
	}
	if derived[1].Blocks[1].Type != "tool_use" || derived[1].Blocks[1].ID != "call_1" {
		t.Errorf("tool_use lost or unpaired: %+v", derived[1].Blocks[1])
	}
	if derived[2].Blocks[0].Type != "tool_result" || derived[2].Blocks[0].ToolUseID != "call_1" {
		t.Errorf("tool_result not paired to call_1: %+v", derived[2].Blocks[0])
	}
	if derived[3].Blocks[0].Text != "Here's the plan." {
		t.Errorf("plan message lost: %+v", derived[3])
	}
}

// The log must survive shadowing: derivation is bounded but Replay keeps
// everything — the guarantee the W1.1 trim now leans on instead of deleting.
func TestBridgeShadowKeepsFullReplay(t *testing.T) {
	cache := newSessionLogCache()
	lg := cache.get(testStore(t), 2)
	if lg == nil {
		t.Fatal("store-backed log is nil")
	}

	for i := 0; i < 5; i++ {
		appendLoggedUser(lg, ai.UserText("old turn"), 1)
		appendLoggedAssistant(lg, ai.AssistantText("old reply"), 1)
	}
	boundary := lg.Seq()
	appendLoggedUser(lg, ai.UserText("live turn"), 1)

	if err := lg.Shadow(boundary, "five early planning turns", "test"); err != nil {
		t.Fatal(err)
	}
	derived := deriveToAIMessages(lg.DeriveMessages())
	if len(derived) != 2 { // summary + live turn
		t.Fatalf("shadow derivation wrong: %d messages", len(derived))
	}
	if got := len(lg.Replay()); got != 13 { // 10 shadowed + live turn + shadow marker + summary
		t.Errorf("replay lost rows: %d", len(lg.Replay()))
	}
}

// nilStore satisfies sessionStore with failures only.
type nilStore struct{}

func (nilStore) SessionLog(string) (*sessionlog.Log, error) {
	return nil, errNoStore
}

var errNoStore = &testStoreError{}

type testStoreError struct{}

func (testStoreError) Error() string { return "no store in this test" }
