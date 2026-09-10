package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The mode matrix: read-only refuses writes and outside reads; workspace
// allows in-root writes and tmp scratch, refuses escapes; danger allows all.
// Fail-closed: no enforcement hook means SANDBOX_UNAVAILABLE, never a silent
// unconfined allow.
func TestModeMatrix(t *testing.T) {
	root := t.TempDir()
	tmp := t.TempDir()
	noop := func(op Operation) error { return nil }

	ro := New(ModeReadOnly, root, []string{tmp}, noop)
	if d := ro.AllowsPath("read", filepath.Join(root, "a.tsx")); !d.Allowed {
		t.Errorf("healthy: read inside workspace refused under read-only: %s", d.Reason)
	}
	if d := ro.AllowsPath("write", filepath.Join(root, "a.tsx")); d.Allowed {
		t.Error("fault: write allowed under read-only")
	}
	if d := ro.AllowsPath("read", "/etc/passwd"); d.Allowed {
		t.Error("fault: outside read allowed")
	}

	ws := New(ModeWorkspace, root, []string{tmp}, noop)
	if d := ws.AllowsPath("write", filepath.Join(root, "app.tsx")); !d.Allowed {
		t.Errorf("healthy: in-root write refused: %s", d.Reason)
	}
	if d := ws.AllowsPath("write", filepath.Join(tmp, "scratch.txt")); !d.Allowed {
		t.Errorf("healthy: tmp write refused: %s", d.Reason)
	}
	if d := ws.AllowsPath("write", "/home/someone/else"); d.Allowed {
		t.Error("fault: outside write allowed")
	}
	// Path-crafting: root prefix as a directory sibling must not pass.
	if d := ws.AllowsPath("write", root+"-evil/x"); d.Allowed {
		t.Error("fault: sibling directory with root as prefix passed")
	}

	df := New(ModeDangerFull, root, nil, noop)
	if d := df.AllowsPath("write", "/anywhere"); !d.Allowed {
		t.Errorf("healthy: danger mode refused a write: %s", d.Reason)
	}

	// Fail-closed: workspace mode without an enforcement hook.
	noEnf := New(ModeWorkspace, root, nil, nil)
	d := noEnf.AllowsPath("write", filepath.Join(root, "a.tsx"))
	if d.Allowed {
		t.Fatal("fault: write allowed without enforcement — SANDBOX_UNAVAILABLE is the contract")
	}
	if !strings.Contains(d.Reason, "SANDBOX_UNAVAILABLE") {
		t.Errorf("fault: refusal not structured: %q", d.Reason)
	}
}

// ResolveWorkspace rejects escapes before they can name anything outside.
func TestResolveWorkspace(t *testing.T) {
	c := New(ModeWorkspace, "/workspace", nil, func(Operation) error { return nil })
	if _, err := c.ResolveWorkspace("../outside"); err == nil {
		t.Error("fault: parent escape resolved")
	}
	if _, err := c.ResolveWorkspace("/abs"); err == nil {
		t.Error("fault: absolute path resolved as workspace-relative")
	}
	p, err := c.ResolveWorkspace("app/main.tsx")
	if err != nil || p != "/workspace/app/main.tsx" {
		t.Errorf("healthy path broken: %q %v", p, err)
	}
}

// Stat goes through the verdict first: even existence probing is confined.
func TestStatConfined(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o600)
	ro := New(ModeReadOnly, root, nil, func(Operation) error { return nil })
	if _, err := ro.Stat(filepath.Join(root, "f.txt")); err != nil {
		t.Errorf("healthy: stat inside root refused: %v", err)
	}
	if _, err := ro.Stat("/etc/passwd"); err == nil {
		t.Error("fault: stat outside root allowed — confinement probing leak")
	}
}
