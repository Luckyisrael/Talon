package ai

import "testing"

func msg(role string, blocks ...Block) Message { return Message{Role: role, Blocks: blocks} }

func text(s string) Block        { return Block{Type: "text", Text: s} }
func toolUse(id string) Block    { return Block{Type: "tool_use", ID: id, Name: "read_file"} }
func toolResult(id string) Block { return Block{Type: "tool_result", ToolUseID: id, Content: "ok"} }

// A stored conversation is replayed to the provider verbatim, and both Anthropic
// and OpenAI reject a tool_use with no matching tool_result. A turn killed
// mid-flight — cancelled request, crash, timeout — leaves exactly that, so every
// later prompt in that project would fail with a 400 until the history was
// cleared by hand.
func TestTrimToValidHistoryDropsUnansweredToolUse(t *testing.T) {
	history := []Message{
		msg("user", text("build me a login screen")),
		msg("assistant", text("on it"), toolUse("t1")),
		msg("user", toolResult("t1")),
		msg("assistant", text("done")),
		msg("user", text("now add a logout button")),
		msg("assistant", text("sure"), toolUse("t2")), // the process died here
	}

	got := TrimToValidHistory(history)
	if len(got) != 4 {
		t.Fatalf("kept %d turns, want 4 (through the completed exchange)", len(got))
	}
	if last := got[len(got)-1]; last.Blocks[0].Text != "done" {
		t.Errorf("history ends at %+v, want the assistant's \"done\"", last)
	}
}

func TestTrimToValidHistoryDropsLeadingOrphanResult(t *testing.T) {
	// Trimming the front of a long conversation can cut between a tool_use and its
	// result, leaving a tool_result the provider has no request for. The assistant
	// turn after it goes too: a replayed history has to open on a user turn.
	history := []Message{
		msg("user", toolResult("t9")),
		msg("assistant", text("finished that")),
		msg("user", text("thanks, next thing")),
		msg("assistant", text("here you go")),
	}

	got := TrimToValidHistory(history)
	if len(got) != 2 {
		t.Fatalf("kept %d turns, want 2", len(got))
	}
	if got[0].Role != "user" || got[0].Blocks[0].Type != "text" {
		t.Errorf("history starts at %+v, want a plain user turn", got[0])
	}
}

func TestTrimToValidHistoryKeepsCompleteHistory(t *testing.T) {
	history := []Message{
		msg("user", text("hello")),
		msg("assistant", text("hi"), toolUse("t1")),
		msg("user", toolResult("t1")),
		msg("assistant", text("all set")),
	}

	if got := TrimToValidHistory(history); len(got) != len(history) {
		t.Errorf("kept %d turns, want all %d", len(got), len(history))
	}
}

func TestTrimToValidHistoryHandlesNothingSalvageable(t *testing.T) {
	history := []Message{
		msg("assistant", toolUse("t1")),
	}
	if got := TrimToValidHistory(history); len(got) != 0 {
		t.Errorf("kept %d turns, want none", len(got))
	}
	if got := TrimToValidHistory(nil); got != nil {
		t.Errorf("TrimToValidHistory(nil) = %v, want nil", got)
	}
}

func TestTrimToValidHistoryMatchesParallelToolCalls(t *testing.T) {
	// The agent issues several tool calls in one assistant turn; all of their
	// results arrive in the following user turn.
	history := []Message{
		msg("user", text("read both files")),
		msg("assistant", toolUse("a"), toolUse("b")),
		msg("user", toolResult("a"), toolResult("b")),
		msg("assistant", text("read them")),
	}
	if got := TrimToValidHistory(history); len(got) != 4 {
		t.Errorf("kept %d turns, want all 4", len(got))
	}

	// One result missing invalidates the whole turn, and with it the user turn
	// that opened it — nothing here can be replayed.
	partial := []Message{
		msg("user", text("read both files")),
		msg("assistant", toolUse("a"), toolUse("b")),
		msg("user", toolResult("a")),
	}
	if got := TrimToValidHistory(partial); len(got) != 0 {
		t.Errorf("kept %d turns, want none", len(got))
	}
}

func TestTrimToValidHistoryEndsOnAssistantTurn(t *testing.T) {
	// A prompt whose build died leaves a user turn with no reply. Replaying it and
	// then appending the next prompt would send two user turns in a row.
	history := []Message{
		msg("user", text("first")),
		msg("assistant", text("done")),
		msg("user", text("second, never answered")),
	}
	got := TrimToValidHistory(history)
	if len(got) != 2 {
		t.Fatalf("kept %d turns, want 2", len(got))
	}
	if got[len(got)-1].Role != "assistant" {
		t.Errorf("history ends on a %s turn, want assistant", got[len(got)-1].Role)
	}
}
