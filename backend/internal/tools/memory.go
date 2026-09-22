package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/luckyisrael/talon/backend/internal/recall"
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

// ReadMemory is the symmetric read counterpart of WriteMemory. The orchestrator
// already injects the most recent memory entries into the system prompt, but
// that view is capped (a long-running project will have far more entries than
// fit). This tool lets the model pull a specific type, a single entry, or
// everything when it needs to recall a decision from a much earlier round.
type ReadMemory struct {
	Store     *StoreAdapter
	ProjectID int64
}

func NewReadMemory(store *StoreAdapter, projectID int64) *ReadMemory {
	return &ReadMemory{Store: store, ProjectID: projectID}
}

func (t *ReadMemory) Name() string { return "read_memory" }

func (t *ReadMemory) Description() string {
	return "Recall durable facts recorded by write_memory. Useful when the in-context memory tail " +
		"is too short to find what you need. Filter by type (convention, decision, architecture, " +
		"fact, warning) or pass contains to substring-match content. Without filters, returns the " +
		"most recent entries first."
}

func (t *ReadMemory) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"type":     {"type": "string", "description": "Optional: filter by entry type — convention, decision, architecture, fact, warning"},
			"contains": {"type": "string", "description": "Optional: case-insensitive substring match against content"}
		},
		"required": []
	}`)
}

const readMemoryCap = 50

func (t *ReadMemory) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Type     string `json:"type"`
		Contains string `json:"contains"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if t.Store == nil {
		return errResult("project memory is not available in this session"), nil
	}

	var entries []MemoryEntry
	var err error
	// fuse merges both recall legs by memory-row ID through recall.Fuse —
	// FTS first, so exact-score ties resolve toward lexical evidence. Only
	// called when the vector leg produced something: with no provider (or
	// one that failed) the FTS path below is byte-identical to today's.
	fuse := func(fts, vec []MemoryEntry) []MemoryEntry {
		byID := map[string]MemoryEntry{}
		lexIDs := make([]string, 0, len(fts))
		vecIDs := make([]string, 0, len(vec))
		for _, e := range fts {
			id := strconv.FormatInt(e.ID, 10)
			lexIDs = append(lexIDs, id)
			byID[id] = e
		}
		for _, e := range vec {
			id := strconv.FormatInt(e.ID, 10)
			if _, dup := byID[id]; !dup {
				byID[id] = e
			}
			vecIDs = append(vecIDs, id)
		}
		fused := recall.Fuse(lexIDs, vecIDs)
		out := make([]MemoryEntry, 0, len(fused))
		for _, id := range fused {
			out = append(out, byID[id])
		}
		if len(out) > readMemoryCap {
			out = out[:readMemoryCap]
		}
		return out
	}
	// Hybrid: when contains is set, try FTS5 tokenised search first — it
	// handles stemming and prefix, so "wallet" hits "wallets" and "MWA".
	// W4.5 adds the vector leg when it exists (nil = provider off, by
	// configuration); both legs fuse when both answered. Falls back to
	// substring when neither did (tests without a store, or a phrase no
	// leg could match).
	if a.Contains != "" {
		fts, ferr := t.Store.SearchMemory(t.ProjectID, a.Contains, a.Type, readMemoryCap)
		var vec []MemoryEntry
		if t.Store != nil && t.Store.VectorSearchMemoryFn != nil {
			// The wiring returns (nil, nil) when no provider is configured;
			// an error means configured-but-unavailable — degrade to FTS,
			// never fail a recall the lexical leg can still answer.
			if v, verr := t.Store.VectorSearchMemoryFn(t.ProjectID, a.Contains, a.Type, readMemoryCap); verr == nil {
				vec = v
			}
		}
		if len(vec) > 0 {
			entries = fuse(fts, vec)
		} else if ferr == nil && len(fts) > 0 {
			entries = fts
		} else {
			if a.Type != "" {
				entries, err = t.Store.ListMemoryByType(t.ProjectID, a.Type)
			} else {
				entries, err = t.Store.ListMemory(t.ProjectID)
			}
			if err != nil {
				return errResult("could not read memory: %v", err), nil
			}
			if len(entries) > 0 {
				for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
			needle := strings.ToLower(a.Contains)
			filtered := entries[:0]
			for _, e := range entries {
				if strings.Contains(strings.ToLower(e.Content), needle) || strings.Contains(strings.ToLower(e.Metadata), needle) {
					filtered = append(filtered, e)
				}
			}
			entries = filtered
			if len(entries) > readMemoryCap {
				entries = entries[:readMemoryCap]
			}
		}
	} else {
		if a.Type != "" {
			entries, err = t.Store.ListMemoryByType(t.ProjectID, a.Type)
		} else {
			entries, err = t.Store.ListMemory(t.ProjectID)
		}
		if err != nil {
			return errResult("could not read memory: %v", err), nil
		}
		if len(entries) == 0 {
			return ToolResult{Content: "No memory entries match."}, nil
		}
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
		if len(entries) > readMemoryCap {
			entries = entries[:readMemoryCap]
		}
	}
	if len(entries) == 0 {
		return ToolResult{Content: "No memory entries match."}, nil
	}

	var b strings.Builder
	for i, e := range entries {
		fmt.Fprintf(&b, "[%s] %s: %s", e.EntryType, e.CreatedAt, e.Content)
		if e.Metadata != "" {
			fmt.Fprintf(&b, " (in %s)", e.Metadata)
		}
		if i != len(entries)-1 {
			b.WriteString("\n")
		}
	}
	return ToolResult{Content: b.String()}, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// RecordFailurePattern persists a runtime-discovered (tool, error) → fix
// mapping as a memory entry. The orchestrator loads these back into the
// failure index each turn, so the model gets the fix on the next occurrence
// without reasoning it from scratch. The pattern is deliberately simple:
// tool name + error substring + one-line cause + one-line fix. Cooldown
// defaults to 3 rounds.
type RecordFailurePattern struct {
	Store     *StoreAdapter
	ProjectID int64

	// recorded counts patterns this session. The instance is built per
	// session, so this is a per-session cap: at most 5 recorded patterns
	// before the model must generalize instead of appending.
	mu       sync.Mutex
	recorded int
}

const maxFailurePatternsPerSession = 5

func NewRecordFailurePattern(store *StoreAdapter, projectID int64) *RecordFailurePattern {
	return &RecordFailurePattern{Store: store, ProjectID: projectID}
}

func (t *RecordFailurePattern) Name() string { return "record_failure_pattern" }

func (t *RecordFailurePattern) Description() string {
	return "Record a runtime-discovered failure pattern so the next occurrence of this error gets an " +
		"automatic one-line fix reminder instead of wasting a round reasoning. Only record patterns " +
		"you have actually fixed — never guess. The pattern is stored as a project memory entry."
}

func (t *RecordFailurePattern) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tool":  {"type": "string", "description": "Tool that failed (e.g. run_typecheck). Empty string means any tool."},
			"match": {"type": "string", "description": "Substring or keyword that identifies this error in the output (will be normalized)."},
			"cause": {"type": "string", "description": "One-line cause of the failure."},
			"fix":   {"type": "string", "description": "One-line fix the model should apply next time."},
			"skill": {"type": "string", "description": "Optional skill name that covers this pattern in detail."}
		},
		"required": ["tool", "match", "cause", "fix"]
	}`)
}

func (t *RecordFailurePattern) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Tool  string `json:"tool"`
		Match string `json:"match"`
		Cause string `json:"cause"`
		Fix   string `json:"fix"`
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(a.Match) == "" || strings.TrimSpace(a.Fix) == "" {
		return errResult("match and fix are required"), nil
	}
	if t.Store == nil {
		return errResult("project memory is not available in this session"), nil
	}
	t.mu.Lock()
	if t.recorded >= maxFailurePatternsPerSession {
		t.mu.Unlock()
		return errResult("session pattern cap reached (%d) — generalize an existing pattern instead of recording another", maxFailurePatternsPerSession), nil
	}
	t.recorded++
	t.mu.Unlock()
	content := fmt.Sprintf("tool=%s | match=%s | cause=%s | fix=%s", a.Tool, a.Match, a.Cause, a.Fix)
	metadata := a.Skill
	if err := t.Store.AddMemory(t.ProjectID, "failure_pattern", content, metadata); err != nil {
		return errResult("could not save pattern: %v", err), nil
	}
	return ToolResult{Content: fmt.Sprintf("Recorded failure pattern: %s → %s", a.Match, truncate(a.Fix, 60))}, nil
}
