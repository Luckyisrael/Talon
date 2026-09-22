package lsp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeFleet builds a manager (explicit windows — tests own the timing), a
// fake-server workspace, and the flag ON: Manager.Client is flag-gated,
// unlike the direct startClient tests in client_test.go.
func fakeFleet(t *testing.T, idle, sweep time.Duration, max int) (*Manager, string) {
	t.Helper()
	t.Setenv("TALONSTUDIO_LSP", "v1")
	root := fakeRoot(t)
	m := newManager(idle, sweep, max)
	t.Cleanup(m.Stop)
	return m, root
}

// TestManagerCachesAndRestartsOnCrash pins the fleet's contract: one
// process per workspace+language (cache hits return the same client), and
// a server that dies between calls is replaced — the replacement serves
// normally because the crash script is once-only.
func TestManagerCachesAndRestartsOnCrash(t *testing.T) {
	m, root := fakeFleet(t, time.Minute, time.Second, 4)
	// Crash env must be present at SPAWN time (cmd.Env snapshots os.Environ).
	marker := filepath.Join(t.TempDir(), "crash-once")
	t.Setenv("FAKE_CRASH_ONCE_METHOD", "textDocument/documentSymbol")
	t.Setenv("FAKE_CRASH_ONCE_FILE", marker)
	ctx := testCtx(t)

	c1, err := m.Client(ctx, root, "app.ts")
	if err != nil {
		t.Fatalf("first Client: %v", err)
	}
	c2, err := m.Client(ctx, root, "app.ts")
	if err != nil {
		t.Fatalf("second Client: %v", err)
	}
	if c2 != c1 {
		t.Error("cache miss on an alive server — one process per workspace+language")
	}

	if _, err := c1.Symbols(ctx, "app.ts"); err == nil {
		t.Fatal("Symbols should fail when the server dies mid-request")
	}
	waitAlive(t, c1, false)

	c3, err := m.Client(ctx, root, "app.ts")
	if err != nil {
		t.Fatalf("restart Client: %v", err)
	}
	if c3 == c1 {
		t.Fatal("dead server must be replaced, not returned")
	}
	syms, err := c3.Symbols(ctx, "app.ts")
	if err != nil {
		t.Fatalf("replacement Symbols: %v", err)
	}
	if len(syms) != 2 {
		t.Errorf("replacement served wrong symbols: %+v", syms)
	}
}

// TestManagerLRUEvictsColdest pins the cap: at max servers, admitting a
// new workspace closes the coldest one — no unbounded process fleet, the
// plan's fleet risk managed the way the device pool manages ports.
func TestManagerLRUEvictsColdest(t *testing.T) {
	m, rootA := fakeFleet(t, time.Minute, time.Second, 1)
	_, rootB := fakeFleet(t, time.Minute, time.Second, 1) // second workspace, same cap
	ctx := testCtx(t)

	cA, err := m.Client(ctx, rootA, "app.ts")
	if err != nil {
		t.Fatalf("client A: %v", err)
	}
	cB, err := m.Client(ctx, rootB, "app.ts")
	if err != nil {
		t.Fatalf("client B: %v", err)
	}
	waitAlive(t, cA, false) // coldest evicted
	waitAlive(t, cB, true)  // newest admitted

	cB2, err := m.Client(ctx, rootB, "app.ts")
	if err != nil {
		t.Fatalf("cached client B: %v", err)
	}
	if cB2 != cB {
		t.Error("kept server must stay cached after an eviction")
	}
}

// TestManagerIdleReaper pins the device-reaper windows applied to servers:
// use refreshes the clock, and an untouched server is shut down and
// dropped from the fleet — nothing idles forever.
func TestManagerIdleReaper(t *testing.T) {
	m, root := fakeFleet(t, 500*time.Millisecond, 50*time.Millisecond, 4)
	ctx := testCtx(t)

	c, err := m.Client(ctx, root, "app.ts")
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	waitAlive(t, c, true)

	// Touch inside the window: total age may exceed idleAfter, but the
	// refresh is what the sweep compares against.
	time.Sleep(300 * time.Millisecond)
	if _, err := m.Client(ctx, root, "app.ts"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	waitAlive(t, c, true)

	// Now leave it alone: first sweep past idleAfter reaps it.
	waitAlive(t, c, false)
	m.mu.Lock()
	n := len(m.servers)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("fleet still holds %d server(s) after reap", n)
	}
}

// TestManagerResolveDetectionAndOverride is resolution without spawning:
// builtin argv per extension, the .talon/config.json override replacing the
// builtin for detected languages, and undetected extensions staying
// undetected (the key refines detection; it does not invent it).
func TestManagerResolveDetectionAndOverride(t *testing.T) {
	root := t.TempDir()
	if lang, argv := resolve(root, "src/main.go"); lang != "go" || len(argv) != 1 || argv[0] != "gopls" {
		t.Errorf("go detection = %q %q", lang, argv)
	}
	if lang, argv := resolve(root, "app.tsx"); lang != "ts" ||
		len(argv) != 2 || argv[0] != "typescript-language-server" || argv[1] != "--stdio" {
		t.Errorf("ts detection = %q %q", lang, argv)
	}
	if lang, argv := resolve(root, "notes.txt"); lang != "" || argv != nil {
		t.Errorf("undetected extension must stay undetected: %q %q", lang, argv)
	}

	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"),
		[]byte(`{"lsp": "/opt/custom-ls --stdio"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if lang, argv := resolve(root, "app.ts"); lang != "ts" ||
		len(argv) != 2 || argv[0] != "/opt/custom-ls" || argv[1] != "--stdio" {
		t.Errorf("override = %q %q", lang, argv)
	}

	for path, want := range map[string]string{
		"main.go": "go", "APP.TSX": "ts", "x.jsx": "ts", "readme.md": "",
	} {
		if got := LangFor(path); got != want {
			t.Errorf("LangFor(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestManagerFlagOffIsUnavailable pins flag-off: the fleet answers with the
// structured degradation and spawns nothing — deployment runs every flag
// unset and must never see a language-server process.
func TestManagerFlagOffIsUnavailable(t *testing.T) {
	m, root := fakeFleet(t, time.Minute, time.Second, 4)
	t.Setenv("TALONSTUDIO_LSP", "") // override the fleet's flag-on
	_, err := m.Client(testCtx(t), root, "app.ts")
	if !IsUnavailable(err) {
		t.Fatalf("err = %v, want structured Unavailable", err)
	}
	if err.Error() != "LSP_UNAVAILABLE: TALONSTUDIO_LSP is off" {
		t.Errorf("err = %v", err)
	}
	m.mu.Lock()
	n := len(m.servers)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("flag off must spawn nothing, fleet holds %d", n)
	}
}
