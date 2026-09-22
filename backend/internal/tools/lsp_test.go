package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/lsp"
)

// The lsp_* tests run against fakes behind the LSPResolve seam — no
// server, no spawn. The real server round-trip is pinned in internal/lsp's
// own tests (fake server = test-binary re-exec); what matters here is the
// tool contract: verdict shape for the verify ladder, tsc-shaped lines for
// the failure index, 1-based positions at the surface, and honest
// degradation when a server cannot run.

type fakeLSP struct {
	diags map[string][]lsp.Diagnostic
	refs  []lsp.Location
	defs  []lsp.Location
	syms  []lsp.Symbol
	edits []lsp.TextEdit
}

func (f *fakeLSP) DiagnosticsFor(_ context.Context, rel string) ([]lsp.Diagnostic, error) {
	return f.diags[rel], nil
}

func (f *fakeLSP) References(_ context.Context, _ string, _, _ int) ([]lsp.Location, error) {
	return f.refs, nil
}

func (f *fakeLSP) Definition(_ context.Context, _ string, _, _ int) ([]lsp.Location, error) {
	return f.defs, nil
}

func (f *fakeLSP) Symbols(_ context.Context, _ string) ([]lsp.Symbol, error) {
	return f.syms, nil
}

func (f *fakeLSP) OrganizeImports(_ context.Context, _ string) ([]lsp.TextEdit, error) {
	return f.edits, nil
}

// resolveByClass routes by the fleet's language class, so a workspace
// sweep exercises per-class resolution the way the real closure does.
func resolveByClass(byClass map[string]LSPConn, errs map[string]error) LSPResolve {
	return func(_ context.Context, path string) (LSPConn, error) {
		class := lsp.LangFor(path)
		if errs != nil {
			if err, ok := errs[class]; ok {
				return nil, err
			}
		}
		if c, ok := byClass[class]; ok {
			return c, nil
		}
		return nil, fmt.Errorf("LSP_UNAVAILABLE: no server for %q", class)
	}
}

// lspTool is the slice of the tool contract these tests exercise — a local
// interface so the tests do not depend on the registry's interface name.
type lspTool interface {
	Execute(context.Context, json.RawMessage) (ToolResult, error)
}

func lspExec(t *testing.T, tool lspTool, args string) ToolResult {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func lspVerdict(t *testing.T, res ToolResult) VerifyResult {
	t.Helper()
	var v VerifyResult
	if err := json.Unmarshal([]byte(res.Content), &v); err != nil {
		t.Fatalf("verdict JSON: %v (%q)", err, res.Content)
	}
	return v
}

// TestLSPDiagnosticsFileScope pins gate-zero's core: a severity-1
// diagnostic fails with a tsc-shaped error line (so the failure index's
// run_typecheck patterns match it), a clean file passes.
func TestLSPDiagnosticsFileScope(t *testing.T) {
	fake := &fakeLSP{diags: map[string][]lsp.Diagnostic{
		"src/app.ts": {{File: "src/app.ts", Line: 3, Col: 5, Severity: 1, Code: "2322",
			Message: "Type 'string' is not assignable to type 'number'."}},
	}}
	tool := &LSPDiagnostics{
		WorkspacePath: "/nowhere",
		Resolve:       resolveByClass(map[string]LSPConn{"ts": fake}, nil),
	}

	res := lspExec(t, tool, `{"path":"src/app.ts"}`)
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	v := lspVerdict(t, res)
	if v.Passed {
		t.Errorf("severity-1 diagnostic must fail the verdict: %+v", v)
	}
	if !strings.Contains(v.Errors, "src/app.ts(3,5): error TS2322:") {
		t.Errorf("errors = %q, want the tsc-shaped line", v.Errors)
	}
	if v.Summary == "" {
		t.Error("summary must not be empty")
	}

	res = lspExec(t, tool, `{"path":"src/clean.ts"}`)
	v = lspVerdict(t, res)
	if !v.Passed || v.Errors != "" {
		t.Errorf("clean file: %+v", v)
	}
}

// TestLSPDiagnosticsUnavailableIsSkippable pins the degradation both the
// model and the ladder see: nil resolver (flag off at wiring) and a dead
// server class both come back as error results — runGate reads isError as
// "unavailable" and records the skip in SkippedGates instead of failing
// the build. An empty workspace is not a failure.
func TestLSPDiagnosticsUnavailableIsSkippable(t *testing.T) {
	tool := &LSPDiagnostics{WorkspacePath: "/nowhere"}
	res := lspExec(t, tool, `{"path":"a.ts"}`)
	if !res.IsError || !strings.Contains(res.Content, "LSP_UNAVAILABLE") {
		t.Fatalf("res = %+v, want structured unavailable error result", res)
	}

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "app.ts"), []byte("export {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dead := &LSPDiagnostics{
		WorkspacePath: ws,
		Resolve: resolveByClass(nil, map[string]error{
			"ts": fmt.Errorf("LSP_UNAVAILABLE: server refused to start"),
		}),
	}
	res = lspExec(t, dead, `{}`)
	if !res.IsError || !strings.Contains(res.Content, "LSP_UNAVAILABLE") {
		t.Fatalf("res = %+v, want unavailable when no class could run", res)
	}

	empty := &LSPDiagnostics{WorkspacePath: t.TempDir()}
	v := lspVerdict(t, lspExec(t, empty, `{}`))
	if !v.Passed {
		t.Errorf("empty workspace must pass, not fail: %+v", v)
	}
}

