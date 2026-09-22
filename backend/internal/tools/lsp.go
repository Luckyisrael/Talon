package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/lsp"
)

// ── W5: the language-server senses ──────────────────────────────────
//
// Four read-only tools over internal/lsp: diagnostics (verify gate zero —
// instant, before tsc), references, definition, and the file outline.
// They sit behind an interface + resolver fn field (the StoreAdapter law
// generalized): *lsp.Client satisfies LSPConn structurally, so wiring
// needs no adapter and tests fake the fleet without spawning anything.
// Every failure to reach a server surfaces as the structured
// LSP_UNAVAILABLE error result — the verify ladder records the skip and
// never blocks.

// LSPConn is the read-only language-server surface these tools need —
// plus organize-imports, the one write, applied by the tool through FS.
type LSPConn interface {
	DiagnosticsFor(ctx context.Context, rel string) ([]lsp.Diagnostic, error)
	References(ctx context.Context, rel string, line, col int) ([]lsp.Location, error)
	Definition(ctx context.Context, rel string, line, col int) ([]lsp.Location, error)
	Symbols(ctx context.Context, rel string) ([]lsp.Symbol, error)
	OrganizeImports(ctx context.Context, rel string) ([]lsp.TextEdit, error)
}

// LSPResolve maps a workspace-relative path to a live server for its
// language. Errors should carry the structured LSP_UNAVAILABLE reason —
// the tools surface them verbatim as error results.
type LSPResolve func(ctx context.Context, path string) (LSPConn, error)

func resolveConn(ctx context.Context, r LSPResolve, path string) (LSPConn, error) {
	if r == nil {
		return nil, fmt.Errorf("LSP_UNAVAILABLE: language server not wired (TALONSTUDIO_LSP off?)")
	}
	return r(ctx, path)
}

// maxLSPWorkspaceFiles bounds the workspace sweep: diagnostics open each
// file on the server, so a giant tree would blow the read budget. Past the
// cap the verdict says so — honest truncation, like the map.
const maxLSPWorkspaceFiles = 400

