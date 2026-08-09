package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

type ToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) List() []ToolDef {
	defs := make([]ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	t, ok := r.tools[name]
	if !ok {
		return ToolResult{
			Content: fmt.Sprintf("unknown tool: %s", name),
			IsError: true,
		}, fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, args)
}

// StoreAdapter wraps the actual store to provide the interface needed by tools.
type StoreAdapter struct {
	CreateADRFn  func(projectID int64, decision, context, options, outcome string) error
	AddMemoryFn  func(projectID int64, entryType, content, metadata string) error
}

func (a *StoreAdapter) CreateADR(projectID int64, decision, context, options, outcome string) error {
	return a.CreateADRFn(projectID, decision, context, options, outcome)
}

func (a *StoreAdapter) AddMemory(projectID int64, entryType, content, metadata string) error {
	return a.AddMemoryFn(projectID, entryType, content, metadata)
}