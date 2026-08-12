package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestFS gives each test its own workspace so writes never collide.
func newTestFS(t *testing.T) *FS {
	t.Helper()
	return NewFS(t.TempDir())
}

func seed(t *testing.T, fs *FS, rel, content string) {
	t.Helper()
	if err := fs.Write(rel, content); err != nil {
		t.Fatalf("seed %s: %v", rel, err)
	}
}

func rawArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return b
}

// ── read_file: offset / limit / continuation ──────────

func TestReadFileOffsetLimit(t *testing.T) {
	fs := newTestFS(t)
	// 10 numbered lines, no trailing newline so Split yields exactly 10.
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "line" + string(rune('0'+i))
	}
	seed(t, fs, "a.ts", strings.Join(lines, "\n"))
	rf := &ReadFile{FS: fs}

	t.Run("window with continuation hint", func(t *testing.T) {
		res, _ := rf.Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "offset": 3, "limit": 2,
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		// Shows lines 3-4 only.
		if !strings.Contains(res.Content, "line2") || !strings.Contains(res.Content, "line3") {
			t.Errorf("want lines 3-4 present, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "line4") {
			t.Errorf("line 5 leaked past the limit:\n%s", res.Content)
		}
		// Continuation tells the model exactly where to resume.
		if !strings.Contains(res.Content, "offset=5") {
			t.Errorf("missing continuation hint, got:\n%s", res.Content)
		}
	})

	t.Run("default limit shows everything, no hint", func(t *testing.T) {
		res, _ := rf.Execute(context.Background(), rawArgs(t, map[string]any{"path": "a.ts"}))
		if !strings.Contains(res.Content, "line0") || !strings.Contains(res.Content, "line9") {
			t.Errorf("want full file, got:\n%s", res.Content)
		}
		if strings.Contains(res.Content, "to continue") {
			t.Errorf("continuation hint shown when whole file fit:\n%s", res.Content)
		}
	})

	t.Run("offset past end is an error, not empty output", func(t *testing.T) {
		res, _ := rf.Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "offset": 99,
		}))
		if !res.IsError {
			t.Fatalf("want error for out-of-range offset, got:\n%s", res.Content)
		}
	})

	t.Run("missing file names list_files", func(t *testing.T) {
		res, _ := rf.Execute(context.Background(), rawArgs(t, map[string]any{"path": "nope.ts"}))
		if !res.IsError || !strings.Contains(res.Content, "list_files") {
			t.Errorf("want a not-found error pointing at list_files, got:\n%s", res.Content)
		}
	})
}

// ── edit_file ─────────────────────────────────────────

func TestEditFile(t *testing.T) {
	rf := func(fs *FS, rel string) string {
		t.Helper()
		c, _, err := fs.Read(rel)
		if err != nil {
			t.Fatalf("read back %s: %v", rel, err)
		}
		return c
	}

	t.Run("single exact replacement", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "a.ts", "const a = 1\nconst b = 2\n")
		res, _ := (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "old_string": "const b = 2", "new_string": "const b = 3",
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if got := rf(fs, "a.ts"); !strings.Contains(got, "const b = 3") || strings.Contains(got, "const b = 2") {
			t.Errorf("edit not applied: %q", got)
		}
	})

	t.Run("ambiguous match refused without replace_all", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "a.ts", "x\nx\n")
		res, _ := (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "old_string": "x", "new_string": "y",
		}))
		if !res.IsError || !strings.Contains(res.Content, "appears 2 times") {
			t.Errorf("want ambiguity error, got: %s", res.Content)
		}
		if got := rf(fs, "a.ts"); got != "x\nx\n" {
			t.Errorf("file changed despite refused edit: %q", got)
		}
	})

	t.Run("replace_all changes every occurrence", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "a.ts", "x\nx\n")
		res, _ := (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "old_string": "x", "new_string": "y", "replace_all": true,
		}))
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if got := rf(fs, "a.ts"); got != "y\ny\n" {
			t.Errorf("replace_all wrong: %q", got)
		}
	})

	t.Run("whitespace mismatch gets a targeted hint", func(t *testing.T) {
		fs := newTestFS(t)
		// File is tab-indented; the caller searches with spaces, so the exact
		// string is absent but its trimmed form is present.
		seed(t, fs, "a.ts", "\treturn 1\n")
		res, _ := (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "old_string": "    return 1", "new_string": "    return 2",
		}))
		if !res.IsError || !strings.Contains(res.Content, "whitespace differs") {
			t.Errorf("want a whitespace hint, got: %s", res.Content)
		}
	})

	t.Run("identical strings are a no-op error", func(t *testing.T) {
		fs := newTestFS(t)
		seed(t, fs, "a.ts", "same\n")
		res, _ := (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
			"path": "a.ts", "old_string": "same", "new_string": "same",
		}))
		if !res.IsError || !strings.Contains(res.Content, "identical") {
			t.Errorf("want identical-strings error, got: %s", res.Content)
		}
	})
}

// ── multi_edit atomicity ──────────────────────────────

// A multi_edit that fails partway must leave the file exactly as it was: the
// whole point of the tool is that a change spanning several places lands all at
// once or not at all.
func TestMultiEditAtomicity(t *testing.T) {
	fs := newTestFS(t)
	original := "alpha\nbeta\ngamma\n"
	seed(t, fs, "a.ts", original)

	res, _ := (&MultiEdit{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
		"path": "a.ts",
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "ALPHA"},   // would succeed
			{"old_string": "nonexistent", "new_string": "X"}, // fails
		},
	}))
	if !res.IsError || !strings.Contains(res.Content, "No edits were applied") {
		t.Fatalf("want atomic failure, got: %s", res.Content)
	}

	got, _, _ := fs.Read("a.ts")
	if got != original {
		t.Errorf("file was mutated despite atomic failure:\ngot:  %q\nwant: %q", got, original)
	}
}