// workspaceLSPFiles lists workspace-relative paths the servers actually
// serve — LangFor's domain (the ts family and Go), deliberately NOT
// IsMappableSource: that predicate is the map/index same-file-set law and
// stays ts-only by design (projectmap.go documents widening it as its own
// coordinated change), while gopls speaks for Go here. The rest of the
// walk law matches the map: skip dirs, symlinks, dotfiles. Lexical order
// for deterministic verdicts.
func workspaceLSPFiles(root string) []string {
	var out []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != root && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if lsp.LangFor(info.Name()) == "" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

// diagVerdict renders diagnostics as the verify contract: severity-1
// errors fail, warnings ride along for context. Lines are tsc-shaped
// (Diagnostic.FormatTsc), so knowledge.NormalizeError and the seeded
// run_typecheck patterns match LSP output with no second rule set.
func diagVerdict(scope string, diags []lsp.Diagnostic) VerifyResult {
	if len(diags) == 0 {
		s := "no diagnostics"
		if scope != "" {
			s = scope + ": no diagnostics"
		}
		return VerifyResult{Passed: true, Summary: s}
	}
	var errs, warns []string
	for _, d := range diags {
		line := d.FormatTsc()
		if d.Severity <= 1 {
			errs = append(errs, line)
		} else {
			warns = append(warns, line)
		}
	}
	summary := fmt.Sprintf("%d error(s)", len(errs))
	if len(warns) > 0 {
		summary += fmt.Sprintf(", %d warning(s)", len(warns))
	}
	if scope != "" {
		summary = scope + ": " + summary
	}
	body := strings.Join(errs, "\n")
	if len(warns) > 0 {
		if body != "" {
			body += "\n"
		}
		body += strings.Join(warns, "\n")
	}
	return VerifyResult{Passed: len(errs) == 0, Errors: body, Summary: summary}
}

// uniqueStrings preserves first-seen order (unavailable notes stay stable).
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ── lsp_diagnostics ─────────────────────────────────────────────────

type LSPDiagnostics struct {
	WorkspacePath string
	Resolve       LSPResolve
}

func (t *LSPDiagnostics) Name() string { return "lsp_diagnostics" }

func (t *LSPDiagnostics) Description() string {
	return "Ask the project's language server for type errors in one file (path) or the whole " +
		"workspace (no path) — instant, runs no tsc. Output is tsc-shaped lines; passed is false " +
		"when errors exist. Use it to check whether your last edit broke anything without paying " +
		"for a full typecheck."
}

func (t *LSPDiagnostics) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative file to diagnose. Omit to diagnose the whole workspace."}
		},
		"required": []
	}`)
}

func (t *LSPDiagnostics) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("bad arguments: %v", err), nil
	}
	if rel := strings.TrimSpace(a.Path); rel != "" {
		conn, err := resolveConn(ctx, t.Resolve, rel)
		if err != nil {
			return errResult("%v", err), nil
		}
		diags, err := conn.DiagnosticsFor(ctx, filepath.ToSlash(rel))
		if err != nil {
			return errResult("diagnostics failed: %v", err), nil
		}
		return marshalVerify(diagVerdict(filepath.ToSlash(rel), diags)), nil
	}

	files := workspaceLSPFiles(t.WorkspacePath)
	truncated := false
	if len(files) > maxLSPWorkspaceFiles {
		files, truncated = files[:maxLSPWorkspaceFiles], true
	}
	if len(files) == 0 {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no served source files"}), nil
	}
	// One server per language class; a class whose server cannot start is
	// noted honestly — and if NO class could run, the tool errors outright,
	// which is how the verify ladder records the skip in SkippedGates.
	var (
		all     []lsp.Diagnostic
		unavail []string
		served  int
	)
	conns := map[string]LSPConn{}
	for _, rel := range files {
		class := lsp.LangFor(rel)
		conn, cached := conns[class]
		if !cached {
			c, err := resolveConn(ctx, t.Resolve, rel)
			if err != nil {
				conns[class] = nil // one resolution attempt per dead class
				unavail = append(unavail, err.Error())
				continue
			}
			conn = c
			conns[class] = conn
		}
		if conn == nil {
			continue
		}
		served++
		diags, err := conn.DiagnosticsFor(ctx, rel)
		if err != nil {
			unavail = append(unavail, "LSP_UNAVAILABLE: "+err.Error())
			continue
		}
		all = append(all, diags...)
	}
	if served == 0 && len(unavail) > 0 {
		return errResult("%s", uniqueStrings(unavail)[0]), nil
	}
	v := diagVerdict("", all)
	if notes := uniqueStrings(unavail); len(notes) > 0 {
		v.Summary += "; note: " + strings.Join(notes, "; ")
	}
	if truncated {
		v.Summary += fmt.Sprintf("; workspace truncated at %d served files", maxLSPWorkspaceFiles)
	}
	return marshalVerify(v), nil
}

// ── lsp_references / lsp_definition ─────────────────────────────────

type LSPReferences struct{ Resolve LSPResolve }

func (t *LSPReferences) Name() string { return "lsp_references" }

func (t *LSPReferences) Description() string {
	return "Find every use of the symbol at path:line:col via the language server — cross-file, " +
		"instant. Positions are 1-based (line 1 is the first line)."
}

func (t *LSPReferences) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative file containing the symbol."},
			"line": {"type": "integer", "description": "1-based line of the symbol."},
			"col":  {"type": "integer", "description": "1-based column of the symbol."}
		},
		"required": ["path", "line", "col"]
	}`)
}

func (t *LSPReferences) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	rel, line, col, res := positionArgs(args)
	if res != nil {
		return *res, nil
	}
	conn, err := resolveConn(ctx, t.Resolve, rel)
	if err != nil {
		return errResult("%v", err), nil
	}
	refs, err := conn.References(ctx, rel, line, col)
	if err != nil {
		return errResult("references failed: %v", err), nil
	}
	if len(refs) == 0 {
		return ToolResult{Content: fmt.Sprintf("no references to %s:%d:%d", rel, line, col)}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d reference(s):\n", len(refs))
	for _, hit := range refs {
		fmt.Fprintf(&b, "%s:%d:%d\n", hit.File, hit.Line, hit.Col)
	}
	return ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}

