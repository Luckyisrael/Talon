package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// UnavailableError is the structured degradation the plan demands:
// "LSP_UNAVAILABLE", never a block — fail-open-availability, the split
// sandbox uses. Missing binary, dead process, no language for the path:
// all land here and surface the same way upstream.
type UnavailableError struct{ Reason string }

func (e *UnavailableError) Error() string { return "LSP_UNAVAILABLE: " + e.Reason }

// Unavailable builds the structured degradation.
func Unavailable(reason string) error { return &UnavailableError{Reason: reason} }

// IsUnavailable reports whether err is the structured degradation.
func IsUnavailable(err error) bool {
	var u *UnavailableError
	return errors.As(err, &u)
}

// Diagnostic is one publishDiagnostics entry, flattened for tools and the
// verify gate: 1-based line/col (the model's world), LSP severity kept.
type Diagnostic struct {
	File     string // workspace-relative, or the raw URI when outside it
	Line     int    // 1-based
	Col      int    // 1-based
	Severity int    // 1=error 2=warning 3=info 4=hint
	Code     string // "TS2322", an eslint rule name, …
	Message  string
}

// Location is one reference/definition hit, workspace-relative.
type Location struct {
	File string
	Line int
	Col  int
}

// Symbol is one flat document symbol.
type Symbol struct {
	Name string
	Kind int // raw LSP SymbolKind
	Line int // 1-based
}

// severityName maps LSP severities to the words tsc prints.
func severityName(s int) string {
	switch s {
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "error"
	}
}

// FormatTsc renders a diagnostic in tsc's own line shape —
// path(line,col): error TS2322: message — so knowledge.NormalizeError and
// every run_typecheck pattern in the failure index match LSP diagnostics
// with NO second rule set. Non-numeric codes (eslint rule names) keep their
// text after the severity word.
func (d Diagnostic) FormatTsc() string {
	head := fmt.Sprintf("%s(%d,%d): %s", d.File, d.Line, d.Col, severityName(d.Severity))
	code := strings.TrimSpace(d.Code)
	if n, err := strconv.Atoi(code); err == nil {
		code = "TS" + strconv.Itoa(n)
	} else if code != "" {
		code = "TS" + strings.TrimLeft(strings.ToUpper(code), "TS")
		if _, err := strconv.Atoi(code[2:]); err != nil {
			code = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(d.Code)), "TS"))
		}
	}
	if code != "" {
		head += " " + code
	}
	return head + ": " + d.Message
}

// Client owns one language-server process and the documents opened on it.
type Client struct {
	rpc      *rpcConn
	cmd      *exec.Cmd
	root     string
	exited   chan struct{}
	closeOne sync.Once

	mu          sync.Mutex
	diags       map[string][]Diagnostic
	diagVer     map[string]int
	diagChanged chan struct{}
	opened      map[string]bool
}

// startClient launches the server, completes the initialize handshake, and
// starts the notification collector. Kill-group lifetime mirrors
// runVerifyCmd (Setpgid + group SIGKILL): a wedged server cannot outlive
// its client. Every launch failure is structured Unavailable.
func startClient(ctx context.Context, root string, argv []string) (*Client, error) {
	if len(argv) == 0 {
		return nil, Unavailable("no language server command (set .talon/config.json \"lsp\" or install a server)")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, Unavailable(fmt.Sprintf("%s not found — install it or set .talon/config.json \"lsp\"", argv[0]))
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	// The server leads its own process group so hardClose can kill the whole
	// member tree at once (tsserver spawns children). No cmd.Cancel: os/exec
	// only accepts it on CommandContext commands, and this process's lifetime
	// is owned by Close/hardClose — not by a request context that will end.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "CI=1", "NO_COLOR=1", "FORCE_COLOR=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, Unavailable("stdin pipe: " + err.Error())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, Unavailable("stdout pipe: " + err.Error())
	}
	if err := cmd.Start(); err != nil {
		return nil, Unavailable(fmt.Sprintf("cannot start %s: %v", argv[0], err))
	}

	c := &Client{
		cmd:         cmd,
		root:        root,
		exited:      make(chan struct{}),
		diags:       map[string][]Diagnostic{},
		diagVer:     map[string]int{},
		diagChanged: make(chan struct{}),
		opened:      map[string]bool{},
	}
	c.rpc = newRPCConn(stdout, stdin, c.onNotification)
	go func() {
		_ = cmd.Wait() // reap; EOF on stdout fails the connection for us
		close(c.exited)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   absURI(root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"publishDiagnostics": map[string]any{"relatedInformation": false},
			},
		},
	}
	if _, err := c.rpc.request(initCtx, "initialize", params); err != nil {
		c.hardClose()
		if IsUnavailable(err) {
			return nil, err
		}
		reason := fmt.Sprintf("initialize failed: %v", err)
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			reason += " (" + lastLines(tail, 3) + ")"
		}
		return nil, Unavailable(reason)
	}
	if err := c.rpc.notify("initialized", map[string]any{}); err != nil {
		c.hardClose()
		return nil, Unavailable("initialized notify failed: " + err.Error())
	}
	return c, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// uriFor maps a workspace-relative path to its file URI.
