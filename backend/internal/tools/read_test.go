package tools

import (
	"context"
	"strings"
	"testing"
)

// SearchFiles is a name-alias for GrepCodebase. The contract under test is:
// (1) it accepts the same arguments, (2) it returns the same matches, and
// (3) it surfaces the alias name so a model that picks up the new vocabulary
// keeps working.
func TestSearchFiles(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "a.ts", "const alpha = 1\nconst beta = 2\nconst alpha2 = 3\n")
	seed(t, fs, "b.tsx", "export const Card = () => null\n")

	t.Run("Name reports the alias", func(t *testing.T) {
		if (&SearchFiles{Inner: &GrepCodebase{FS: fs}}).Name() != "search_files" {
			t.Error("search_files must be the registered name")
		}
	})

	t.Run("matches mirror grep_codebase", func(t *testing.T) {
		alias := &SearchFiles{Inner: &GrepCodebase{FS: fs}}
		res, _ := alias.Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "alpha",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		// Both lines matching in a.ts.
		if c := strings.Count(res.Content, "a.ts:"); c < 2 {
			t.Errorf("expected 2+ lines, got %d in:\n%s", c, res.Content)
		}
		// b.tsx should not match.
		if strings.Contains(res.Content, "b.tsx") {
			t.Errorf("b.tsx should not match alpha, got:\n%s", res.Content)
		}
	})

	t.Run("respects the glob", func(t *testing.T) {
		alias := &SearchFiles{Inner: &GrepCodebase{FS: fs}}
		res, _ := alias.Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "alpha", "glob": "*.tsx",
		}))
		// No .tsx file contains alpha → expect a clean "no matches" answer.
		if !strings.Contains(res.Content, "No matches") {
			t.Errorf("want no matches, got:\n%s", res.Content)
		}
	})

	t.Run("is read-only", func(t *testing.T) {
		if !(&SearchFiles{Inner: &GrepCodebase{FS: fs}}).ReadOnly() {
			t.Error("search_files must be read-only")
		}
	})

	t.Run("invalid regex is an error", func(t *testing.T) {
		alias := &SearchFiles{Inner: &GrepCodebase{FS: fs}}
		res, _ := alias.Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "[unclosed",
		}))
		if !res.IsError {
			t.Errorf("want a regex error, got: %s", res.Content)
		}
	})
}

// GrepCodebase behaviour that's worth pinning alongside the alias.
func TestGrepCodebaseBasics(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "a.ts", "const alpha = 1\nconst beta = 2\nconst alpha2 = 3\n")
	seed(t, fs, "b.tsx", "export const Card = () => null\n")

	t.Run("invalid regex is an error", func(t *testing.T) {
		res, _ := (&GrepCodebase{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "[unclosed",
		}))
		if !res.IsError {
			t.Errorf("want a regex error, got: %s", res.Content)
		}
	})
}
