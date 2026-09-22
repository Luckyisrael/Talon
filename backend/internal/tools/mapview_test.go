package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// W4.6's tool-side gates: the index view must be byte-identical to the walk
// for the same tree (one renderer, one extractor, one file-set law), and
// Execute must prefer the view when wired, degrade to the walk when not, and
// carry a note when a view rendered behind a failed refresh.

func writeMapFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"app.ts":      "export function boot() {}\nimport {x} from './util';\n",
		"util.ts":     "export const x = 1;\nexport default function helper() {}\n",
		"nested/m.ts": "export class Mesh {}\nconst secret = 2;\nimport 'left-pad';\n",
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// fixtureRows builds MapFile rows exactly the way reconcile stores them —
// the walk's rules with the exported extractors.
func fixtureRows(t *testing.T, root string) []MapFile {
	t.Helper()
	var files []MapFile
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			if path != root && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMappableSource(info.Name()) {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		src := string(data)
		files = append(files, MapFile{
			Path:    filepath.ToSlash(rel),
			Exports: ExtractExports(src),
			Imports: ExtractImports(src),
		})
		return nil
	})
	return files
}

// TestProjectMapIndexViewMatchesWalk is W4.6's parity gate: rows produced
// through the index's data path (same extractors, same file set) must render
// byte-identically to the walk.
func TestProjectMapIndexViewMatchesWalk(t *testing.T) {
	root := writeMapFixture(t)
	walk := ProjectMapText(root)
	if walk == "" {
		t.Fatal("walk produced no map for a non-empty fixture")
	}
	view := ProjectMapTextFromIndex(fixtureRows(t, root))
	if view != walk {
		t.Errorf("index view differs from walk:\n--- walk ---\n%s\n--- view ---\n%s", walk, view)
	}
}

func TestProjectMapToolPrefersIndexView(t *testing.T) {
	root := writeMapFixture(t)
	rows := fixtureRows(t, root)
	tool := &ProjectMap{
		FS:         NewFS(root),
		IndexFiles: func() ([]MapFile, error) { return rows, nil },
	}
	got := mustExecute(t, tool, map[string]any{})
	if want := ProjectMapTextFromIndex(rows); got.Content != want {
		t.Errorf("tool did not use the view:\ngot:\n%s\nwant:\n%s", got.Content, want)
	}
}

func TestProjectMapToolWithoutIndexWalks(t *testing.T) {
	root := writeMapFixture(t)
	tool := &ProjectMap{FS: NewFS(root)} // nil hooks = flag off
	got := mustExecute(t, tool, map[string]any{})
	if want := ProjectMapText(root); got.Content != want {
		t.Errorf("nil hooks changed the walk output:\ngot:\n%s\nwant:\n%s", got.Content, want)
	}
}

func TestProjectMapToolIndexFetchErrorWalks(t *testing.T) {
	root := writeMapFixture(t)
	tool := &ProjectMap{
		FS:         NewFS(root),
		IndexFiles: func() ([]MapFile, error) { return nil, errors.New("db gone") },
	}
	got := mustExecute(t, tool, map[string]any{})
	if want := ProjectMapText(root); got.Content != want {
		t.Errorf("fetch error should fall back to the walk:\ngot:\n%s\nwant:\n%s", got.Content, want)
	}
}

func TestProjectMapToolReconcileErrorNotes(t *testing.T) {
	root := writeMapFixture(t)
	rows := fixtureRows(t, root)
	tool := &ProjectMap{
		FS:         NewFS(root),
		Reconcile:  func() error { return errors.New("locked") },
		IndexFiles: func() ([]MapFile, error) { return rows, nil },
	}
	got := mustExecute(t, tool, map[string]any{})
	if !strings.Contains(got.Content, "[index refresh failed: locked") {
		t.Errorf("missing stale note:\n%s", got.Content)
	}
	if !strings.Contains(got.Content, "## Current project structure") {
		t.Errorf("view body missing:\n%s", got.Content)
	}
}

func TestProjectMapToolEmptyIndexFallsToWalkMessage(t *testing.T) {
	root := t.TempDir() // fresh project
	tool := &ProjectMap{
		FS:         NewFS(root),
		IndexFiles: func() ([]MapFile, error) { return nil, nil },
	}
	got := mustExecute(t, tool, map[string]any{})
	if !strings.Contains(got.Content, "No source files yet") {
		t.Errorf("empty index should keep the fresh-project message, got: %s", got.Content)
	}
}
