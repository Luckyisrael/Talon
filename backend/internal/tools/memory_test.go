package tools

import (
	"context"
	"strings"
	"testing"
)

// fakeMemoryStore stands in for *StoreAdapter without depending on the real
// store package. The tool under test consumes a *StoreAdapter, so the test
// passes one with the two read hooks wired to in-memory slices. This mirrors
// the build-session wiring in api/buildsession.go.
func newMemoryAdapter(entries []MemoryEntry) *StoreAdapter {
	all := append([]MemoryEntry(nil), entries...)
	return &StoreAdapter{
		AddMemoryFn: func(_ int64, _, _, _ string) error {
			all = append(all, MemoryEntry{})
			return nil
		},
		GetAllMemoryFn: func(int64) ([]MemoryEntry, error) {
			return append([]MemoryEntry(nil), all...), nil
		},
		GetMemoryByTypeFn: func(_ int64, t string) ([]MemoryEntry, error) {
			var out []MemoryEntry
			for _, e := range all {
				if e.EntryType == t {
					out = append(out, e)
				}
			}
			return out, nil
		},
	}
}

func TestRecordFailurePatternCap(t *testing.T) {
	tool := NewRecordFailurePattern(newMemoryAdapter(nil), 1)
	args := `{"tool":"run_typecheck","match":"TS9999","cause":"test","fix":"fix it"}`
	for i := 0; i < 5; i++ {
		res, err := tool.Execute(context.Background(), []byte(args))
		if err != nil || strings.Contains(res.Content, "cap reached") {
			t.Fatalf("record %d should succeed: %+v %v", i+1, res, err)
		}
	}
	res, _ := tool.Execute(context.Background(), []byte(args))
	if !strings.Contains(res.Content, "cap reached") {
		t.Errorf("6th record should hit the session cap, got: %q", res.Content)
	}
}

func TestReadMemory(t *testing.T) {
	entries := []MemoryEntry{
		{ID: 1, EntryType: "fact", Content: "The project uses Expo SDK 55", Metadata: "package.json", CreatedAt: "2025-01-01 10:00:00 UTC"},
		{ID: 2, EntryType: "decision", Content: "Use Acid Lime as the only action color", Metadata: "ADR-0001", CreatedAt: "2025-01-02 10:00:00 UTC"},
		{ID: 3, EntryType: "warning", Content: "MWA mock wallet is installed on Redroid; do not bundle a real one", CreatedAt: "2025-01-03 10:00:00 UTC"},
	}

	t.Run("no filter returns the most recent first", func(t *testing.T) {
		rm := NewReadMemory(newMemoryAdapter(entries), 1)
		res, _ := rm.Execute(context.Background(), rawArgs(t, map[string]any{}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		// Newest-first means entry 3 appears before entry 2, which appears before entry 1.
		i3 := strings.Index(res.Content, "MWA mock wallet")
		i2 := strings.Index(res.Content, "Acid Lime")
		i1 := strings.Index(res.Content, "Expo SDK 55")
		if i3 < 0 || i2 < 0 || i1 < 0 {
			t.Fatalf("missing entry in output:\n%s", res.Content)
		}
		if !(i3 < i2 && i2 < i1) {
			t.Errorf("entries not newest-first: i3=%d i2=%d i1=%d\n%s", i3, i2, i1, res.Content)
		}
	})

	t.Run("filter by type narrows the result", func(t *testing.T) {
		rm := NewReadMemory(newMemoryAdapter(entries), 1)
		res, _ := rm.Execute(context.Background(), rawArgs(t, map[string]any{
			"type": "decision",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if !strings.Contains(res.Content, "Acid Lime") {
			t.Errorf("want the decision entry, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "Expo SDK 55") {
			t.Errorf("did not expect a fact entry, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "MWA mock wallet") {
			t.Errorf("did not expect a warning entry, got:\n%s", res.Content)
		}
	})

	t.Run("contains is case-insensitive and substring-based", func(t *testing.T) {
		rm := NewReadMemory(newMemoryAdapter(entries), 1)
		res, _ := rm.Execute(context.Background(), rawArgs(t, map[string]any{
			"contains": "MWA",
		}))
		if !strings.Contains(res.Content, "MWA mock wallet") {
			t.Errorf("want the warning, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "Acid Lime") {
			t.Errorf("did not expect non-matching entries, got:\n%s", res.Content)
		}
	})

	t.Run("empty store is a clear 'no entries' message, not an error", func(t *testing.T) {
		rm := NewReadMemory(newMemoryAdapter(nil), 1)
		res, _ := rm.Execute(context.Background(), rawArgs(t, map[string]any{}))
		if res.IsError {
			t.Fatalf("want a soft message, got error: %s", res.Content)
		}
		if !strings.Contains(res.Content, "No memory entries") {
			t.Errorf("want a 'no memory entries' message, got: %s", res.Content)
		}
	})

	t.Run("nil adapter is reported, not a panic", func(t *testing.T) {
		rm := NewReadMemory(nil, 1)
		res, _ := rm.Execute(context.Background(), rawArgs(t, map[string]any{}))
		if !res.IsError {
			t.Errorf("want an error for nil adapter, got: %s", res.Content)
		}
	})

	t.Run("is read-only", func(t *testing.T) {
		rm := NewReadMemory(newMemoryAdapter(entries), 1)
		if !rm.ReadOnly() {
			t.Error("read_memory must be read-only")
		}
	})
}
