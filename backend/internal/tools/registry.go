package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
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

// ReadOnly marks tools with no side effects. The orchestrator runs these
// concurrently within a single turn; everything else is serialised.
type ReadOnly interface {
	ReadOnly() bool
}

// A Registry is created per request. Sharing one across requests would let one
// tenant's workspace paths leak into another's tool calls.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := tool.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns tool definitions in a stable, name-sorted order. Order matters:
// tool definitions sit in the cached prefix of every request, and iterating a
// Go map would reshuffle them on each call and invalidate the prompt cache.
func (r *Registry) List() []ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]ToolDef, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// Names returns the registered tool names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsReadOnly reports whether a tool is safe to run concurrently.
func (r *Registry) IsReadOnly(name string) bool {
	t, ok := r.Get(name)
	if !ok {
		return false
	}
	ro, ok := t.(ReadOnly)
	return ok && ro.ReadOnly()
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// toolTimeout bounds any single tool call. Long-running work (typecheck, apk
// builds) applies its own, shorter budget internally; this is the backstop that
// keeps a wedged subprocess from holding a build open forever.
const toolTimeout = 10 * time.Minute

// maxToolOutput caps what one tool call can add to the conversation.
const maxToolOutput = 32 * 1024

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (ToolResult, error) {
	t, ok := r.Get(name)
	if !ok {
		return ToolResult{
			Content: fmt.Sprintf("No tool named %q. Available tools: %v", name, r.Names()),
			IsError: true,
		}, nil
	}

	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	result, err := t.Execute(ctx, args)

	// A tool that returns an error has already described the problem in
	// Content most of the time; make sure something useful reaches the model
	// either way, and never propagate the error as a loop-ending failure — a
	// failed tool call is information the agent should react to.
	if err != nil && result.Content == "" {
		result.Content = err.Error()
		result.IsError = true
	}
	if err != nil {
		result.IsError = true
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.Content = fmt.Sprintf("%s timed out after %s", name, toolTimeout)
		result.IsError = true
	}
	if result.Content == "" {
		result.Content = "(no output)"
	}
	if len(result.Content) > maxToolOutput {
		result.Content = result.Content[:maxToolOutput] +
			fmt.Sprintf("\n\n[output truncated at %d KB]", maxToolOutput/1024)
	}

	return result, nil
}

// StoreAdapter wraps the store so tools can persist without importing it.
type StoreAdapter struct {
	CreateADRFn func(projectID int64, decision, context, options, outcome string) error
	AddMemoryFn func(projectID int64, entryType, content, metadata string) error
}

func (a *StoreAdapter) CreateADR(projectID int64, decision, context, options, outcome string) error {
	return a.CreateADRFn(projectID, decision, context, options, outcome)
}

func (a *StoreAdapter) AddMemory(projectID int64, entryType, content, metadata string) error {
	return a.AddMemoryFn(projectID, entryType, content, metadata)
}
