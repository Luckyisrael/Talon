package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W1.6: the H4 approval seam reaches run_shell. Tier-2 commands escalate
// through it (one strictly-wider ask, one audit event, turn_approvals the
// binding record), a bare handler (nil slog) still builds a working session,
// and the user-initiated HTTP actions (push, export) audit through the same
// seam.

func seamFixture(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/seam.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("seam@test.local", "x")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	p, err := st.CreateProject(u.ID, "Seam", "1.0.0", "seam", "com.talon.seam", "", "")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return st, p.ID
}

// approvalDecided extracts every ApprovalDecided payload from a replay, in
// order — the audit trail this file's gates count.
func approvalDecided(t *testing.T, lg *sessionlog.Log) []events.ApprovalPayload {
	t.Helper()
	if lg == nil {
		t.Fatal("session log is nil")
	}
	var out []events.ApprovalPayload
	for _, e := range lg.Replay() {
		if e.Kind != events.ApprovalDecided {
			continue
		}
		var p events.ApprovalPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("approval payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func seamHandler(t *testing.T, st *store.Store) *PromptHandler {
	t.Helper()
	return NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)
}

// TestNewBuildSessionWiresTier2Seam guards the registration wiring: the
// session's seam and sandbox reach run_shell, and a bare handler literal
// (nil slog — the shape many tests build) still constructs a session: the
// log resolution degrades instead of panicking.
func TestNewBuildSessionWiresTier2Seam(t *testing.T) {
	st, projID := seamFixture(t)

	h := seamHandler(t, st)
	bs := h.newBuildSession(t.TempDir(), projID, 42, "", nil)
	if bs.seam == nil {
		t.Fatal("session built no approval seam")
	}
	tool, ok := bs.reg.Get("run_shell")
	if !ok {
		t.Fatal("run_shell is not registered")
	}
	rs, ok := tool.(*tools.RunShell)
	if !ok {
		t.Fatalf("run_shell is %T, want *tools.RunShell", tool)
	}
	if rs.Sandbox == nil || rs.Approval == nil {
		t.Fatal("tier-2 seam fields not wired on run_shell")
	}
	if rs.PromptID != 42 {
		t.Errorf("run_shell PromptID = %d, want 42", rs.PromptID)
	}

	bare := &PromptHandler{store: st}
	b2 := bare.newBuildSession(t.TempDir(), projID, 43, "", nil)
	if b2.seam == nil {
		t.Fatal("bare handler (nil slog) built no seam")
	}
}

// TestTier2ShellGrantFlowAuditsOnce is the end-to-end binding record: the
// unanswered ask falls back to ask_user and audits one Unavailable decision;
// the turn grant (tier2_shell) lets the retry run, auditing exactly one
// AllowedOnce — one audit event per decision, never double-counted.
func TestTier2ShellGrantFlowAuditsOnce(t *testing.T) {
	st, projID := seamFixture(t)
	h := seamHandler(t, st)

	const promptID = int64(42)
	bs := h.newBuildSession(t.TempDir(), projID, promptID, "", nil)
	tool, ok := bs.reg.Get("run_shell")
	if !ok {
		t.Fatal("run_shell is not registered")
	}
	rs := tool.(*tools.RunShell)
	exec := func() string {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"command": "git push origin main"})
		res, err := rs.Execute(context.Background(), args)
		if err != nil {
			t.Fatal(err)
		}
		return res.Content
	}

	// Unanswered: the model-visible ask_user fallback, one audit event.
	c := exec()
	if !strings.Contains(c, "APPROVAL REQUIRED") || !strings.Contains(c, "ask_user") {
		t.Fatalf("unanswered tier-2 command should fall back to ask_user, got: %s", c)
	}
	lg := h.slog.get(st, projID)
	evs := approvalDecided(t, lg)
	if len(evs) != 1 {
		t.Fatalf("after the unanswered ask: %d audit events, want exactly 1", len(evs))
	}
	if evs[0].Allowed {
		t.Error("unanswered decision must not read as allowed")
	}

	// The user says yes — the binding grant lands in turn_approvals.
	if err := st.GrantApproval(projID, promptID, store.ScopeTier2Shell); err != nil {
		t.Fatalf("grant: %v", err)
	}

	// The retry runs: subprocess output, not a refusal.
	c = exec()
	if strings.Contains(c, "APPROVAL REQUIRED") || strings.Contains(c, "REFUSED") {
		t.Fatalf("granted tier-2 command was refused: %s", c)
	}
	if !strings.Contains(c, "command failed") && !strings.Contains(c, "succeeded") {
		t.Fatalf("granted tier-2 command did not execute, got: %s", c)
	}

	// Exactly one new audit event: the grant decision, scoped to the
	// escalation, attributed to this prompt.
	evs = approvalDecided(t, lg)
	if len(evs) != 2 {
		t.Fatalf("after the grant: %d audit events, want exactly 2 (one per decision)", len(evs))
	}
	if !evs[1].Allowed {
		t.Error("grant decision must read as allowed")
	}
	if evs[1].PromptID != promptID {
		t.Errorf("audit prompt = %d, want %d", evs[1].PromptID, promptID)
	}
	if !strings.HasPrefix(evs[1].Scope, "sandbox-escalation:") {
		t.Errorf("audit scope = %q, want a sandbox-escalation scope", evs[1].Scope)
	}
}

// TestApproveUserActionAuditsOnce covers the user-initiated HTTP actions
// (git push, release export): one Request through the seam, one audit event
// each, allowed for the authenticated POST.
func TestApproveUserActionAuditsOnce(t *testing.T) {
	st, projID := seamFixture(t)

	out := approveUserAction(st, projID, scopeGitPush, "push project Seam to GitHub repo seam (branch main)")
	if out != approval.AllowedOnce {
		t.Fatalf("git push outcome = %s, want allowed-once", out)
	}
	out = approveUserAction(st, projID, scopeReleaseExport, "export release bundle for project Seam")
	if out != approval.AllowedOnce {
		t.Fatalf("release export outcome = %s, want allowed-once", out)
	}

	evs := approvalDecided(t, openSessionLog(st, projID))
	if len(evs) != 2 {
		t.Fatalf("%d audit events, want exactly 2 (one per action)", len(evs))
	}
	if evs[0].Scope != scopeGitPush || !evs[0].Allowed {
		t.Errorf("first event = {scope %q allowed %t}, want {scope %q allowed true}", evs[0].Scope, evs[0].Allowed, scopeGitPush)
	}
	if evs[1].Scope != scopeReleaseExport || !evs[1].Allowed {
		t.Errorf("second event = {scope %q allowed %t}, want {scope %q allowed true}", evs[1].Scope, evs[1].Allowed, scopeReleaseExport)
	}
}
