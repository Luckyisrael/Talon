package tools

import (
	"context"
	"encoding/json"
)

// MarkTaskDone lets the agent check off a plan item — a screen or feature from the
// build plan — once it is genuinely built and compiling.
//
// Like ask_user, it is not executed as an ordinary tool: the orchestrator owns the
// plan graph, so it intercepts a mark_task_done call, matches the named item to a
// seeded task, and flips its status. This is a model-agnostic assist, not a gate —
// the build already marks screens done by a file-and-compile heuristic, so a model
// that never calls this still gets a correct checklist; a model that does call it
// can also check off features, which the heuristic cannot match honestly on its
// own. Execute is only the honest fallback if a call ever bypasses interception.
type MarkTaskDone struct{}

type MarkTaskDoneArgs struct {
	Task  string   `json:"task"`
	Tasks []string `json:"tasks,omitempty"`
	Note  string   `json:"note,omitempty"`
}

func (t *MarkTaskDone) Name() string { return "mark_task_done" }

func (t *MarkTaskDone) Description() string {
	return "Mark screens or features from the build plan as done, once they are actually implemented and the " +
		"project compiles. Pass one screen/feature as \"task\", or several at once as \"tasks\" — batching " +
		"finished items into a single call saves rounds. Name items as they appear in the plan checklist. Use this " +
		"to keep the plan honest as you finish items — never mark something done that you have not built and " +
		"verified. It records progress only; it does not build anything."
}

func (t *MarkTaskDone) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {"type": "string", "description": "One screen name or feature text to mark done, as shown in the build plan checklist"},
			"tasks": {"type": "array", "items": {"type": "string"}, "description": "Several screens/features to mark done in one call — prefer this when multiple items finished together"},
			"note": {"type": "string", "description": "Optional one-line note on what was completed"}
		}
	}`)
}

// Execute is the fallback for a call that bypassed the loop's interception. It
// never runs on the normal path; a benign note keeps a stray call from reading as
// an error to the model.
func (t *MarkTaskDone) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "Recorded against the build plan."}, nil
}
