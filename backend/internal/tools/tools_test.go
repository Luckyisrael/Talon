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

// ── capture_screenshot: honest visual verification ────

// fakeVision stands in for the model. It records what it was handed so a test
// can prove the captured image actually reached it — the bug being fixed is that
// the old tool captured a screenshot and threw the pixels away.
type fakeVision struct {
	verdict *VisionVerdict
	err     error

	calls    int
	gotBytes []byte
	gotWant  string
}

func (f *fakeVision) VisionCheck(_ context.Context, png []byte, intended string) (*VisionVerdict, error) {
	f.calls++
	f.gotBytes = png
	f.gotWant = intended
	if f.err != nil {
		return nil, f.err
	}
	return f.verdict, nil
}

func TestCaptureScreenshotVision(t *testing.T) {
	png := []byte("\x89PNG-fake-bytes")
	// Skip the real adb call; feed a fixed image in so the analysis path runs
	// without a device attached.
	fixedCapture := func([]byte) func(context.Context) ([]byte, error) {
		return func(context.Context) ([]byte, error) { return png, nil }
	}
	decode := func(t *testing.T, res ToolResult) ScreenshotResult {
		t.Helper()
		var sr ScreenshotResult
		if err := json.Unmarshal([]byte(res.Content), &sr); err != nil {
			t.Fatalf("result is not ScreenshotResult JSON: %v (%q)", err, res.Content)
		}
		return sr
	}

	t.Run("no vision configured: captured, not analyzed, no invented verdict", func(t *testing.T) {
		cs := &CaptureScreenshot{capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{"intended_screen": "home"}))
		sr := decode(t, res)
		if res.IsError || !sr.Success || sr.Analyzed {
			t.Fatalf("want captured-but-not-analyzed, got %+v (is_error=%v)", sr, res.IsError)
		}
		if sr.Bytes != len(png) {
			t.Errorf("captured byte count not reported: got %d want %d", sr.Bytes, len(png))
		}
	})

	t.Run("vision set but no intended_screen: not analyzed, model left alone", func(t *testing.T) {
		fv := &fakeVision{verdict: &VisionVerdict{Matches: true}}
		cs := &CaptureScreenshot{Vision: fv, capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{}))
		if sr := decode(t, res); sr.Analyzed || res.IsError {
			t.Fatalf("want not-analyzed with nothing to check against, got %+v", sr)
		}
		if fv.calls != 0 {
			t.Errorf("vision was called with no target to compare against")
		}
	})

	t.Run("match: captured bytes reach the checker and the verdict passes", func(t *testing.T) {
		fv := &fakeVision{verdict: &VisionVerdict{Matches: true, Reason: "looks right"}}
		cs := &CaptureScreenshot{Vision: fv, capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{"intended_screen": "home"}))
		sr := decode(t, res)
		if res.IsError || !sr.Analyzed || !sr.Matches {
			t.Fatalf("want a passing analyzed verdict, got %+v (is_error=%v)", sr, res.IsError)
		}
		// The whole point of the fix: the image used to be discarded. Prove the
		// exact captured bytes and the target both reached the model.
		if string(fv.gotBytes) != string(png) {
			t.Errorf("captured image did not reach the checker: got %q", fv.gotBytes)
		}
		if fv.gotWant != "home" {
			t.Errorf("intended_screen not forwarded: got %q", fv.gotWant)
		}
	})

	t.Run("mismatch is surfaced as is_error so the loop keeps working", func(t *testing.T) {
		fv := &fakeVision{verdict: &VisionVerdict{Matches: false, Reason: "blank screen"}}
		cs := &CaptureScreenshot{Vision: fv, capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{"intended_screen": "home"}))
		sr := decode(t, res)
		if !res.IsError || !sr.Analyzed || sr.Matches {
			t.Fatalf("want an analyzed mismatch flagged is_error, got %+v (is_error=%v)", sr, res.IsError)
		}
	})

	t.Run("a visible error is an error even when the layout matches", func(t *testing.T) {
		fv := &fakeVision{verdict: &VisionVerdict{Matches: true, HasError: true, ErrorMsg: "red box"}}
		cs := &CaptureScreenshot{Vision: fv, capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{"intended_screen": "home"}))
		if sr := decode(t, res); !res.IsError || !sr.HasError {
			t.Fatalf("want a visible-error verdict flagged is_error, got %+v (is_error=%v)", sr, res.IsError)
		}
	})

	t.Run("a failed analysis is neither a pass nor a fail", func(t *testing.T) {
		fv := &fakeVision{err: errors.New("model timeout")}
		cs := &CaptureScreenshot{Vision: fv, capture: fixedCapture(png)}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{"intended_screen": "home"}))
		sr := decode(t, res)
		if res.IsError || sr.Analyzed {
			t.Fatalf("a failed analysis must not read as a screen verdict, got %+v (is_error=%v)", sr, res.IsError)
		}
		if !strings.Contains(sr.Reason, "analysis failed") {
			t.Errorf("reason should explain the analysis failure, got %q", sr.Reason)
		}
	})

	t.Run("capture failure is an error", func(t *testing.T) {
		cs := &CaptureScreenshot{capture: func(context.Context) ([]byte, error) {
			return nil, errors.New("no device")
		}}
		res, _ := cs.Execute(context.Background(), rawArgs(t, map[string]any{}))
		if sr := decode(t, res); !res.IsError || sr.Success {
			t.Fatalf("want a failed capture, got %+v (is_error=%v)", sr, res.IsError)
		}
	})
}

// ── read_skill ────────────────────────────────────────

// stubLibrary stands in for the knowledge registry so the tool can be tested
// without embedding real content.
type stubLibrary struct{ bodies map[string]string }

func (s stubLibrary) SkillBody(name string) (string, bool) { b, ok := s.bodies[name]; return b, ok }
func (s stubLibrary) SkillNames() []string {
	names := make([]string, 0, len(s.bodies))
	for n := range s.bodies {
		names = append(names, n)
	}
	return names
}

func TestReadSkill(t *testing.T) {
	rs := &ReadSkill{Library: stubLibrary{bodies: map[string]string{"wallet-mwa": "MWA body text"}}}

	t.Run("known skill returns its body verbatim", func(t *testing.T) {
		res, _ := rs.Execute(context.Background(), rawArgs(t, map[string]any{"name": "wallet-mwa"}))
		if res.IsError || res.Content != "MWA body text" {
			t.Fatalf("want the skill body, got is_error=%v content=%q", res.IsError, res.Content)
		}
	})

	t.Run("unknown skill errors and lists what exists", func(t *testing.T) {
		res, _ := rs.Execute(context.Background(), rawArgs(t, map[string]any{"name": "ghost"}))
		if !res.IsError || !strings.Contains(res.Content, "wallet-mwa") {
			t.Errorf("want a not-found error naming available skills, got: %s", res.Content)
		}
	})

	t.Run("blank name is an error, not a lookup", func(t *testing.T) {
		res, _ := rs.Execute(context.Background(), rawArgs(t, map[string]any{"name": "   "}))
		if !res.IsError {
			t.Errorf("want an error for a blank name, got: %s", res.Content)
		}
	})

	t.Run("read_skill is read-only", func(t *testing.T) {
		if !rs.ReadOnly() {
			t.Error("read_skill must be read-only so it can run alongside other reads")
		}
	})

	t.Run("nil library degrades instead of panicking", func(t *testing.T) {
		res, _ := (&ReadSkill{}).Execute(context.Background(), rawArgs(t, map[string]any{"name": "x"}))
		if !res.IsError {
			t.Errorf("want a clean error with no library, got: %s", res.Content)
		}
	})
}
