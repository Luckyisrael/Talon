package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The fusions (memory + code chunks) both run inside their tools' Execute —
// these tests pin them end to end through the real tool outputs: fused
// order, the vector-off degenerate case (byte-identical to today), and the
// degrade paths.

func mustExecute(t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) (ToolResult, error)
}, args map[string]any) ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	return res
}

// indexOfContent finds a line's position in read_memory output by content
// fragment — order assertions don't depend on the exact [type] date prefix.
func lineOrder(t *testing.T, content string, fragments ...string) []int {
	t.Helper()
	lines := strings.Split(content, "\n")
	pos := make([]int, len(fragments))
	for i, f := range fragments {
		pos[i] = -1
		for j, line := range lines {
			if strings.Contains(line, f) {
				pos[i] = j
				break
			}
		}
		if pos[i] < 0 {
			t.Errorf("fragment %q not found in:\n%s", f, content)
		}
	}
	return pos
}

// TestReadMemoryFusesVectorLeg: the row both legs see (ID 2) outranks rows
// each leg saw alone; among single-leg rows the tie resolves toward the
// lexical list (fuse passes FTS first).
func TestReadMemoryFusesVectorLeg(t *testing.T) {
	store := &StoreAdapter{
		SearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return []MemoryEntry{
				{ID: 1, EntryType: "fact", Content: "lex-only"},
				{ID: 2, EntryType: "fact", Content: "both-legs"},
			}, nil
		},
		VectorSearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return []MemoryEntry{
				{ID: 3, EntryType: "fact", Content: "vec-only"},
				{ID: 2, EntryType: "fact", Content: "both-legs"},
			}, nil
		},
	}
	res := mustExecute(t, NewReadMemory(store, 7), map[string]any{"contains": "anything"})
	order := lineOrder(t, res.Content, "both-legs", "lex-only", "vec-only")
	if !(order[0] < order[1] && order[1] < order[2]) {
		t.Errorf("want [both-legs, lex-only, vec-only], got positions %v in:\n%s", order, res.Content)
	}
}

// TestReadMemoryVectorOnlyAnswers: FTS phrase-missed everything the vector
// found — the fusion must still answer instead of falling to substring.
func TestReadMemoryVectorOnlyAnswers(t *testing.T) {
	store := &StoreAdapter{
		SearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return nil, nil // phrase matched nothing
		},
		VectorSearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return []MemoryEntry{{ID: 9, EntryType: "decision", Content: "vector-found-it"}}, nil
		},
	}
	res := mustExecute(t, NewReadMemory(store, 7), map[string]any{"contains": "query"})
	if !strings.Contains(res.Content, "vector-found-it") {
		t.Errorf("vector-only row missing from:\n%s", res.Content)
	}
}

// TestReadMemoryVectorOffIsUnchanged: nil VectorSearchMemoryFn — the
// flag-off / provider-off deployment — must reproduce today's FTS order
// exactly, with no fusion and no substring detour.
func TestReadMemoryVectorOffIsUnchanged(t *testing.T) {
	store := &StoreAdapter{
		SearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return []MemoryEntry{
				{ID: 1, EntryType: "fact", Content: "first-fts"},
				{ID: 2, EntryType: "fact", Content: "second-fts"},
			}, nil
		},
	}
	res := mustExecute(t, NewReadMemory(store, 7), map[string]any{"contains": "query"})
	order := lineOrder(t, res.Content, "first-fts", "second-fts")
	if !(order[0] < order[1]) {
		t.Errorf("FTS-only order changed: positions %v in:\n%s", order, res.Content)
	}
	if strings.Contains(res.Content, "No memory entries") {
		t.Errorf("unexpected empty result:\n%s", res.Content)
	}
}

