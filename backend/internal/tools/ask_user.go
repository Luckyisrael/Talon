package tools

import (
	"context"
	"encoding/json"
)

// AskUser lets the agent pause the build to ask the user a clarifying question
// or offer a choice, rather than guessing and building the wrong thing.
//
// It is not executed like an ordinary tool: the orchestrator intercepts an
// ask_user call before any tool runs, surfaces the question, and ends the turn
// paused. The user's answer resumes the turn as this call's tool_result. Execute
// exists only to satisfy the Tool interface and to be the honest fallback if a
// call ever reaches it — it never runs on the paused path.
type AskUser struct{}

type AskUserArgs struct {
	Question    string   `json:"question"`
	Choices     []string `json:"choices,omitempty"`
	MultiSelect bool     `json:"multi_select,omitempty"`
}

func (t *AskUser) Name() string { return "ask_user" }

func (t *AskUser) Description() string {
	return "Pause and ask the user a single clarifying question when a real decision would otherwise be " +
		"a guess — an ambiguous requirement, a choice between defensible approaches, missing information only " +
		"they have. The build stops until they answer, so call this alone (not alongside other tools) and only " +
		"when the answer changes what you build. Do not use it for routine implementation details you can " +
		"decide yourself, and never to ask for secrets, keys, or seed phrases. Offer choices when the options " +
		"are known so the user can pick rather than type. Set multi_select=true when the question genuinely " +
		"admits multiple answers at once (features to enable, screens to include, packages to add) so the UI " +
		"renders checkboxes instead of a single-choice picker; leave it false for pick-one questions."
}

func (t *AskUser) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"question":     {"type": "string", "description": "The question to ask, in one or two sentences"},
			"choices":      {"type": "array", "items": {"type": "string"}, "description": "Optional list of concrete options for the user to pick from"},
			"multi_select": {"type": "boolean", "description": "True when the question admits more than one choice at once (renders checkboxes); false or omitted for pick-one questions (renders radio-style)"}
		},
		"required": ["question"]
	}`)
}

// Execute is the fallback for a call that somehow bypassed the loop's pause
// interception. It never runs on the normal path; returning a benign note keeps
// a stray call from looking like an error to the model.
func (t *AskUser) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "Your question has been surfaced to the user; await their answer."}, nil
}