// TestLSPDiagnosticsWorkspaceSweep pins the whole-workspace scope gate
// zero calls with {}: planted error fails the verdict, an unavailable
// class is a note rather than a dead sweep, and the walk keeps the map's
// law (node_modules and dotfiles skipped).
func TestLSPDiagnosticsWorkspaceSweep(t *testing.T) {
	ws := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/app.ts", "export {}\n")
	write("src/main.go", "package main\n")
	write("node_modules/evil.ts", "x")
	write(".hidden.ts", "x")

	tsFake := &fakeLSP{diags: map[string][]lsp.Diagnostic{
		"src/app.ts": {{File: "src/app.ts", Line: 1, Col: 1, Severity: 1, Code: "2345", Message: "boom"}},
	}}
	tool := &LSPDiagnostics{WorkspacePath: ws, Resolve: resolveByClass(
		map[string]LSPConn{"ts": tsFake},
		map[string]error{"go": fmt.Errorf("LSP_UNAVAILABLE: gopls not found")},
	)}

	res := lspExec(t, tool, `{}`)
	if res.IsError {
		t.Fatalf("partial sweep must not error: %s", res.Content)
	}
	v := lspVerdict(t, res)
	if v.Passed {
		t.Errorf("planted error must fail the sweep: %+v", v)
	}
	if !strings.Contains(v.Summary, "LSP_UNAVAILABLE: gopls not found") {
		t.Errorf("summary must carry the unavailable note: %q", v.Summary)
	}
	if strings.Contains(v.Errors, "node_modules") || strings.Contains(v.Errors, ".hidden.ts") {
		t.Errorf("walk must skip node_modules and dotfiles: %q", v.Errors)
	}
}

// TestWorkspaceLSPFilesServedOnlyAndSorted pins the sweep's file set: only
// the fleet's served languages, lexical order for deterministic verdicts.
func TestWorkspaceLSPFilesServedOnlyAndSorted(t *testing.T) {
	ws := t.TempDir()
	for _, rel := range []string{"b.ts", "a.go", "notes.md", "icon.svg", "sub/c.tsx"} {
		p := filepath.Join(ws, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := workspaceLSPFiles(ws)
	want := []string{"a.go", "b.ts", "sub/c.tsx"}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("files = %v, want %v", got, want)
			break
		}
	}
}