func (c *Client) uriFor(rel string) string {
	return absURI(filepath.Join(c.root, rel))
}

func absURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// relForURI maps a file URI back to a workspace-relative path. URIs outside
// the workspace (dependency files) come back ok=false with the raw URI.
func (c *Client) relForURI(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri, false
	}
	rel, err := filepath.Rel(c.root, filepath.FromSlash(u.Path))
	if err != nil {
		return uri, false
	}
	return filepath.ToSlash(rel), true
}

func languageIDFor(rel string) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".go":
		return "go"
	default:
		return ""
	}
}

// onNotification folds publishDiagnostics into per-file state and wakes
// every waiter. A publish bumps the file's version; OpenFile records the
// pre-open version so DiagnosticsFor waits for the publish that THIS open
// provokes, not a stale one.
func (c *Client) onNotification(method string, params json.RawMessage) {
	if method != "textDocument/publishDiagnostics" {
		return
	}
	var p struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
			} `json:"range"`
			Severity int             `json:"severity"`
			Code     json.RawMessage `json:"code"`
			Message  string          `json:"message"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	rel, _ := c.relForURI(p.URI)
	out := make([]Diagnostic, 0, len(p.Diagnostics))
	for _, d := range p.Diagnostics {
		code := strings.Trim(strings.TrimSpace(string(d.Code)), `"`)
		sev := d.Severity
		if sev == 0 {
			sev = 1 // LSP: absent severity means Error
		}
		out = append(out, Diagnostic{
			File:     rel,
			Line:     d.Range.Start.Line + 1,
			Col:      d.Range.Start.Character + 1,
			Severity: sev,
			Code:     code,
			Message:  d.Message,
		})
	}
	c.mu.Lock()
	c.diags[rel] = out
	c.diagVer[rel]++
	close(c.diagChanged)
	c.diagChanged = make(chan struct{})
	c.mu.Unlock()
}

// OpenFile (re)declares a file on the server with its CURRENT bytes:
// didClose-then-didOpen, so a file rewritten since the last open never
// serves stale diagnostics. Returns the version counter to wait against.
func (c *Client) OpenFile(rel string) (int, error) {
	src, err := os.ReadFile(filepath.Join(c.root, rel))
	if err != nil {
		return 0, err
	}
	uri := c.uriFor(rel)
	c.mu.Lock()
	wasOpen := c.opened[rel]
	snap := c.diagVer[rel]
	c.mu.Unlock()
	if wasOpen {
		_ = c.rpc.notify("textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": uri},
		})
	}
	lid := languageIDFor(rel)
	if lid == "" {
		lid = "plaintext"
	}
	if err := c.rpc.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": lid,
			"version":    1,
			"text":       string(src),
		},
	}); err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.opened[rel] = true
	c.mu.Unlock()
	return snap, nil
}