type LSPDefinition struct{ Resolve LSPResolve }

func (t *LSPDefinition) Name() string { return "lsp_definition" }

func (t *LSPDefinition) Description() string {
	return "Jump to where the symbol at path:line:col is defined, via the language server — " +
		"cross-file, instant. Positions are 1-based."
}

func (t *LSPDefinition) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative file containing the symbol use."},
			"line": {"type": "integer", "description": "1-based line of the use."},
			"col":  {"type": "integer", "description": "1-based column of the use."}
		},
		"required": ["path", "line", "col"]
	}`)
}

func (t *LSPDefinition) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	rel, line, col, res := positionArgs(args)
	if res != nil {
		return *res, nil
	}
	conn, err := resolveConn(ctx, t.Resolve, rel)
	if err != nil {
		return errResult("%v", err), nil
	}
	defs, err := conn.Definition(ctx, rel, line, col)
	if err != nil {
		return errResult("definition failed: %v", err), nil
	}
	if len(defs) == 0 {
		return ToolResult{Content: fmt.Sprintf("no definition found for %s:%d:%d", rel, line, col)}, nil
	}
	var b strings.Builder
	for _, hit := range defs {
		fmt.Fprintf(&b, "%s:%d:%d\n", hit.File, hit.Line, hit.Col)
	}
	return ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// positionArgs parses the shared path/line/col shape. A non-nil result is
// an already-rendered error result the caller returns as-is.
func positionArgs(args json.RawMessage) (rel string, line, col int, res *ToolResult) {
	var a struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Col  int    `json:"col"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		r := errResult("bad arguments: %v", err)
		return "", 0, 0, &r
	}
	rel = filepath.ToSlash(strings.TrimSpace(a.Path))
	if rel == "" || a.Line < 1 || a.Col < 1 {
		r := errResult("positions are 1-based: path, line >= 1 and col >= 1 are required")
		return "", 0, 0, &r
	}
	return rel, a.Line, a.Col, nil
}

// ── lsp_symbol ──────────────────────────────────────────────────────

type LSPSymbol struct{ Resolve LSPResolve }

func (t *LSPSymbol) Name() string { return "lsp_symbol" }

func (t *LSPSymbol) Description() string {
	return "Outline one file — its declarations (functions, classes, …) with kind and 1-based " +
		"line — via the language server. Pass name to filter (case-insensitive substring)."
}

func (t *LSPSymbol) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative file to outline."},
			"name": {"type": "string", "description": "Optional case-insensitive name filter."}
		},
		"required": ["path"]
	}`)
}

func (t *LSPSymbol) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("bad arguments: %v", err), nil
	}
	rel := filepath.ToSlash(strings.TrimSpace(a.Path))
	if rel == "" {
		return errResult("path is required"), nil
	}
	conn, err := resolveConn(ctx, t.Resolve, rel)
	if err != nil {
		return errResult("%v", err), nil
	}
	syms, err := conn.Symbols(ctx, rel)
	if err != nil {
		return errResult("symbols failed: %v", err), nil
	}
	filter := strings.ToLower(strings.TrimSpace(a.Name))
	kept := make([]lsp.Symbol, 0, len(syms))
	for _, s := range syms {
		if filter != "" && !strings.Contains(strings.ToLower(s.Name), filter) {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		if filter != "" {
			return ToolResult{Content: fmt.Sprintf("no symbol matching %q in %s", a.Name, rel)}, nil
		}
		return ToolResult{Content: rel + ": no symbols reported"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d symbol(s) in %s:\n", len(kept), rel)
	for _, s := range kept {
		fmt.Fprintf(&b, "%d %s %s\n", s.Line, lsp.KindName(s.Kind), s.Name)
	}
	return ToolResult{Content: strings.TrimRight(b.String(), "\n")}, nil
}
