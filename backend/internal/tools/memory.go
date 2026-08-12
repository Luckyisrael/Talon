package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// WriteMemory records a durable fact about the project. The ProjectID is bound
// when the tool is constructed rather than read out of the context: a tool built
// per request already knows which project it belongs to, and pulling it from an
// untyped context value was a panic waiting for the one call that lacked it.
type WriteMemory struct {
	Store     *StoreAdapter
	ProjectID int64
}

type WriteMemoryArgs struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	Metadata string `json:"metadata,omitempty"`
}

func NewWriteMemory(store *StoreAdapter, projectID int64) *WriteMemory {
	return &WriteMemory{Store: store, ProjectID: projectID}
}

func (t *WriteMemory) Name() string { return "write_memory" }

func (t *WriteMemory) Description() string {
	return "Record something about this project worth remembering in later turns: a convention you " +
		"established, a decision you made, a constraint you discovered, or a warning about something " +
		"fragile. Memory is included in the context of every future turn, so keep entries short and " +
		"specific — one fact each."
}

func (t *WriteMemory) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"type": {
				"type": "string",
				"enum": ["convention", "decision", "architecture", "fact", "warning"],
				"description": "What kind of entry this is"
			},
			"content": {
				"type": "string",
				"description": "The fact itself, in one or two sentences"
			},
			"metadata": {
				"type": "string",
				"description": "Optional: the file or area it applies to"
			}
		},
		"required": ["type", "content"]
	}`)
}

func (t *WriteMemory) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WriteMemoryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(a.Content) == "" {
		return errResult("content is required"), nil
	}
	if t.Store == nil {
		return errResult("project memory is not available in this session"), nil
	}
	if a.Type == "" {
		a.Type = "fact"
	}

	if err := t.Store.AddMemory(t.ProjectID, a.Type, a.Content, a.Metadata); err != nil {
		return errResult("could not save that: %v", err), nil
	}

	return ToolResult{Content: fmt.Sprintf("Remembered (%s): %s", a.Type, truncate(a.Content, 80))}, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
