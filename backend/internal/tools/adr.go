package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// WriteADR records an Architecture Decision Record.
type WriteADR struct {
	Store *StoreAdapter
}

type WriteADRArgs struct {
	Decision string `json:"decision"`
	Context  string `json:"context"`
	Options  string `json:"options,omitempty"`
	Outcome  string `json:"outcome"`
}

func NewWriteADR(store *StoreAdapter) *WriteADR {
	return &WriteADR{Store: store}
}

func (t *WriteADR) Name() string { return "write_adr" }

func (t *WriteADR) Description() string {
	return "Record an Architecture Decision Record (ADR) for the project. Use this when making a significant architectural decision that future generations must follow."
}

func (t *WriteADR) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"decision": {"type": "string", "description": "The architectural decision made"},
			"context": {"type": "string", "description": "Why this decision was made"},
			"options": {"type": "string", "description": "Alternative options considered"},
			"outcome": {"type": "string", "description": "The final decision/rationale"}
		},
		"required": ["decision", "context", "outcome"]
	}`)
}

func (t *WriteADR) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WriteADRArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	projectID := ctx.Value("project_id").(int64)
	if err := t.Store.CreateADR(projectID, a.Decision, a.Context, a.Options, a.Outcome); err != nil {
		return ToolResult{Content: fmt.Sprintf("failed to record ADR: %v", err), IsError: true}, err
	}

	return ToolResult{Content: fmt.Sprintf("ADR recorded: %s", a.Decision)}, nil
}
