package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/sandbox"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W9 gates at the composition layer: the mode resolves from project config
// and the request-body override, the sandbox and run_shell carry it, the
// seam's hard-deny hook is wired, and a read-only project refuses the
// user-initiated HTTP actions with a structured outcome + audit reason.

func writePermissionConfig(t *testing.T, workspace, mode string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]string{"permission_mode": mode})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".talon", "config.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSessionCarriesPermissionMode: read-only override downgrades the
// sandbox capability and reaches run_shell; the default posture is ask and
// the hard-deny hook is wired on the seam.
func TestSessionCarriesPermissionMode(t *testing.T) {
	st, projID := seamFixture(t)
	h := seamHandler(t, st)

	bs := h.newBuildSession(t.TempDir(), projID, 42, "", nil)
	if bs.permMode != approval.ModeAsk {
		t.Errorf("default posture = %q, want ask", bs.permMode)
	}
	if bs.seam == nil || bs.seam.HardDeny == nil {
		t.Fatal("seam's W9 hard-deny hook not wired")
	}

	ro := h.newBuildSession(t.TempDir(), projID, 43, "", nil, "read-only")
	if ro.permMode != approval.ModeReadOnly {
		t.Errorf("override posture = %q, want read-only", ro.permMode)
	}
	if ro.sandbox.Mode() != sandbox.ModeReadOnly {
		t.Errorf("sandbox mode = %v, want read-only capability under a read-only posture", ro.sandbox.Mode())
	}
	tool, ok := ro.reg.Get("run_shell")
	if !ok {
		t.Fatal("run_shell is not registered")
	}
	if rs := tool.(*tools.RunShell); rs.Permission != approval.ModeReadOnly {
		t.Errorf("run_shell Permission = %q, want read-only", rs.Permission)
	}
}

// TestProjectConfigAndOverridePrecedence: .talon/config.json sets the
// project posture; the session override beats it; an invalid override
// collapses to the project posture, an invalid project posture to ask.
func TestProjectConfigAndOverridePrecedence(t *testing.T) {
	st, projID := seamFixture(t)
	h := seamHandler(t, st)

	ws := t.TempDir()
	writePermissionConfig(t, ws, "auto")

	proj := h.newBuildSession(ws, projID, 0, "", nil)
	if proj.permMode != approval.ModeAuto {
		t.Errorf("project config auto → %q, want auto", proj.permMode)
	}
	over := h.newBuildSession(ws, projID, 0, "", nil, "accept-edits")
	if over.permMode != approval.ModeAcceptEdits {
		t.Errorf("session override lost: %q", over.permMode)
	}
	bad := h.newBuildSession(ws, projID, 0, "", nil, "banana")
	if bad.permMode != approval.ModeAuto {
		t.Errorf("invalid override must fall to project posture, got %q", bad.permMode)
	}

	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ".talon", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := h.newBuildSession(broken, projID, 0, "", nil).permMode; m != approval.ModeAsk {
		t.Errorf("broken config → %q, want ask", m)
	}
}

// TestReadOnlyProjectRefusesUserActions: a read-only project posture turns
// the user-initiated HTTP actions into structured refusals — outcome
// rejected with a read-only reason on the single audit event — instead of
// the default confirm-through-the-seam.
func TestReadOnlyProjectRefusesUserActions(t *testing.T) {
	st, projID := seamFixture(t)

	ws := t.TempDir()
	writePermissionConfig(t, ws, "read-only")

	out := approveUserAction(st, projID, ws, scopeGitPush, "push project Seam to GitHub repo seam (branch main)")
	if out != approval.Rejected {
		t.Fatalf("read-only push outcome = %s, want rejected", out)
	}
	evs := approvalDecided(t, openSessionLog(st, projID))
	if len(evs) != 1 {
		t.Fatalf("%d audit events, want exactly 1 (the refusal)", len(evs))
	}
	if evs[0].Allowed || !strings.Contains(evs[0].Reason, "read-only") {
		t.Errorf("refusal audit = {allowed %t reason %q}, want the structured read-only reason", evs[0].Allowed, evs[0].Reason)
	}

	// Default posture (no config) keeps today's confirm: one event, allowed.
	plain := t.TempDir()
	if out := approveUserAction(st, projID, plain, scopeReleaseExport, "export release bundle for project Seam"); out != approval.AllowedOnce {
		t.Errorf("default-posture export outcome = %s, want allowed-once (today's behavior)", out)
	}
}
