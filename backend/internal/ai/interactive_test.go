package ai

import (
	"encoding/json"
	"testing"
)

// namedUse builds a tool_use block for an arbitrary tool — the messages_test
// helper hardcodes read_file, and these tests turn on the tool name.
func namedUse(id, name string) Block {
	return Block{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(`{}`)}
}

func askUse(id, question string) Block {
	input, _ := json.Marshal(map[string]string{"question": question})
	return Block{Type: "tool_use", ID: id, Name: "ask_user", Input: input}
}

func TestPendingQuestionExtractsText(t *testing.T) {
	history := []Message{
		msg("user", text("build it")),
		msg("assistant", text("checking"), askUse("ask1", "May I run awk on the types?")),
	}
	q, ok := PendingQuestion(history)
	if !ok || q != "May I run awk on the types?" {
		t.Errorf("got (%q, %v)", q, ok)
	}
	if _, ok := PendingQuestion([]Message{msg("user", text("hi"))}); ok {
		t.Error("user-ending history must not report a pending question")
	}
	if _, ok := PendingQuestion(nil); ok {
		t.Error("empty history must not report a pending question")
	}
}

// A turn paused on ask_user ends on an assistant turn whose ask_user tool_use has
// no result yet. PendingToolUse is what resume uses to find that call so it can
// answer it instead of dropping it as an orphan.
func TestPendingToolUseFindsTrailingAskUser(t *testing.T) {
	history := []Message{
		msg("user", text("build me a wallet screen")),
		msg("assistant", text("one question first"), namedUse("ask1", "ask_user")),
	}

	askID, allIDs, ok := PendingToolUse(history, "ask_user")
	if !ok {
		t.Fatal("did not find the pending ask_user")
	}
	if askID != "ask1" {
		t.Errorf("askID = %q, want ask1", askID)
	}
	if len(allIDs) != 1 || allIDs[0] != "ask1" {
		t.Errorf("allIDs = %v, want [ask1]", allIDs)
	}
}

// ask_user is meant to be called alone, but a model can batch it with other
// calls. Resume must satisfy every tool_use in that turn or the provider 400s, so
// PendingToolUse returns the ask_user id plus the ids of its siblings.
func TestPendingToolUseReturnsSiblings(t *testing.T) {
	history := []Message{
		msg("user", text("scaffold it")),
		msg("assistant",
			namedUse("r1", "read_file"),
			namedUse("ask1", "ask_user"),
			namedUse("r2", "list_files"),
		),
	}

	askID, allIDs, ok := PendingToolUse(history, "ask_user")
	if !ok {
		t.Fatal("did not find the pending ask_user among siblings")
	}
	if askID != "ask1" {
		t.Errorf("askID = %q, want ask1", askID)
	}
	if len(allIDs) != 3 {
		t.Fatalf("allIDs = %v, want all three tool_use ids", allIDs)
	}
	// Every id in the turn must be present so AnswerTurn can close them all.
	want := map[string]bool{"r1": true, "ask1": true, "r2": true}
	for _, id := range allIDs {
		if !want[id] {
			t.Errorf("unexpected id %q in allIDs %v", id, allIDs)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Errorf("missing ids from allIDs: %v", want)
	}
}

func TestPendingToolUseIgnoresAnsweredHistory(t *testing.T) {
	// The trailing turn is a user turn: the previous tool call was already
	// answered, so there is nothing pending.
	history := []Message{
		msg("user", text("build it")),
		msg("assistant", namedUse("ask1", "ask_user")),
		msg("user", Block{Type: "tool_result", ToolUseID: "ask1", Content: "yes"}),
	}
	if _, _, ok := PendingToolUse(history, "ask_user"); ok {
		t.Error("reported a pending call on an already-answered history")
	}
}

func TestPendingToolUseIgnoresOtherTools(t *testing.T) {
	// A trailing assistant turn that calls tools, but none of them ask_user, is a
	// normal working turn — not a pause.
	history := []Message{
		msg("user", text("build it")),
		msg("assistant", namedUse("w1", "write_file")),
	}
	if _, _, ok := PendingToolUse(history, "ask_user"); ok {
		t.Error("reported a pending ask_user where only write_file was called")
	}
}

func TestPendingToolUseHandlesEmptyAndTextOnly(t *testing.T) {
	if _, _, ok := PendingToolUse(nil, "ask_user"); ok {
		t.Error("reported a pending call on empty history")
	}
	textOnly := []Message{
		msg("user", text("hi")),
		msg("assistant", text("all done, nothing to ask")),
	}
	if _, _, ok := PendingToolUse(textOnly, "ask_user"); ok {
		t.Error("reported a pending call on a text-only assistant turn")
	}
}

// The answer resolves the ask_user call as its tool_result. A lone ask_user gets
// exactly one tool_result carrying the user's words verbatim.
func TestAnswerTurnResolvesLoneAskUser(t *testing.T) {
	turn := AnswerTurn("ask1", []string{"ask1"}, "use a dark theme")

	if turn.Role != "user" {
		t.Errorf("answer turn role = %q, want user", turn.Role)
	}
	if len(turn.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(turn.Blocks))
	}
	b := turn.Blocks[0]
	if b.Type != "tool_result" || b.ToolUseID != "ask1" {
		t.Errorf("block = %+v, want a tool_result for ask1", b)
	}
	if b.Content != "use a dark theme" {
		t.Errorf("answer content = %q, want the user's words verbatim", b.Content)
	}
	if b.IsError {
		t.Error("an answer is not an error result")
	}
}

