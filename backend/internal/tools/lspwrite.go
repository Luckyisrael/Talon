package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/lsp"
)

// ── W5.4: lsp_organize_imports + the formatter key ─────────────────────
//
// organize-imports is the ONE write any lsp_* tool performs. It goes
// through FS like every other write — journaled, snapshot-restorable,
// workspace-scoped — and the server only ever proposes; the bytes are
// applied here, clamped, or not at all.

// LSPOrganizeImports asks the language server to organize one file's
// imports (sort, dedupe, drop unused) and applies the returned edits.
type LSPOrganizeImports struct {
	FS            *FS
	WorkspacePath string
	Resolve       LSPResolve
}

func (t *LSPOrganizeImports) Name() string { return "lsp_organize_imports" }

func (t *LSPOrganizeImports) Description() string {
	return "Ask the language server to organize one file's imports (sort, dedupe, drop the " +
		"unused ones) and apply the change through the workspace journal. Returns what " +
		"changed; a file already organized comes back untouched. Different from run_prettier: " +
		"this reorders imports, that formats the whole file."
}

func (t *LSPOrganizeImports) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Workspace-relative file to organize."}
		},
		"required": ["path"]
	}`)
}

func (t *LSPOrganizeImports) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("bad arguments: %v", err), nil
	}
	rel := filepath.ToSlash(strings.TrimSpace(a.Path))
	if rel == "" {
		return errResult("path is required"), nil
	}
	if t.FS == nil {
		return errResult("LSP_UNAVAILABLE: workspace filesystem not wired"), nil
	}
	conn, err := resolveConn(ctx, t.Resolve, rel)
	if err != nil {
		return errResult("%v", err), nil
	}
	edits, err := conn.OrganizeImports(ctx, rel)
	if err != nil {
		return errResult("organize imports failed: %v", err), nil
	}
	if len(edits) == 0 {
		return ToolResult{Content: rel + ": imports already organized"}, nil
	}

	// Read's bool means "truncated to the size cap", not "exists" — a
	// missing file arrives as the error above. Applying edits to a clipped
	// buffer would amputate the tail of a large file, so too-large is
	// refused honestly instead of written back partial.
	content, tooLarge, err := t.FS.Read(rel)
	if err != nil {
		return errResult("cannot read %s: %v", rel, err), nil
	}
	if tooLarge {
		return errResult("%s exceeds the safe edit size — not applying import changes to a partial view", rel), nil
	}
	next := lsp.ApplyEdits(content, edits)
	if next == content {
		return ToolResult{Content: rel + ": imports already organized"}, nil
	}
	if err := t.FS.Write(rel, next); err != nil {
		return errResult("cannot write %s: %v", rel, err), nil
	}
	return ToolResult{Content: fmt.Sprintf("organized imports in %s (%d edit(s) applied)", rel, len(edits))}, nil
}

// formatterRunner returns program+argv for the workspace's formatter: the
// .talon/config.json `formatter` key when set (behind TALONSTUDIO_LSP —
// flag-off keeps run_prettier byte-identical to shipped), else the npx
// prettier default. run_prettier stays the formatter of record either way;
// the key swaps the binary, not the tool.
func formatterRunner(workspacePath string, targets []string) (string, []string) {
	if lsp.Enabled() {
		if fc := lsp.LoadConfig(workspacePath).FormatterCommand(); len(fc) > 0 {
			argv := append([]string{}, fc[1:]...)
			return fc[0], append(argv, targets...)
		}
	}
	return "npx", append([]string{"--no-install", "prettier", "--write"}, targets...)
}
