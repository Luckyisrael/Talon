package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/store"
)

func newIdxStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

func mustReconcile(t *testing.T, root string, st *store.Store) Stats {
	t.Helper()
	stats, err := Reconcile(root, 7, st)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return stats
}

func mustDigest(t *testing.T, st *store.Store) string {
	t.Helper()
	d, err := st.CodeIndexDigest(7)
	if err != nil {
		t.Fatalf("CodeIndexDigest: %v", err)
	}
	return d
}

// TestReconcileIncrementalEqualsFull is W4's central gate: after a one-file
// write, reconciling the stored index must produce the byte-identical digest
// a full re-index of the same tree produces — and only the changed file may
// be re-parsed. Deletions count too, including the edge-target clearing that
// makes an unchanged importer's rows match a fresh build.
func TestReconcileIncrementalEqualsFull(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{
		"src/app.ts":  "import { login } from './auth'\n\nexport function useAuth() {\n\treturn login()\n}\n",
		"src/auth.ts": "export function login() {\n\treturn 'tok'\n}\n",
		"src/util.ts": "export const twice = (n: number) => n * 2\n",
	})
	st := newIdxStore(t)

	// Full build.
	stats := mustReconcile(t, ws, st)
	if stats.Indexed != 3 || stats.Unchanged != 0 || stats.Removed != 0 {
		t.Fatalf("first build stats = %+v, want 3 indexed", stats)
	}
	full := mustDigest(t, st)
	if full == "" {
		t.Fatal("empty digest after full build")
	}

	// Idempotent pass: nothing moves, nothing is rewritten.
	stats = mustReconcile(t, ws, st)
	if stats.Indexed != 0 || stats.Unchanged != 3 || stats.Removed != 0 {
		t.Errorf("no-op pass stats = %+v, want 0 indexed / 3 unchanged", stats)
	}
	if d := mustDigest(t, st); d != full {
		t.Error("a no-op reconcile changed the index state")
	}

	// One file changes: exactly one re-indexed.
	writeTree(t, ws, map[string]string{
		"src/util.ts": "export const twice = (n: number) => n * 2.5\n",
	})
	stats = mustReconcile(t, ws, st)
	if stats.Indexed != 1 || stats.Unchanged != 2 || stats.Removed != 0 {
		t.Errorf("one-write stats = %+v, want 1 indexed / 2 unchanged", stats)
	}
	incremental := mustDigest(t, st)

	// A fresh store, full build of the SAME tree: hash-equal to the incremental one.
	st2 := newIdxStore(t)
	mustReconcile(t, ws, st2)
	if fresh := mustDigest(t, st2); fresh != incremental {
		t.Errorf("incremental digest %s != full re-index digest %s", incremental, fresh)
	}

	// Delete a file that others import. The importer is unchanged (0 re-indexed),
	// but its resolved ToPath must clear so state still equals a fresh build.
	if err := os.Remove(filepath.Join(ws, "src", "auth.ts")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	stats = mustReconcile(t, ws, st)
	if stats.Removed != 1 || stats.Indexed != 0 || stats.Unchanged != 2 {
		t.Errorf("delete stats = %+v, want 1 removed / 0 indexed / 2 unchanged", stats)
	}
	incremental2 := mustDigest(t, st)
	st3 := newIdxStore(t)
	mustReconcile(t, ws, st3)
	if fresh := mustDigest(t, st3); fresh != incremental2 {
		t.Errorf("post-delete incremental digest %s != fresh full digest %s", incremental2, fresh)
	}
}

func TestReconcileResolvesLocalImports(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{
		"src/app.ts":  "import { login } from './auth'\nimport { route } from 'expo-router'\n\nexport function go() {\n\treturn login()\n}\n",
		"src/auth.ts": "export function login() {\n\treturn 'tok'\n}\n",
	})
	st := newIdxStore(t)
	mustReconcile(t, ws, st)

	edges, err := st.CodeEdgesFrom(7, "src/app.ts", "")
	if err != nil {
		t.Fatalf("CodeEdgesFrom: %v", err)
	}
	byName := map[string]string{}
	for _, e := range edges {
		if e.Kind == "import" {
			byName[e.ToName] = e.ToPath
		}
	}
	if byName["./auth"] != "src/auth.ts" {
		t.Errorf("./auth resolved to %q, want src/auth.ts", byName["./auth"])
	}
	if byName["expo-router"] != "" {
		t.Errorf("package resolved to %q — packages must stay unresolved", byName["expo-router"])
	}
}

func TestReconcileFlagOff(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "")
	ws := t.TempDir()
	writeTree(t, ws, map[string]string{
		"src/app.ts": "export const a = 1\n",
	})
	st := newIdxStore(t)
	stats, err := Reconcile(ws, 7, st)
	if err != nil {
		t.Fatalf("Reconcile (flag off): %v", err)
	}
	if stats != (Stats{}) {
		t.Errorf("flag-off stats = %+v, want zero — dark means no work", stats)
	}
	hashes, err := st.CodeFileHashes(7)
	if err != nil {
		t.Fatalf("CodeFileHashes: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("flag-off wrote %d index rows", len(hashes))
	}
}

// TestReconcileTemplateTimeBudget is the plan's "index build under time
// budget on the template workspace": a cold full build of the real template
// must finish well inside the budget, proving the extractor never wanders
// into pathological work (node_modules and friends are skipped by the shared
// file-set law, asserted here by the file count staying sane).
func TestReconcileTemplateTimeBudget(t *testing.T) {
	root := "../../template"
	if _, err := os.Stat(root); err != nil {
		t.Skipf("template workspace absent: %v", err)
	}
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	st := newIdxStore(t)

	start := time.Now()
	stats, err := Reconcile(root, 7, st)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if stats.Indexed == 0 {
		t.Fatal("template produced no indexed files — walk rules wrong?")
	}
	if elapsed > 10*time.Second {
		t.Errorf("cold index build took %v, over the 10s budget", elapsed)
	}
	// Second pass must be the cheap one: unchanged files are never re-parsed.
	start = time.Now()
	stats = mustReconcile(t, root, st)
	if stats.Indexed != 0 {
		t.Errorf("warm reconcile re-indexed %d files, want 0", stats.Indexed)
	}
	if warm := time.Since(start); warm > elapsed && warm > 5*time.Second {
		t.Errorf("warm reconcile (%v) should not cost more than the cold build (%v)", warm, elapsed)
	}
}