// When ask_user was batched with other calls, resume must answer ask_user and
// still satisfy the siblings — each gets a neutral "not run" result so the
// assistant turn is fully closed and the replay does not 400.
func TestAnswerTurnSatisfiesSiblings(t *testing.T) {
	turn := AnswerTurn("ask1", []string{"r1", "ask1", "r2"}, "yes, add analytics")

	if len(turn.Blocks) != 3 {
		t.Fatalf("got %d blocks, want one per pending tool_use", len(turn.Blocks))
	}

	byID := map[string]Block{}
	for _, b := range turn.Blocks {
		if b.Type != "tool_result" {
			t.Errorf("block %+v is not a tool_result", b)
		}
		byID[b.ToolUseID] = b
	}

	if got := byID["ask1"].Content; got != "yes, add analytics" {
		t.Errorf("ask_user answer = %q, want the user's words", got)
	}
	// The siblings must carry a note (not the answer, not empty) so the model can
	// reissue them if it still needs them.
	for _, id := range []string{"r1", "r2"} {
		b, ok := byID[id]
		if !ok {
			t.Errorf("no tool_result for sibling %q — the turn is unsatisfied", id)
			continue
		}
		if b.Content == "" || b.Content == "yes, add analytics" {
			t.Errorf("sibling %q content = %q, want a distinct not-run note", id, b.Content)
		}
	}
}

// AnsweredHistory is the whole reason resume works: it must keep the trailing
// answer turn. TrimToValidHistory would back-trim it away (a tool_result user
// turn is not a valid ending), which is exactly the bug this helper exists to
// avoid — so this test guards against a regression to that trim.
func TestAnsweredHistoryKeepsTheAnswerTail(t *testing.T) {
	stored := []Message{
		msg("user", text("build me a wallet screen")),
		msg("assistant", text("one question first"), namedUse("ask1", "ask_user")),
	}
	answer := AnswerTurn("ask1", []string{"ask1"}, "use MWA")

	full := AnsweredHistory(stored, answer)

	if len(full) != 3 {
		t.Fatalf("got %d turns, want the prompt, the ask, and the answer", len(full))
	}
	last := full[len(full)-1]
	if last.Role != "user" || len(last.Blocks) != 1 || last.Blocks[0].Type != "tool_result" {
		t.Fatalf("last turn = %+v, want the user tool_result answer preserved", last)
	}
	if last.Blocks[0].ToolUseID != "ask1" || last.Blocks[0].Content != "use MWA" {
		t.Errorf("answer block = %+v, want the ask1 answer verbatim", last.Blocks[0])
	}
	// The ask_user turn it answers must still be there, or the tool_result is an
	// orphan and the replay 400s.
	if _, _, ok := PendingToolUse(stored, "ask_user"); !ok {
		t.Fatal("test setup: stored history should end on a pending ask_user")
	}
	if full[1].Role != "assistant" {
		t.Errorf("turn before the answer = %q, want the assistant ask_user turn", full[1].Role)
	}
}

// A history truncated upstream can open mid-exchange on an orphan tool_result,
// which both providers reject as a first turn. AnsweredHistory front-trims to
// the first clean user boundary — while still preserving the answered tail.
func TestAnsweredHistoryFrontTrimsAnOrphanOpening(t *testing.T) {
	stored := []Message{
		// Truncation left a dangling tool_result with no preceding tool_use.
		msg("user", Block{Type: "tool_result", ToolUseID: "gone", Content: "stale"}),
		msg("assistant", text("working")),
		msg("user", text("actually, add a settings tab")),
		msg("assistant", namedUse("ask1", "ask_user")),
	}
	answer := AnswerTurn("ask1", []string{"ask1"}, "yes")

	full := AnsweredHistory(stored, answer)

	if len(full) == 0 {
		t.Fatal("front-trim removed everything")
	}
	if !isCleanBoundary(full[0]) {
		t.Errorf("history opens on %+v, want a clean user boundary", full[0])
	}
	// The answer tail survives the front-trim.
	last := full[len(full)-1]
	if last.Role != "user" || last.Blocks[0].ToolUseID != "ask1" {
		t.Errorf("last turn = %+v, want the ask1 answer still at the tail", last)
	}
}
