package lsp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The client tests run against the fake server (fakeRoot points
// .talon/config.json at this test binary) — real framing, real handshake,
// deterministic behavior. No network, no node_modules, no type-checker.

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// waitAlive polls the process-exit signal: the Wait goroutine closes it
// asynchronously, so death assertions cannot be instantaneous.
func waitAlive(t *testing.T, c *Client, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.Alive() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("client alive=%v, want %v within 3s", c.Alive(), want)
}

// TestClientOrganizeImportsRoundTrip pins W5.4's protocol leg: the fake
// server answers source.organizeImports with one insert-at-top edit,
// returned in 1-based tool coordinates, and applying it to the fixture's
// exact bytes lands the prelude first.
func TestClientOrganizeImportsRoundTrip(t *testing.T) {
	root := fakeRoot(t)
	argv := LoadConfig(root).Command()
	ctx := testCtx(t)

	c, err := startClient(ctx, root, argv)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	defer c.Close()

	edits, err := c.OrganizeImports(ctx, "app.ts")
	if err != nil {
		t.Fatalf("OrganizeImports: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("edits = %+v, want the fake's single insert", edits)
	}
	e := edits[0]
	if e.StartLine != 1 || e.StartCol != 1 || e.EndLine != 1 || e.EndCol != 1 {
		t.Errorf("edit positions = %+v, want 1-based insert at the top", e)
	}
	orig := "export const port: number = 8080\nexport function boot() {}\n"
	got := ApplyEdits(orig, edits)
	want := "import \"./alpha\";\nimport \"./beta\";\n" + orig
	if got != want {
		t.Errorf("applied = %q, want %q", got, want)
	}
}

// TestClientHandshakeDiagnosticsTscShape pins the W5 eval gate's first
// clause end to end: the planted error is NAMED by the server round-trip —
// no tsc anywhere — and renders in tsc's own line shape so the failure
// index's run_typecheck patterns match it unchanged.
func TestClientHandshakeDiagnosticsTscShape(t *testing.T) {
	root := fakeRoot(t)
	argv := LoadConfig(root).Command()
	if len(argv) == 0 {
		t.Fatal("fakeRoot config produced no argv")
	}
	ctx := testCtx(t)

	c, err := startClient(ctx, root, argv)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	defer c.Close()

	diags, err := c.DiagnosticsFor(ctx, "broken.ts")
	if err != nil {
		t.Fatalf("DiagnosticsFor: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly the planted one", diags)
	}
	d := diags[0]
	if d.File != "broken.ts" || d.Line != 1 || d.Col != 1 || d.Severity != 1 || d.Code != "2322" {
		t.Errorf("diagnostic = %+v", d)
	}
	want := "broken.ts(1,1): error TS2322: Type 'string' is not assignable to type 'number'."
	if got := d.FormatTsc(); got != want {
		t.Errorf("FormatTsc() = %q\nwant %q", got, want)
	}

	// A clean file opens clean — the gate does not cry wolf.
	clean, err := c.DiagnosticsFor(ctx, "app.ts")
	if err != nil {
		t.Fatalf("DiagnosticsFor(app.ts): %v", err)
	}
	if len(clean) != 0 {
		t.Errorf("clean file reported: %+v", clean)
	}
}

// TestClientReferencesDefinitionSymbols pins the read-only senses: hits
// resolve across files (the eval gate's second clause), positions come back
// 1-based like the model's world, and symbols flatten with spec-correct
// kind names.
func TestClientReferencesDefinitionSymbols(t *testing.T) {
	root := fakeRoot(t)
	t.Setenv("FAKE_OTHER_URI", absURI(filepath.Join(root, "other.ts")))
	argv := LoadConfig(root).Command()
	ctx := testCtx(t)

	c, err := startClient(ctx, root, argv)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	defer c.Close()

	refs, err := c.References(ctx, "app.ts", 1, 17)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want the hit in app.ts and the cross-file one", refs)
	}
	if refs[0].File != "app.ts" || refs[0].Line != 1 || refs[0].Col != 17 {
		t.Errorf("refs[0] = %+v, want app.ts:1:17", refs[0])
	}
	if refs[1].File != "other.ts" || refs[1].Line != 5 || refs[1].Col != 1 {
		t.Errorf("refs[1] = %+v, want other.ts:5:1", refs[1])
	}

	def, err := c.Definition(ctx, "app.ts", 1, 1)
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(def) != 1 || def[0].File != "app.ts" || def[0].Line != 8 || def[0].Col != 1 {
		t.Errorf("def = %+v, want app.ts:8:1", def)
	}

	syms, err := c.Symbols(ctx, "app.ts")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("syms = %+v", syms)
	}
	if syms[0].Name != "boot" || syms[0].Kind != 12 || syms[0].Line != 1 {
		t.Errorf("syms[0] = %+v, want boot function at 1", syms[0])
	}
	if syms[1].Name != "AppRoot" || syms[1].Kind != 5 || syms[1].Line != 11 {
		t.Errorf("syms[1] = %+v, want AppRoot class at 11", syms[1])
	}
	if got := KindName(syms[1].Kind); got != "class" {
		t.Errorf("KindName(5) = %q, want class (spec numbering)", got)
	}
}

// TestClientMissingBinaryIsUnavailable pins fail-open-availability: a
// server that cannot start is the structured LSP_UNAVAILABLE degradation,
// never a hard error anything could trip over.
func TestClientMissingBinaryIsUnavailable(t *testing.T) {
	_, err := startClient(context.Background(), t.TempDir(), []string{"/definitely/not/a/lsp/server"})
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want structured Unavailable", err)
	}
	if !strings.Contains(err.Error(), "LSP_UNAVAILABLE") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

// TestClientCrashIsStructuredAndFreshRestartWorks pins restart-on-crash:
// a server that dies mid-request fails that request and marks the client
// dead; a fresh start (the crash script is once-only, marked by a file)
// serves normally — one crash proves replacement, not a crash loop.
func TestClientCrashIsStructuredAndFreshRestartWorks(t *testing.T) {
	root := fakeRoot(t)
	marker := filepath.Join(t.TempDir(), "crash-once")
	t.Setenv("FAKE_CRASH_ONCE_METHOD", "textDocument/documentSymbol")
	t.Setenv("FAKE_CRASH_ONCE_FILE", marker)
	argv := LoadConfig(root).Command()
	ctx := testCtx(t)

	c1, err := startClient(ctx, root, argv)
	if err != nil {
		t.Fatalf("first startClient: %v", err)
	}
	if _, err := c1.Symbols(ctx, "app.ts"); err == nil {
		t.Fatal("Symbols should fail when the server dies mid-request")
	}
	waitAlive(t, c1, false)
	_ = c1.Close() // hardClose on an already-dead client: cheap and safe

	c2, err := startClient(ctx, root, argv)
	if err != nil {
		t.Fatalf("replacement startClient: %v", err)
	}
	defer c2.Close()
	if !c2.Alive() {
		t.Fatal("replacement client should be alive")
	}
	syms, err := c2.Symbols(ctx, "app.ts")
	if err != nil {
		t.Fatalf("replacement Symbols: %v", err)
	}
	if len(syms) != 2 {
		t.Errorf("replacement served wrong symbols: %+v", syms)
	}
}
