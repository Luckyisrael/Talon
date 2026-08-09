package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// WriteMemory writes an entry to project memory.
type WriteMemory struct {
	Store *StoreAdapter
}

type WriteMemoryArgs struct {
	Type     string `json:"type"`
	Content  string `json:"content"`
	Metadata string `json:"metadata,omitempty"`
}

func NewWriteMemory(store *StoreAdapter) *WriteMemory {
	return &WriteMemory{Store: store}
}

func (t *WriteMemory) Name() string { return "write_memory" }

func (t *WriteMemory) Description() string {
	return "Write an entry to project memory. Use this after meaningful changes to persist architectural decisions, conventions, or key facts about what was built."
}

func (t *WriteMemory) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"type": {"type": "string", "enum": ["convention", "decision", "architecture", "fact", "warning"], "description": "Type of memory entry"},
			"content": {"type": "string", "description": "The memory content to persist"},
			"metadata": {"type": "string", "description": "Optional metadata (e.g., file path, date)"}
		},
		"required": ["type", "content"]
	}`)
}

func (t *WriteMemory) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WriteMemoryArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	projectID := ctx.Value("project_id").(int64)
	if err := t.Store.AddMemory(projectID, a.Type, a.Content, a.Metadata); err != nil {
		return ToolResult{Content: fmt.Sprintf("failed to write memory: %v", err), IsError: true}, err
	}

	return ToolResult{Content: fmt.Sprintf("Memory written (%s): %s", a.Type, truncate(a.Content, 50))}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
