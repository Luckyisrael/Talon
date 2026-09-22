package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/tools"
)

// TestReconcileStoresMapRows pins W4.6's data path: reconcile stores each
// file's map row through the walk's own extractors, edits refresh the row,
// and deletions drop it — so the view can neither show a file the tree lacks
// nor miss one it has (the same file-set law the walk obeys).
func TestReconcileStoresMapRows(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "v1") // Reconcile is a no-op without the flag
	root := t.TempDir()
	const appSrc = "export function boot() {}\nimport './util';\n"
	const utilSrc = "export const x = 1;\n"
	writeTree(t, root, map[string]string{"app.ts": appSrc, "util.ts": utilSrc})
	st := newIdxStore(t)
	stats := mustReconcile(t, root, st)
	if stats.Indexed != 2 {
		t.Fatalf("reconcile indexed %d files, want 2 (flag off?)", stats.Indexed)
	}

	rows, err := st.CodeFilesWithExports(7)
	if err != nil {
		t.Fatalf("CodeFilesWithExports: %v", err)
	}
	if len(rows) != 2 || rows[0].Path != "app.ts" || rows[1].Path != "util.ts" {
		h, herr := st.CodeFileHashes(7)
		t.Fatalf("rows = %+v (hashes=%+v herr=%v), want app.ts then util.ts", rows, h, herr)
	}
	if got, want := strings.Join(rows[0].Exports, ","), strings.Join(tools.ExtractExports(appSrc), ","); got != want {
		t.Errorf("app exports = %q, want the walk's extractor output %q", got, want)
	}
	if got := strings.Join(rows[0].Imports, ","); got != "./util" {
		t.Errorf("app imports = %q, want ./util", got)
	}
	if len(rows[1].Exports) != 1 || rows[1].Exports[0] != "x" || len(rows[1].Imports) != 0 {
		t.Errorf("util row wrong: %+v", rows[1])
	}

	// An edit re-stores the row on the next reconcile (hash mismatch).
	writeTree(t, root, map[string]string{"app.ts": "export function reboot() {}\n"})
	mustReconcile(t, root, st)
	rows, err = st.CodeFilesWithExports(7)
	if err != nil {
		t.Fatalf("CodeFilesWithExports after edit: %v", err)
	}
	if len(rows) != 2 || len(rows[0].Exports) != 1 || rows[0].Exports[0] != "reboot" {
		t.Errorf("edited row not refreshed: %+v", rows[0])
	}

	// A deletion drops the row — the view can't resurrect a removed file.
	if err := os.Remove(filepath.Join(root, "app.ts")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustReconcile(t, root, st)
	rows, err = st.CodeFilesWithExports(7)
	if err != nil {
		t.Fatalf("CodeFilesWithExports after delete: %v", err)
	}
	if len(rows) != 1 || rows[0].Path != "util.ts" {
		t.Errorf("deleted file's row survived: %+v", rows)
	}
}