// TestReadMemoryVectorErrorDegrades: a configured-but-broken provider must
// not fail a recall the lexical leg can still answer — degrade, no error.
func TestReadMemoryVectorErrorDegrades(t *testing.T) {
	store := &StoreAdapter{
		SearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return []MemoryEntry{{ID: 1, EntryType: "fact", Content: "lexical-survives"}}, nil
		},
		VectorSearchMemoryFn: func(int64, string, string, int) ([]MemoryEntry, error) {
			return nil, context.DeadlineExceeded
		},
	}
	res := mustExecute(t, NewReadMemory(store, 7), map[string]any{"contains": "query"})
	if !strings.Contains(res.Content, "lexical-survives") {
		t.Errorf("lexical results lost when the vector leg errored:\n%s", res.Content)
	}
}

// TestCodebaseSearchFusesVectorLeg: the "same fusion" on the other tool —
// a chunk both legs rank rises; single-leg chunks follow with lexical
// tie-break; output keeps the path:line contract.
func TestCodebaseSearchFusesVectorLeg(t *testing.T) {
	search := &CodebaseSearch{
		Search: func(string, int) ([]IndexHit, error) {
			return []IndexHit{
				{Path: "src/a.ts", Name: "alpha", StartLine: 1, Body: "const alpha = 1"},
				{Path: "src/b.ts", Name: "beta", StartLine: 5, Body: "const beta = 2"},
			}, nil
		},
		VectorSearch: func(string, int) ([]IndexHit, error) {
			return []IndexHit{
				{Path: "src/c.ts", Name: "gamma", StartLine: 9, Body: "const gamma = 3"},
				{Path: "src/b.ts", Name: "beta", StartLine: 5, Body: "const beta = 2"},
			}, nil
		},
	}
	res := mustExecute(t, search, map[string]any{"query": "anything"})
	// b (both lists) first, then the exact-score tie a (lexical) over c.
	idx := lineOrder(t, res.Content, "src/b.ts:5", "src/a.ts:1", "src/c.ts:9")
	if !(idx[0] < idx[1] && idx[1] < idx[2]) {
		t.Errorf("want [b, a, c], got positions %v in:\n%s", idx, res.Content)
	}
}

// TestCodebaseSearchVectorOffIsUnchanged: no VectorSearch field — flag-off
// deployments keep the exact lexical-only behavior.
func TestCodebaseSearchVectorOffIsUnchanged(t *testing.T) {
	search := &CodebaseSearch{
		Search: func(string, int) ([]IndexHit, error) {
			return []IndexHit{
				{Path: "src/a.ts", Name: "alpha", StartLine: 1, Body: "const alpha = 1"},
				{Path: "src/b.ts", Name: "beta", StartLine: 5, Body: "const beta = 2"},
			}, nil
		},
	}
	res := mustExecute(t, search, map[string]any{"query": "anything"})
	idx := lineOrder(t, res.Content, "src/a.ts:1", "src/b.ts:5")
	if !(idx[0] < idx[1]) {
		t.Errorf("lexical-only order changed: positions %v in:\n%s", idx, res.Content)
	}
	if strings.Contains(res.Content, "vector leg unavailable") {
		t.Errorf("spurious vector note:\n%s", res.Content)
	}
}

// TestCodebaseSearchVectorErrorNotes: broken provider surfaces an honest
// inline note while lexical results keep flowing.
func TestCodebaseSearchVectorErrorNotes(t *testing.T) {
	search := &CodebaseSearch{
		Search: func(string, int) ([]IndexHit, error) {
			return []IndexHit{{Path: "src/a.ts", Name: "alpha", StartLine: 1, Body: "const alpha = 1"}}, nil
		},
		VectorSearch: func(string, int) ([]IndexHit, error) {
			return nil, context.DeadlineExceeded
		},
	}
	res := mustExecute(t, search, map[string]any{"query": "anything"})
	if !strings.Contains(res.Content, "src/a.ts:1") {
		t.Errorf("lexical results lost:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "vector leg unavailable") {
		t.Errorf("missing degradation note:\n%s", res.Content)
	}
}
