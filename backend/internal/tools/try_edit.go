package tools

import (
	"context"
	"encoding/json"
)

// TryEdit applies one file change the way edit_file / write_file / multi_edit
// would, then typechecks: a change that breaks types is auto-reverted to its
// pre-image with `Reverted: <reason>` on the tool_result, so the model sees
// the rollback instead of discovering it three rounds later. A change that
// passes stays applied and staged — call keep_edit to accept and clear the
// pre-image, or leave it staged as undo history.
//
// Like mark_task_done, this is orchestrator-owned: the loop intercepts the
// call because only the loop owns both the session's patch buffer and the
// verify path. Execute is only the honest fallback for a bypassed call.
// Behind the TALONSTUDIO_PATCH_BUFFER flag the interception is off and a
// try_edit call behaves as the plain underlying edit.
type TryEdit struct{}

func (t *TryEdit) Name() string { return "try_edit" }

func (t *TryEdit) Description() string {
	return "Apply one file change with automatic rollback: the edit is applied, then typechecked. " +
		"If types break, the file is reverted to what it was and the result says Reverted with the reason — " +
		"fix the error and try again instead of building on a broken file. If types pass, the edit stays " +
		"and its pre-image is staged as undo history (call keep_edit to accept and clear it). " +
		"Pass mode edit (old/new), write (whole-file content), or multi (list of old/new pairs), plus path."
}

func (t *TryEdit) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path relative to the project root"},
			"mode": {"type": "string", "enum": ["edit", "write", "multi"], "description": "edit: replace old with new; write: replace whole file with content; multi: apply each old/new pair in order"},
			"old": {"type": "string", "description": "edit/multi: the exact text to replace (must match once)"},
			"new": {"type": "string", "description": "edit/multi: the replacement text"},
			"content": {"type": "string", "description": "write: the whole new file content"},
			"edits": {"type": "array", "items": {"type": "object", "properties": {"old": {"type": "string"}, "new": {"type": "string"}}}, "description": "multi: ordered old/new pairs"}
		},
		"required": ["path", "mode"]
	}`)
}

func (t *TryEdit) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "Try-edit needs the loop's patch buffer, which is off in this session — apply the change with edit_file / write_file / multi_edit instead."}, nil
}

// KeepEdit accepts a staged try_edit: the pre-image is dropped and the edit
// stands as an ordinary change. Accepting is explicit — a passing try_edit
// stays staged (revertible) until this call clears it. Accepting a path with
// nothing staged is a benign no-op.
type KeepEdit struct{}

func (t *KeepEdit) Name() string { return "keep_edit" }

func (t *KeepEdit) Description() string {
	return "Accept a try_edit change: drop its staged pre-image so the edit stands as an ordinary change. " +
		"Pass the same path the try_edit used. Accepting a path with nothing staged does nothing."
}

func (t *KeepEdit) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "File path whose staged pre-image to drop"}
		},
		"required": ["path"]
	}`)
}

func (t *KeepEdit) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "Nothing staged in this session — the patch buffer is off, so there is nothing to accept."}, nil
}