func TestMultiEditAllApplied(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "a.ts", "alpha\nbeta\n")
	res, _ := (&MultiEdit{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
		"path": "a.ts",
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "ALPHA"},
			{"old_string": "beta", "new_string": "BETA"},
		},
	}))
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if got, _, _ := fs.Read("a.ts"); got != "ALPHA\nBETA\n" {
		t.Errorf("edits not all applied: %q", got)
	}
}

// ── protected platform files ──────────────────────────

func TestProtectedFilesRefused(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "package.json", `{"name":"x"}`)

	cases := map[string]func() (ToolResult, error){
		"write": func() (ToolResult, error) {
			return (&WriteFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
				"path": "package.json", "content": "{}",
			}))
		},
		"edit": func() (ToolResult, error) {
			return (&EditFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
				"path": "package.json", "old_string": "x", "new_string": "y",
			}))
		},
		"delete": func() (ToolResult, error) {
			return (&DeleteFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
				"path": "package.json",
			}))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			res, _ := run()
			if !res.IsError || !strings.Contains(res.Content, "BLOCKED") {
				t.Errorf("%s of a protected file was not blocked: %s", name, res.Content)
			}
		})
	}

	// The guard is path-exact: a component legitimately named index.js deep in
	// the tree is application code, not the entrypoint.
	res, _ := (&WriteFile{FS: fs}).Execute(context.Background(), rawArgs(t, map[string]any{
		"path": "components/index.js", "content": "export const x = 1\n",
	}))
	if res.IsError {
		t.Errorf("a nested index.js should be writable, got: %s", res.Content)
	}
}

// ── registry: output cap, unknown tool, error fallback, arg defaulting ──

type stubTool struct {
	name    string
	content string
	err     error
	echo    bool
}

func (s stubTool) Name() string                 { return s.name }
func (s stubTool) Description() string          { return "stub" }
func (s stubTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s stubTool) Execute(_ context.Context, args json.RawMessage) (ToolResult, error) {
	if s.echo {
		return ToolResult{Content: string(args)}, nil
	}
	return ToolResult{Content: s.content}, s.err
}

func TestRegistryExecute(t *testing.T) {
	t.Run("unknown tool is a non-fatal error", func(t *testing.T) {
		r := NewRegistry()
		res, err := r.Execute(context.Background(), "ghost", nil)
		if err != nil {
			t.Fatalf("Execute should not return a Go error for an unknown tool: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Content, "No tool named") {
			t.Errorf("want unknown-tool message, got: %s", res.Content)
		}
	})

	t.Run("oversized output is capped", func(t *testing.T) {
		r := NewRegistry()
		r.Register(stubTool{name: "big", content: strings.Repeat("x", maxToolOutput+500)})
		res, _ := r.Execute(context.Background(), "big", nil)
		if len(res.Content) > maxToolOutput+128 {
			t.Errorf("output not capped: %d bytes", len(res.Content))
		}
		if !strings.Contains(res.Content, "output truncated") {
			t.Errorf("truncation not announced: %q", res.Content[max(0, len(res.Content)-80):])
		}
	})

	t.Run("tool error with empty content falls back to the error text", func(t *testing.T) {
		r := NewRegistry()
		r.Register(stubTool{name: "boom", err: errors.New("kaboom")})
		res, _ := r.Execute(context.Background(), "boom", nil)
		if !res.IsError || res.Content != "kaboom" {
			t.Errorf("want the error surfaced as content, got: is_error=%v content=%q", res.IsError, res.Content)
		}
	})

	t.Run("nil args are defaulted to an empty object", func(t *testing.T) {
		r := NewRegistry()
		r.Register(stubTool{name: "echo", echo: true})
		res, _ := r.Execute(context.Background(), "echo", nil)
		if res.Content != "{}" {
			t.Errorf("nil args not defaulted: %q", res.Content)
		}
	})
}

// List / Names are the cached prefix of every request; they must be sorted so a
// map's iteration order can never invalidate the prompt cache between calls.
func TestRegistryDeterministicOrder(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		r.Register(stubTool{name: n})
	}
	names := r.Names()
	want := []string{"alpha", "bravo", "charlie"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("Names not sorted: %v", names)
		}
	}
	defs := r.List()
	for i := range want {
		if defs[i].Name != want[i] {
			t.Fatalf("List not sorted: %v", defs)
		}
	}
}

// walkSource must descend into source directories but never into dependency or
// build trees — grep and list_files would otherwise drown in node_modules.
func TestWalkSourceSkipsVendorDirs(t *testing.T) {
	fs := newTestFS(t)
	seed(t, fs, "app/index.tsx", "x")
	seed(t, fs, "node_modules/react/index.js", "y")
	if err := os.MkdirAll(filepath.Join(fs.Root(), "node_modules", "react"), 0755); err != nil {
		t.Fatal(err)
	}

	files, err := walkSource(fs.Root(), fs.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(f, "node_modules") {
			t.Errorf("walk descended into node_modules: %v", files)
		}
	}
	var sawApp bool
	for _, f := range files {
		if f == "app/index.tsx" {
			sawApp = true
		}
	}
	if !sawApp {
		t.Errorf("walk missed real source: %v", files)
	}
}