// DiagnosticsFor opens the file and waits for the publish it provokes —
// ctx expiry is an honest failure (a silent server is not a clean bill of
// health), which upstream maps to the same unavailable path as a dead one.
func (c *Client) DiagnosticsFor(ctx context.Context, rel string) ([]Diagnostic, error) {
	snap, err := c.OpenFile(rel)
	if err != nil {
		return nil, err
	}
	return c.waitDiagnostics(ctx, rel, snap)
}

func (c *Client) waitDiagnostics(ctx context.Context, rel string, after int) ([]Diagnostic, error) {
	for {
		c.mu.Lock()
		if c.diagVer[rel] > after {
			d := append([]Diagnostic(nil), c.diags[rel]...)
			c.mu.Unlock()
			return d, nil
		}
		changed := c.diagChanged
		c.mu.Unlock()
		select {
		case <-changed:
		case <-c.exited:
			return nil, Unavailable("server exited while waiting for diagnostics")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// References resolves textDocument/references. line/col are 1-based — the
// model's world; LSP positions are 0-based and converted here.
func (c *Client) References(ctx context.Context, rel string, line, col int) ([]Location, error) {
	if _, err := c.OpenFile(rel); err != nil {
		return nil, err
	}
	raw, err := c.rpc.request(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.uriFor(rel)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
		"context":      map[string]any{"includeDeclaration": true},
	})
	if err != nil {
		return nil, err
	}
	return c.parseLocations(raw)
}

// Definition resolves textDocument/definition — a single Location or a
// list, per the spec's union.
func (c *Client) Definition(ctx context.Context, rel string, line, col int) ([]Location, error) {
	if _, err := c.OpenFile(rel); err != nil {
		return nil, err
	}
	raw, err := c.rpc.request(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": c.uriFor(rel)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	})
	if err != nil {
		return nil, err
	}
	return c.parseLocations(raw)
}

type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
}

func (c *Client) parseLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one lspLocation
	if err := json.Unmarshal(raw, &one); err == nil && one.URI != "" {
		return c.toLocations([]lspLocation{one}), nil
	}
	var many []lspLocation
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, fmt.Errorf("lsp: unreadable locations: %w", err)
	}
	return c.toLocations(many), nil
}

func (c *Client) toLocations(in []lspLocation) []Location {
	out := make([]Location, 0, len(in))
	for _, l := range in {
		rel, _ := c.relForURI(l.URI)
		out = append(out, Location{File: rel, Line: l.Range.Start.Line + 1, Col: l.Range.Start.Character + 1})
	}
	return out
}

// Symbols flattens textDocument/documentSymbol — SymbolInformation (flat)
// or DocumentSymbol (hierarchical, children expanded) per the spec union.
func (c *Client) Symbols(ctx context.Context, rel string) ([]Symbol, error) {
	if _, err := c.OpenFile(rel); err != nil {
		return nil, err
	}
	raw, err := c.rpc.request(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": c.uriFor(rel)},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("lsp: unreadable symbols: %w", err)
	}
	var out []Symbol
	for _, e := range entries {
		out = append(out, c.parseSymbol(e)...)
	}
	return out, nil
}

func (c *Client) parseSymbol(raw json.RawMessage) []Symbol {
	// Flat: SymbolInformation carries location; hierarchical DocumentSymbol
	// carries selectionRange and children.
	var flat struct {
		Name     string `json:"name"`
		Kind     int    `json:"kind"`
		Location struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line int `json:"line"`
				} `json:"start"`
			} `json:"range"`
		} `json:"location"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && flat.Location.URI != "" {
		rel, _ := c.relForURI(flat.Location.URI)
		_ = rel // location's file belongs to the symbol's own file — line is what tools print
		return []Symbol{{Name: flat.Name, Kind: flat.Kind, Line: flat.Location.Range.Start.Line + 1}}
	}
	var hier struct {
		Name           string `json:"name"`
		Kind           int    `json:"kind"`
		SelectionRange struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"selectionRange"`
		Range struct {
			Start struct {
				Line int `json:"line"`
			} `json:"start"`
		} `json:"range"`
		Children []json.RawMessage `json:"children"`
	}
	if err := json.Unmarshal(raw, &hier); err != nil {
		return nil
	}
	line := hier.SelectionRange.Start.Line
	if line == 0 {
		line = hier.Range.Start.Line
	}
	out := []Symbol{{Name: hier.Name, Kind: hier.Kind, Line: line + 1}}
	for _, ch := range hier.Children {
		out = append(out, c.parseSymbol(ch)...)
	}
	return out
}

