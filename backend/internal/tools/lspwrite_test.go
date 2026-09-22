package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/lsp"
)

// W5.4: organize-imports is the one lsp write — proposed by the server,
// applied through FS (journaled, workspace-scoped), refused when
// unavailable, byte-identical when nothing changes. formatterRunner pins
// the config-key override and its flag gate.

func TestLSPOrganizeImportsAppliesThroughFS(t *testing.T) {
	root := t.TempDir()
	fs := NewFS(root)
	const orig = "export const b = 1\nexport const a = 2\n"
	if err := os.WriteFile(filepath.Join(root, "src.ts"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLSP{edits: []lsp.TextEdit{
		{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1, NewText: "import \"./z\";\n"},
	}}
	tool := &LSPOrganizeImports{
		FS:      fs,
		Resolve: resolveByClass(map[string]LSPConn{"ts": fake}, nil),
	}

	res := lspExec(t, tool, `{"path":"src.ts"}`)
	if res.IsError {
		t.Fatalf("organize errored: %s", res.Content)
	}
	if !strings.Contains(res.Content, "organized imports in src.ts") {
		t.Errorf("res = %q", res.Content)
	}
	after, _, err := fs.Read("src.ts")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if want := "import \"./z\";\n" + orig; after != want {
		t.Errorf("file = %q, want %q", after, want)
	}
	// The write lands in the journal like every other write.
	if touched := fs.Touched(); len(touched) != 1 || touched[0] != "src.ts" {
		t.Errorf("Touched() = %v, want [src.ts]", touched)
	}
}

// No edits from the server means already-organized: an honest message,
// not an error — and the bytes stay byte-identical.
func TestLSPOrganizeImportsNoEditsIsHonestNoop(t *testing.T) {
	root := t.TempDir()
	fs := NewFS(root)
	const orig = "export const a = 1\n"
	if err := os.WriteFile(filepath.Join(root, "src.ts"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &LSPOrganizeImports{
		FS:      fs,
		Resolve: resolveByClass(map[string]LSPConn{"ts": &fakeLSP{}}, nil),
	}

	res := lspExec(t, tool, `{"path":"src.ts"}`)
	if res.IsError || !strings.Contains(res.Content, "already organized") {
		t.Fatalf("res = %+v, want honest no-op", res)
	}
	after, _, _ := fs.Read("src.ts")
	if after != orig {
		t.Errorf("bytes changed on a no-op: %q", after)
	}
}

// Unavailable degrades the same way every lsp tool does — structured
// error results the ladder reads as skips, never panics.
func TestLSPOrganizeImportsUnavailablePaths(t *testing.T) {
	root := t.TempDir()

	noResolve := &LSPOrganizeImports{FS: NewFS(root)}
	res := lspExec(t, noResolve, `{"path":"src.ts"}`)
	if !res.IsError || !strings.Contains(res.Content, "LSP_UNAVAILABLE") {
		t.Errorf("nil resolver = %+v", res)
	}

	noFS := &LSPOrganizeImports{Resolve: resolveByClass(map[string]LSPConn{"ts": &fakeLSP{}}, nil)}
	res = lspExec(t, noFS, `{"path":"src.ts"}`)
	if !res.IsError || !strings.Contains(res.Content, "filesystem not wired") {
		t.Errorf("nil FS = %+v", res)
	}

	res = lspExec(t, noResolve, `{}`)
	if !res.IsError || !strings.Contains(res.Content, "path is required") {
		t.Errorf("missing path = %+v", res)
	}
}

// The formatter key swaps the command only behind TALONSTUDIO_LSP —
// flag-off (every deployment) keeps run_prettier byte-identical npx
// prettier, with or without a config file present.
func TestFormatterRunnerConfigKeyAndFlag(t *testing.T) {
	targets := []string{"src.ts"}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"),
		[]byte(`{"formatter": "dprint write"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	prog, argv := formatterRunner(root, targets)
	if prog != "npx" || len(argv) != 4 || argv[0] != "--no-install" ||
		argv[1] != "prettier" || argv[2] != "--write" || argv[3] != "src.ts" {
		t.Errorf("flag off = %q %q, want shipped npx prettier", prog, argv)
	}

	t.Setenv("TALONSTUDIO_LSP", "v1")
	prog, argv = formatterRunner(root, targets)
	if prog != "dprint" || len(argv) != 2 || argv[0] != "write" || argv[1] != "src.ts" {
		t.Errorf("flag on with key = %q %q, want dprint write src.ts", prog, argv)
	}

	plain := t.TempDir()
	prog, _ = formatterRunner(plain, targets)
	if prog != "npx" {
		t.Errorf("flag on without key must keep the default, got %q", prog)
	}
}
