package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Baseline benchmarks for the perf plan (Phase 1). They measure wall time of
// the verify-gate primitives on a realistic workspace so each optimization
// must move a recorded number. TALON_BENCH_WS points at the workspace;
// default is workspaces/27 when present, else the benchmark skips (CI has no
// workspaces checked in).
func benchWorkspace() string {
	if ws := os.Getenv("TALON_BENCH_WS"); ws != "" {
		return ws
	}
	matches, _ := filepath.Glob("../../workspaces/*/tsconfig.json")
	if len(matches) > 0 {
		return matches[0][:len(matches[0])-len("/tsconfig.json")]
	}
	return ""
}

func BenchmarkTypecheck(b *testing.B) {
	ws := benchWorkspace()
	if ws == "" {
		b.Skip("no workspace available")
	}
	t := &RunTypecheck{WorkspacePath: ws}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := t.Execute(context.Background(), json.RawMessage(`{}`)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLintWalk(b *testing.B) {
	ws := benchWorkspace()
	if ws == "" {
		b.Skip("no workspace available")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := walkSource(ws, ws); err != nil {
			b.Fatal(err)
		}
	}
}
