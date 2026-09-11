package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// askUserRequest is what makes the loop pause: it recognises an ask_user call
// among a round's tool uses and pulls out the question to surface. These pin the
// three cases the loop turns on — a plain ask, one batched with other tools, and
// no ask at all.
func TestAskUserRequestFindsQuestionAndChoices(t *testing.T) {
	uses := []ai.ToolUseBlock{
		{ID: "ask1", Name: "ask_user", Input: json.RawMessage(
			`{"question":"Which auth flow?","choices":["MWA","email link"]}`)},
	}

	id, question, choices, ok := askUserRequest(uses)
	if !ok {
		t.Fatal("did not recognise the ask_user call")
	}
	if id != "ask1" {
		t.Errorf("id = %q, want ask1", id)
	}
	if question != "Which auth flow?" {
		t.Errorf("question = %q, want the model's question", question)
	}
	if len(choices) != 2 || choices[0] != "MWA" || choices[1] != "email link" {
		t.Errorf("choices = %v, want [MWA, email link]", choices)
	}
}

func TestAskUserRequestDetectsAmongSiblings(t *testing.T) {
	// The pause must be detected even when the model batches ask_user with other
	// calls — the loop intercepts before any of them run.
	uses := []ai.ToolUseBlock{
		{ID: "r1", Name: "read_file", Input: json.RawMessage(`{"path":"app.tsx"}`)},
		{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"question":"Proceed?"}`)},
	}
	id, question, _, ok := askUserRequest(uses)
	if !ok || id != "ask1" || question != "Proceed?" {
		t.Errorf("got (id=%q, q=%q, ok=%v), want the batched ask_user", id, question, ok)
	}
}

func TestAskUserRequestIgnoresOrdinaryCalls(t *testing.T) {
	uses := []ai.ToolUseBlock{
		{ID: "w1", Name: "write_file", Input: json.RawMessage(`{"path":"a.tsx"}`)},
	}
	if _, _, _, ok := askUserRequest(uses); ok {
		t.Error("reported an ask_user where only write_file was called")
	}
	if _, _, _, ok := askUserRequest(nil); ok {
		t.Error("reported an ask_user on an empty round")
	}
}

// Malformed input still pauses: the model explicitly asked to stop and wait, so a
// broken payload should not be silently executed as an ordinary call. The
// question just comes back empty.
func TestAskUserRequestPausesOnMalformedInput(t *testing.T) {
	uses := []ai.ToolUseBlock{
		{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"question":`)},
	}
	id, question, _, ok := askUserRequest(uses)
	if !ok {
		t.Fatal("malformed ask_user input should still pause, not execute")
	}
	if id != "ask1" {
		t.Errorf("id = %q, want ask1", id)
	}
	if question != "" {
		t.Errorf("question = %q, want empty on malformed input", question)
	}
}