// TestLSPReferencesDefinitionAndPositionValidation pins the navigation
// senses: hits render as path:line:col (cross-file), zero hits is an
// honest message rather than an error, and 0-based or missing positions
// are refused loudly — the tool surface promises 1-based.
func TestLSPReferencesDefinitionAndPositionValidation(t *testing.T) {
	fake := &fakeLSP{
		refs: []lsp.Location{
			{File: "src/app.ts", Line: 1, Col: 17},
			{File: "src/other.ts", Line: 5, Col: 1},
		},
		defs: []lsp.Location{{File: "src/app.ts", Line: 8, Col: 1}},
	}
	resolve := resolveByClass(map[string]LSPConn{"ts": fake}, nil)

	refs := &LSPReferences{Resolve: resolve}
	res := lspExec(t, refs, `{"path":"src/app.ts","line":1,"col":17}`)
	if res.IsError {
		t.Fatalf("refs errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "2 reference(s)") ||
		!strings.Contains(res.Content, "src/app.ts:1:17") ||
		!strings.Contains(res.Content, "src/other.ts:5:1") {
		t.Errorf("refs = %q", res.Content)
	}

	fake.refs = nil
	res = lspExec(t, refs, `{"path":"src/app.ts","line":1,"col":17}`)
	if res.IsError || !strings.Contains(res.Content, "no references") {
		t.Errorf("zero hits: %+v", res)
	}

	def := &LSPDefinition{Resolve: resolve}
	res = lspExec(t, def, `{"path":"src/app.ts","line":1,"col":17}`)
	if res.IsError || !strings.Contains(res.Content, "src/app.ts:8:1") {
		t.Errorf("def = %+v", res)
	}

	for _, args := range []string{
		`{"path":"src/app.ts","line":0,"col":1}`,
		`{"path":"src/app.ts","line":1}`,
		`{}`,
	} {
		res = lspExec(t, refs, args)
		if !res.IsError || !strings.Contains(res.Content, "1-based") {
			t.Errorf("args %s → %+v, want 1-based refusal", args, res)
		}
	}
}

// TestLSPSymbolOutline pins the outline: spec kind words at 1-based lines,
// case-insensitive filtering, no-match as an honest message.
func TestLSPSymbolOutline(t *testing.T) {
	fake := &fakeLSP{syms: []lsp.Symbol{
		{Name: "boot", Kind: 12, Line: 1},
		{Name: "AppRoot", Kind: 5, Line: 11},
		{Name: "helper", Kind: 6, Line: 20},
	}}
	tool := &LSPSymbol{Resolve: resolveByClass(map[string]LSPConn{"ts": fake}, nil)}

	res := lspExec(t, tool, `{"path":"src/app.ts"}`)
	if res.IsError {
		t.Fatalf("outline errored: %s", res.Content)
	}
	for _, want := range []string{
		"3 symbol(s) in src/app.ts",
		"1 function boot",
		"11 class AppRoot",
		"20 method helper",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("outline missing %q:\n%s", want, res.Content)
		}
	}

	res = lspExec(t, tool, `{"path":"src/app.ts","name":"app"}`)
	if res.IsError || !strings.Contains(res.Content, "1 symbol(s)") ||
		!strings.Contains(res.Content, "AppRoot") {
		t.Errorf("filter = %+v", res)
	}

	res = lspExec(t, tool, `{"path":"src/app.ts","name":"zzz"}`)
	if res.IsError || !strings.Contains(res.Content, "no symbol matching") {
		t.Errorf("no-match = %+v", res)
	}
}

// TestDiagVerdictWarningsDoNotFail pins the severity law: warnings ride
// along in Errors for context but never flip passed — matching tsc's own
// gate, which fails only on errors.
func TestDiagVerdictWarningsDoNotFail(t *testing.T) {
	v := diagVerdict("", []lsp.Diagnostic{
		{File: "a.ts", Line: 1, Col: 1, Severity: 2, Code: "6133", Message: "'x' is declared but never read."},
	})
	if !v.Passed {
		t.Errorf("warnings must not fail the verdict: %+v", v)
	}
	if !strings.Contains(v.Errors, "TS6133") {
		t.Errorf("warnings still ride along in Errors: %q", v.Errors)
	}
	if !strings.Contains(v.Summary, "1 warning(s)") {
		t.Errorf("summary counts warnings: %q", v.Summary)
	}

	v = diagVerdict("", nil)
	if !v.Passed || v.Summary != "no diagnostics" {
		t.Errorf("empty diagnostics: %+v", v)
	}
}
