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
// Vars (not consts) purely so the timeout test can scale them down.
var toolTimeout = 10 * time.Minute

// buildToolTimeout is the one exception: build_apk is prebuild + a cold gradle
// compile, which measures ~13 minutes on this host. The generic budget made
// every cold agent-driven build die at exactly 10 minutes — the tool needs
// room for the real run, with the terminal stream (gradle output is mirrored
// live) serving as the visible heartbeat while it runs.
var buildToolTimeout = 30 * time.Minute

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

	// Reject arguments that do not fit the tool's schema before running anything.
	// A model can send fields keyed for a different tool; without this the tool
	// silently drops them and "succeeds" doing nothing, and the model retries the
	// same mistake for rounds. Failing here with a message that names the right
	// fields lets it self-correct in one.
	if msg, ok := validateArgs(name, t.InputSchema(), args); !ok {
		return ToolResult{Content: msg, IsError: true}, nil
	}

	budget := toolTimeout
	if name == "build_apk" {
		budget = buildToolTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
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
		result.Content = fmt.Sprintf("%s timed out after %s", name, budget)
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
// Both function fields are nilable — a tool built against this adapter
// should check before calling, because the wiring layer (api/buildsession)
// chooses which to populate per tool. That keeps the surface small for
// tools that only need one direction.
type StoreAdapter struct {
	CreateADRFn      func(projectID int64, decision, context, options, outcome string) error
	AddMemoryFn      func(projectID int64, entryType, content, metadata string) error
	GetAllMemoryFn   func(projectID int64) ([]MemoryEntry, error)
	GetMemoryByTypeFn func(projectID int64, entryType string) ([]MemoryEntry, error)
	SearchMemoryFn   func(projectID int64, query, entryType string, limit int) ([]MemoryEntry, error)
}

// MemoryEntry is the read-side view of a project memory row. It mirrors the
// fields WriteMemory would emit plus a creation timestamp; the store-side
// store.ProjectMemory type stays in the store package and is converted at the
// adapter boundary so tools never need to import the store.
type MemoryEntry struct {
	ID         int64
	EntryType  string
	Content    string
	Metadata   string
	CreatedAt  string
}

func (a *StoreAdapter) CreateADR(projectID int64, decision, context, options, outcome string) error {
	if a == nil || a.CreateADRFn == nil {
		return fmt.Errorf("ADR persistence is not wired in this session")
	}
	return a.CreateADRFn(projectID, decision, context, options, outcome)
}

func (a *StoreAdapter) AddMemory(projectID int64, entryType, content, metadata string) error {
	if a == nil || a.AddMemoryFn == nil {
		return fmt.Errorf("project memory is not wired in this session")
	}
	return a.AddMemoryFn(projectID, entryType, content, metadata)
}

// ListMemory returns every memory entry for a project, oldest first — the same
// order the system-prompt enrichment uses, so what the model sees in context
// matches what read_memory returns.
func (a *StoreAdapter) ListMemory(projectID int64) ([]MemoryEntry, error) {
	if a == nil || a.GetAllMemoryFn == nil {
		return nil, fmt.Errorf("project memory is not wired in this session")
	}
	return a.GetAllMemoryFn(projectID)
}

// ListMemoryByType returns the entries of one type, oldest first.
func (a *StoreAdapter) ListMemoryByType(projectID int64, entryType string) ([]MemoryEntry, error) {
	if a == nil || a.GetMemoryByTypeFn == nil {
		return nil, fmt.Errorf("project memory is not wired in this session")
	}
	return a.GetMemoryByTypeFn(projectID, entryType)
}

func (a *StoreAdapter) SearchMemory(projectID int64, query, entryType string, limit int) ([]MemoryEntry, error) {
	if a == nil || a.SearchMemoryFn == nil {
		// Fallback to List + substring — no FTS wired (tests without store).
		return nil, fmt.Errorf("search not wired")
	}
	return a.SearchMemoryFn(projectID, query, entryType, limit)
}