// OrganizeImports asks the server for source.organizeImports on one file
// and returns the edits to apply — it does NOT write. Applying belongs to
// the tool (with FS, inside the workspace), keeping the client's read-side
// senses pure and the write visible to the harness's journal.
func (c *Client) OrganizeImports(ctx context.Context, rel string) ([]TextEdit, error) {
	if _, err := c.OpenFile(rel); err != nil {
		return nil, err
	}
	// Whole-document range: gopls wants a real one, tsserver ignores it —
	// (0,0)-(lastline,0) satisfies both. Counted from the bytes on disk so
	// the server sees the document the tool is about to edit.
	lines := 1
	if content, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel))); err == nil {
		lines = strings.Count(string(content), "\n") + 1
	}
	uri := c.uriFor(rel)
	raw, err := c.rpc.request(ctx, "textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": lines, "character": 0},
		},
		"context": map[string]any{
			"diagnostics": []any{},
			"only":        []any{"source.organizeImports"},
		},
	})
	if err != nil {
		return nil, err
	}
	return parseCodeActions(raw, uri)
}

// parseCodeActions flattens CodeAction edits for one URI into
// tool-surface TextEdits (both axes +1 from LSP's 0-based). Command-only
// actions carry no edit — nothing to apply, skipped. An answer keyed for
// another URI is not ours to apply; an absent/null result is an empty
// edit list, which callers read as "already organized".
func parseCodeActions(raw json.RawMessage, forURI string) ([]TextEdit, error) {
	var actions []struct {
		Edit *struct {
			Changes map[string][]struct {
				Range struct {
					Start struct{ Line, Character int } `json:"start"`
					End   struct{ Line, Character int } `json:"end"`
				} `json:"range"`
				NewText string `json:"newText"`
			} `json:"changes"`
		} `json:"edit"`
	}
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("code actions: %w", err)
	}
	var out []TextEdit
	for _, a := range actions {
		if a.Edit == nil {
			continue
		}
		for _, e := range a.Edit.Changes[forURI] {
			out = append(out, TextEdit{
				StartLine: e.Range.Start.Line + 1,
				StartCol:  e.Range.Start.Character + 1,
				EndLine:   e.Range.End.Line + 1,
				EndCol:    e.Range.End.Character + 1,
				NewText:   e.NewText,
			})
		}
	}
	return out, nil
}

// KindName maps LSP SymbolKinds (the spec's numbering) to words a model
// reads fast; unknown kinds print as kind-N rather than guessing.
func KindName(k int) string {
	switch k {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "boolean"
	case 18:
		return "array"
	case 19:
		return "object"
	case 22:
		return "enum-member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type-parameter"
	default:
		return fmt.Sprintf("kind-%d", k)
	}
}

// Alive reports whether the server process is still with us.
func (c *Client) Alive() bool {
	select {
	case <-c.exited:
		return false
	default:
		return true
	}
}

// Close shuts the server down politely (shutdown → exit), then guarantees
// the process group is gone either way. Safe to call more than once.
func (c *Client) Close() error {
	c.closeOne.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = c.rpc.request(ctx, "shutdown", nil)
		_ = c.rpc.notify("exit", nil)
		select {
		case <-c.exited:
		case <-time.After(1500 * time.Millisecond):
		}
		c.hardClose()
	})
	return nil
}

// hardClose guarantees the kill group lands — the polite path above is a
// courtesy, this is the law (no server outlives its client).
func (c *Client) hardClose() {
	c.rpc.fail(errors.New("client closing"))
	if c.cmd.Process != nil {
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}
	select {
	case <-c.exited:
	case <-time.After(2 * time.Second):
	}
}
