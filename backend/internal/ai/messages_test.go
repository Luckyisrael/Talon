package ai

import (
	"encoding/json"
	"testing"
)

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

// Gemini's OpenAI-compatible endpoint issues a thought_signature on every
// assistant message that calls tools and rejects the replay of that message if
// the signature is missing. This is the exact failure that appeared as
// "Function call is missing a thought_signature in functionCall parts" on the
// second tool call of a build. The signature must survive the parse → Block →
// toOpenAI round trip.
func TestOpenAIReplayCarriesThoughtSignature(t *testing.T) {
	history := []Message{
		msg("user", text("build me a login screen")),
		msg("assistant",
			Block{Type: "tool_use", ID: "call_1", Name: "list_files", Input: json.RawMessage(`{}`),
				ThoughtSignature: "sN7fJqkX2"},
			text("checking the project")),
		msg("user", toolResult("call_1")),
	}

	out := toOpenAI("", history)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages (user, assistant, tool), got %d", len(out))
	}

	var assistantMsg *openaiChatMessage
	for i := range out {
		if out[i].Role == "assistant" && len(out[i].ToolCalls) > 0 {
			assistantMsg = &out[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message with tool_calls in the replay")
	}

	google, ok := assistantMsg.ToolCalls[0].ExtraContent["google"].(map[string]any)
	if !ok {
		t.Fatalf("extra_content.google missing from first tool call replay: %#v", assistantMsg.ToolCalls[0])
	}
	if got := google["thought_signature"]; got != "sN7fJqkX2" {
		t.Errorf("thought_signature = %v, want sN7fJqkX2", got)
	}

	// A tool call with no signature must not emit extra_content at all.
	noSig := toOpenAI("", []Message{
		msg("user", text("hi")),
		msg("assistant", toolUse("call_2")),
		msg("user", toolResult("call_2")),
	})
	var replay *openaiChatMessage
	for i := range noSig {
		if noSig[i].Role == "assistant" && len(noSig[i].ToolCalls) > 0 {
			replay = &noSig[i]
			break
		}
	}
	if replay == nil {
		t.Fatal("no assistant message in the no-signature replay")
	}
	if replay.ToolCalls[0].ExtraContent != nil {
		t.Errorf("extra_content emitted without a signature: %#v", replay.ToolCalls[0].ExtraContent)
	}
}

// Gemini only signs the FIRST part of a parallel batch of tool calls; later
// parts come back bare. The replay must keep the signature positional — signed
// first part, unsigned second part — or the next request dies with
// "missing a thought_signature ... position 2".
func TestOpenAIReplayKeepsParallelSignaturePositional(t *testing.T) {
	history := []Message{
		msg("user", text("check the weather in both cities")),
		msg("assistant",
			Block{Type: "tool_use", ID: "call_1", Name: "list_files", Input: json.RawMessage(`{}`),
				ThoughtSignature: "SIG_A"},
			Block{Type: "tool_use", ID: "call_2", Name: "list_files", Input: json.RawMessage(`{}`),
				ThoughtSignature: ""},
		),
		msg("user", toolResult("call_1"), toolResult("call_2")),
	}

	out := toOpenAI("", history)

	var assistantMsg *openaiChatMessage
	for i := range out {
		if out[i].Role == "assistant" && len(out[i].ToolCalls) > 0 {
			assistantMsg = &out[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message with tool_calls in the replay")
	}
	if len(assistantMsg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool_calls, got %d", len(assistantMsg.ToolCalls))
	}

	firstGoogle, ok := assistantMsg.ToolCalls[0].ExtraContent["google"].(map[string]any)
	if !ok || firstGoogle["thought_signature"] != "SIG_A" {
		t.Errorf("first call signature not preserved: %#v", assistantMsg.ToolCalls[0].ExtraContent)
	}
	if assistantMsg.ToolCalls[1].ExtraContent != nil {
		t.Errorf("second call must stay bare, got extra_content: %#v", assistantMsg.ToolCalls[1].ExtraContent)
	}
}
