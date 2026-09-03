package ai

import (
	"encoding/json"
	"strings"
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

// The system prompt is the largest block that repeats verbatim on every round of
// a build, so the OpenAI/OpenRouter path must send it with a cache_control marker
// — without one, each round re-bills the whole system prompt at the uncached
// rate. This pins the exact wire shape OpenRouter reads to place the breakpoint:
// a content-parts array whose text part carries cache_control:{type:"ephemeral"}.
// It marshals the message the way the request actually goes out and reads the
// shape back, so it fails if the content silently reverts to a bare string.
func TestOpenAISystemPromptIsCacheable(t *testing.T) {
	out := toOpenAI("you are a build agent", []Message{
		msg("user", text("build me a login screen")),
	})
	if len(out) == 0 || out[0].Role != "system" {
		t.Fatalf("expected a leading system message, got %#v", out)
	}

	raw, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal system message: %v", err)
	}
	var decoded struct {
		Role    string `json:"role"`
		Content []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			CacheControl struct {
				Type string `json:"type"`
			} `json:"cache_control"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("system content is not the content-parts array OpenRouter reads for caching: %v\n%s", err, raw)
	}
	if len(decoded.Content) != 1 {
		t.Fatalf("want exactly one text part, got %d: %s", len(decoded.Content), raw)
	}
	part := decoded.Content[0]
	if part.Type != "text" || part.Text != "you are a build agent" {
		t.Errorf("system text part = %+v, want the system prompt carried as a text part", part)
	}
	if part.CacheControl.Type != "ephemeral" {
		t.Errorf("system part cache_control.type = %q, want \"ephemeral\" — no marker means no prompt caching on OpenRouter", part.CacheControl.Type)
	}
}

// An empty system prompt must emit no system message at all — not an empty
// cacheable part — so a toolless or system-less call stays byte-for-byte what it
// was before caching was wired in.
func TestOpenAIEmptySystemEmitsNoSystemMessage(t *testing.T) {
	out := toOpenAI("", []Message{msg("user", text("hi"))})
	for _, m := range out {
		if m.Role == "system" {
			t.Errorf("emitted a system message for an empty system prompt: %#v", m)
		}
	}
}

func findResult(t *testing.T, msgs []Message, id string) Block {
	t.Helper()
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" && b.ToolUseID == id {
				return b
			}
		}
	}
	t.Fatalf("no tool_result for %q", id)
	return Block{}
}

func readCall(id, path string) Block {
	return Block{Type: "tool_use", ID: id, Name: "read_file", Input: json.RawMessage(`{"path":"` + path + `"}`)}
}

// When the same file is read twice in a turn, the older result is dead weight —
// the newer read has the current bytes — yet it is replayed in full every round.
// Trimming collapses the stale copy to a pointer while keeping the newest read
// whole, and must never orphan a tool_use (both providers 400 on that) or touch
// the caller's stored transcript.
func TestTrimSupersededCollapsesEarlierReadOfSamePath(t *testing.T) {
	big := strings.Repeat("A", minTrimmableResult+50)
	newer := strings.Repeat("B", minTrimmableResult+50)
	history := []Message{
		msg("user", text("open the config")),
		msg("assistant", readCall("r1", "app/config.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r1", Content: big}),
		msg("assistant", text("editing"), readCall("r2", "app/config.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r2", Content: newer}),
	}

	out := TrimSupersededToolOutput(history)

	if first := findResult(t, out, "r1"); first.Content == big {
		t.Fatal("the superseded read_file result was not collapsed")
	} else if !strings.Contains(first.Content, "app/config.ts") {
		t.Errorf("the pointer should name the re-read file, got: %q", first.Content)
	}
	if last := findResult(t, out, "r2"); last.Content != newer {
		t.Error("the newest read_file result must keep its full content")
	}
	if hasUnansweredToolUse(out) {
		t.Error("trimming orphaned a tool_use — the provider would 400 on this replay")
	}
	// Full fidelity: the caller's history is what gets persisted and shown, so it
	// must be untouched.
	if got := findResult(t, history, "r1").Content; got != big {
		t.Errorf("TrimSupersededToolOutput mutated the caller's history (r1 content changed, len now %d)", len(got))
	}
}

// A small stale result is left alone: collapsing it saves almost nothing and, on
// a caching provider, a change deep in the prefix can cost a re-cache of
// everything after it.
func TestTrimSupersededLeavesSmallResults(t *testing.T) {
	small := "line one\nline two\n"
	history := []Message{
		msg("user", text("peek")),
		msg("assistant", readCall("r1", "a.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r1", Content: small}),
		msg("assistant", readCall("r2", "a.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r2", Content: small}),
	}
	out := TrimSupersededToolOutput(history)
	if got := findResult(t, out, "r1").Content; got != small {
		t.Errorf("a small superseded result was collapsed; content = %q", got)
	}
}

// An error result carries information the newer read does not, and is small, so
// it is never collapsed even when the same path is read again afterwards.
func TestTrimSupersededKeepsErrorResults(t *testing.T) {
	errBody := strings.Repeat("permission denied ", 60) // large, but an error
	newer := strings.Repeat("B", minTrimmableResult+50)
	history := []Message{
		msg("user", text("open it")),
		msg("assistant", readCall("r1", "secret.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r1", Content: errBody, IsError: true}),
		msg("assistant", readCall("r2", "secret.ts")),
		msg("user", Block{Type: "tool_result", ToolUseID: "r2", Content: newer}),
	}
	out := TrimSupersededToolOutput(history)
	if got := findResult(t, out, "r1").Content; got != errBody {
		t.Errorf("an error result was collapsed; content = %q", got)
	}
}

// Reads of different files never supersede one another, and a file read only once
// is always kept whole.
func TestTrimSupersededKeepsDistinctAndSingleReads(t *testing.T) {
	a := strings.Repeat("A", minTrimmableResult+50)
	b := strings.Repeat("B", minTrimmableResult+50)
	history := []Message{
		msg("user", text("read both")),
		msg("assistant", readCall("r1", "a.ts"), readCall("r2", "b.ts")),
		msg("user",
			Block{Type: "tool_result", ToolUseID: "r1", Content: a},
			Block{Type: "tool_result", ToolUseID: "r2", Content: b}),
	}
	out := TrimSupersededToolOutput(history)
	if findResult(t, out, "r1").Content != a || findResult(t, out, "r2").Content != b {
		t.Error("distinct single reads must both keep their full content")
	}
}

// A large result from a tool that is not read-only (a write, a build) is never
// collapsed — those results are not interchangeable, so "latest wins" does not
// hold for them.
func TestTrimSupersededOnlyTouchesReadOnlyTools(t *testing.T) {
	body := strings.Repeat("diff ", 400)
	history := []Message{
		msg("user", text("apply it twice")),
		msg("assistant", Block{Type: "tool_use", ID: "w1", Name: "write_file", Input: json.RawMessage(`{"path":"a.ts"}`)}),
		msg("user", Block{Type: "tool_result", ToolUseID: "w1", Content: body}),
		msg("assistant", Block{Type: "tool_use", ID: "w2", Name: "write_file", Input: json.RawMessage(`{"path":"a.ts"}`)}),
		msg("user", Block{Type: "tool_result", ToolUseID: "w2", Content: body}),
	}
	out := TrimSupersededToolOutput(history)
	if findResult(t, out, "w1").Content != body {
		t.Error("a write_file result was collapsed; only read_file/list_files are supersedable")
	}
}
