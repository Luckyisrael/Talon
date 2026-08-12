package tools

import (
	"context"
	"encoding/json"
	"strings"
)

// WriteADR records an architecture decision so later turns build within it
// instead of quietly introducing a second pattern.
type WriteADR struct {
	Store     *StoreAdapter
	ProjectID int64
}

type WriteADRArgs struct {
	Decision string `json:"decision"`
	Context  string `json:"context"`
	Options  string `json:"options,omitempty"`
	Outcome  string `json:"outcome"`
}

func NewWriteADR(store *StoreAdapter, projectID int64) *WriteADR {
	return &WriteADR{Store: store, ProjectID: projectID}
}

func (t *WriteADR) Name() string { return "write_adr" }

func (t *WriteADR) Description() string {
	return "Record an architecture decision that later work must follow — a state management approach, " +
		"a data-fetching pattern, a navigation structure. Use it when you pick one of several defensible " +
		"options and the choice needs to hold across the whole app. Do not use it for routine " +
		"implementation details."
}

func (t *WriteADR) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"decision": {"type": "string", "description": "What was decided, in one line"},
			"context":  {"type": "string", "description": "What forced the decision"},
			"options":  {"type": "string", "description": "What else you considered and why you passed on it"},
			"outcome":  {"type": "string", "description": "What this means for future code"}
		},
		"required": ["decision", "context", "outcome"]
	}`)
}

func (t *WriteADR) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WriteADRArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(a.Decision) == "" {
		return errResult("decision is required"), nil
	}
	if t.Store == nil {
		return errResult("architecture decisions are not available in this session"), nil
	}

	if err := t.Store.CreateADR(t.ProjectID, a.Decision, a.Context, a.Options, a.Outcome); err != nil {
		return errResult("could not record that decision: %v", err), nil
	}

	return ToolResult{Content: "Recorded: " + truncate(a.Decision, 120)}, nil
}
