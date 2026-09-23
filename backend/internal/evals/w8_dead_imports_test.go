package evals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deadPkgExceptions declares every harness package the dead-import law
// (law 8, PLAN_WORLDCLASS W8.3) currently tolerates. An entry needs a
// reason AND a closing gate — an exception without either is a silent
// half-life, which is the exact failure mode the law exists to prevent.
// Stale entries (package gained a real importer, or was deleted) fail
// this test so the map cannot rot either.
var deadPkgExceptions = map[string]string{}

// TestPhaseW8NoDeadImports: any package under internal/harness with zero
// non-test importers fails make evals. Dead code must meet one of three
// fates — wired, declared (a reason-carrying exception above), or
// deleted. This is law 8 enforced by CI, permanently.
func TestPhaseW8NoDeadImports(t *testing.T) {
	root := backendRoot(t)

	harnessDir := filepath.Join(root, "internal", "harness")
	entries, err := os.ReadDir(harnessDir)
	if err != nil {
		t.Fatalf("read %s: %v", harnessDir, err)
	}

	type pkg struct{ name, path string }
	var pkgs []pkg
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(harnessDir, e.Name())
		if !hasNonTestGo(dir) {
			continue
		}
		imp := "github.com/luckyisrael/talon/backend/internal/harness/" + e.Name()
		pkgs = append(pkgs, pkg{name: e.Name(), path: imp})
	}
	if len(pkgs) == 0 {
		t.Fatal("no harness packages discovered — the walker is broken, not the tree")
	}

	// Index every non-test Go file's import block. A file inside a
	// package's own directory cannot import it (Go forbids self-import),
	// and test files are excluded by the law's wording — everything else,
	// siblings inside internal/harness included, counts as a consumer.
	importers := map[string]int{}
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, p := range pkgs {
			if strings.HasPrefix(path, filepath.Join(harnessDir, p.name)+string(filepath.Separator)) {
				continue // own directory: self-import is impossible
			}
			if strings.Contains(string(data), `"`+p.path+`"`) {
				importers[p.path]++
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}

	seen := map[string]bool{}
	for _, p := range pkgs {
		seen[p.name] = true
		if importers[p.path] > 0 {
			if reason, ok := deadPkgExceptions[p.name]; ok {
				t.Errorf("stale exception: %q now has %d non-test importer(s); drop it from deadPkgExceptions (%s)",
					p.name, importers[p.path], reason)
			}
			continue
		}
		if _, ok := deadPkgExceptions[p.name]; ok {
			continue // declared above, with a reason and a closing gate
		}
		t.Errorf("law 8: internal/harness/%s has zero non-test importers — "+
			"wire it, declare it in deadPkgExceptions with a reason and a closing gate, or delete it",
			p.name)
	}
	for name, reason := range deadPkgExceptions {
		if !seen[name] {
			t.Errorf("stale exception: harness package %q no longer exists; remove it (%s)", name, reason)
		}
	}
}

// hasNonTestGo reports whether dir contains at least one buildable
// non-test Go file.
func hasNonTestGo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}

// backendRoot locates the directory holding go.mod, walking up from the
// test's working directory (backend/internal/evals).
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test working directory")
		}
		dir = parent
	}
}
