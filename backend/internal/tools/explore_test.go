package tools

import (
	"context"
	"strings"
	"testing"
)

func TestExplore(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "app/index.tsx", "export default function Index() {}")
	seed(t, fs, "app/Card.tsx", "export const Card = () => null")
	seed(t, fs, "app/api/route.ts", "export const GET = () => null")
	seed(t, fs, "lib/state.ts", "export const state = {}")
	seed(t, fs, "node_modules/dep/index.js", "ignored")

	t.Run("no args: a bare tree is the most useful thing to give", func(t *testing.T) {
		// explore is the orientation tool. With no name, pattern, or read, the
		// caller has signalled "I do not know what's here yet" — exactly the case
		// where a tree is the right answer. The other tools return an error in
		// this state; explore returns the tree because that is what "I am lost"
		// looks like in practice.
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{}))
		if res.IsError {
			t.Fatalf("want a tree, got error: %s", res.Content)
		}
		if !strings.Contains(res.Content, "app/index.tsx") {
			t.Errorf("want a tree, got:\n%s", res.Content)
		}
	})

	t.Run("name and read together return a single combined report", func(t *testing.T) {
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"name": "*.tsx",
			"read": "app/index.tsx",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		// Section headers must both appear.
		if !strings.Contains(res.Content, "## tree") {
			t.Errorf("want a tree section, got:\n%s", res.Content)
		}
		if !strings.Contains(res.Content, "## find_files") {
			t.Errorf("want a find_files section, got:\n%s", res.Content)
		}
		if !strings.Contains(res.Content, "## read_file app/index.tsx") {
			t.Errorf("want a read_file section, got:\n%s", res.Content)
		}
		// The tree must not descend into node_modules.
		if strings.Contains(res.Content, "node_modules") {
			t.Errorf("tree should skip node_modules, got:\n%s", res.Content)
		}
	})

	t.Run("pattern searches code files only", func(t *testing.T) {
		seed(t, fs, "lib/tokens.css", ".acid { color: #caff04 }")
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "color: #",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if !strings.Contains(res.Content, "lib/tokens.css:1") {
			t.Errorf("want a CSS match in the report, got:\n%s", res.Content)
		}
		if !strings.Contains(res.Content, "## pattern") {
			t.Errorf("want a pattern section, got:\n%s", res.Content)
		}
	})

	t.Run("invalid regex is surfaced, but tree and other sub-actions still ship", func(t *testing.T) {
		// The contract is "one failing sub-action must not poison the others".
		// A regex error shows up inline as "(failed: ...)" — the rest of the
		// report stays useful, and the top-level result is not IsError because
		// some sections did succeed.
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"pattern": "[unclosed",
		}))
		if !strings.Contains(res.Content, "invalid regular expression") {
			t.Errorf("want the regex error in the report, got:\n%s", res.Content)
		}
		// Tree should still be there.
		if !strings.Contains(res.Content, "## tree") {
			t.Errorf("want the tree to survive a sub-action failure, got:\n%s", res.Content)
		}
	})

	t.Run("missing read target is reported, not fatal", func(t *testing.T) {
		// Pair with a name arg so anyRan is true and explore does not bail early.
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"name": "*.tsx",
			"read": "nope.ts",
		}))
		if res.IsError {
			t.Fatalf("explore should not error on a missing read when name is set: %s", res.Content)
		}
		if !strings.Contains(res.Content, "could not read") {
			t.Errorf("want a graceful missing-file note, got:\n%s", res.Content)
		}
		if !strings.Contains(res.Content, "Use list_files") {
			t.Errorf("want a hint pointing the model at list_files, got:\n%s", res.Content)
		}
	})

	t.Run("rejects path escape on scope", func(t *testing.T) {
		res, _ := (&Explore{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "../etc",
		}))
		if !res.IsError {
			t.Errorf("want a path-escape error, got: %s", res.Content)
		}
	})

	t.Run("is read-only", func(t *testing.T) {
		if !(&Explore{FS: fs}).ReadOnly() {
			t.Error("explore must be read-only")
		}
	})
}
