package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// newVerifyExport builds a Pro-tier owner + project with no verification
// record, so each case below can plant the state it needs.
func newVerifyExport(t *testing.T) (*ExportHandler, *store.Store, *store.Project) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "exportverify.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("verify-exp@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTier(u.ID, "pro"); err != nil {
		t.Fatalf("SetTier: %v", err)
	}
	p, err := st.CreateProject(u.ID, "ve", "1.0.0", "ve-slug", "com.test.ve", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return &ExportHandler{store: st}, st, p
}

// The W6 shipping rule: no record refuses, refused refuses, best-effort
// refuses — each 403 carries the reason, a named override ships and lands
// exactly one verify_override audit row, and a clean verified record ships
// with no audit noise.
func TestEnforceVerifyPolicyRefusesAndAudits(t *testing.T) {
	h, st, p := newVerifyExport(t)

	// Never verified: absence of evidence is not a passing build.
	req := authedRequest(p.UserID, "")
	rec := httptest.NewRecorder()
	if h.enforceVerifyPolicy(rec, req, p, false) {
		t.Fatal("a project with no verification record must not ship")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no verification record") {
		t.Errorf("body should explain the refusal: %s", rec.Body.String())
	}
	if rows, err := st.ListAuditLog(p.ID, 10); err != nil || len(rows) != 0 {
		t.Errorf("a plain refusal is not an override: rows=%v err=%v", rows, err)
	}

	// Refused: the recorded reason reaches the caller.
	if err := st.SetProjectVerifyState(store.ProjectVerifyState{
		ProjectID: p.ID,
		Status:    "refused",
		Policy:    "strict",
		Reason:    "required gate(s) never ran: typecheck",
	}); err != nil {
		t.Fatal(err)
	}
	req = authedRequest(p.UserID, "")
	rec = httptest.NewRecorder()
	if h.enforceVerifyPolicy(rec, req, p, false) {
		t.Fatal("a refused sweep must not ship")
	}
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "typecheck") {
		t.Errorf("status=%d body=%s, want 403 naming the gate", rec.Code, rec.Body.String())
	}

	// Named human key: override ships and audits the actor + the reason.
	req = authedRequest(p.UserID, "")
	rec = httptest.NewRecorder()
	if !h.enforceVerifyPolicy(rec, req, p, true) {
		t.Fatal("override:true must ship a refused build")
	}
	rows, err := st.ListAuditLog(p.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one audit row, got %d", len(rows))
	}
	if rows[0].Action != "verify_override" || rows[0].TargetID != "export" || rows[0].UserID != p.UserID {
		t.Errorf("audit row = %+v, want verify_override/export by the project owner", rows[0])
	}
	if !strings.Contains(rows[0].Detail, "typecheck") {
		t.Errorf("audit detail should carry the refusal reason: %q", rows[0].Detail)
	}

	// Best-effort: refuses without an override, naming the skipped gate.
	if err := st.SetProjectVerifyState(store.ProjectVerifyState{
		ProjectID: p.ID,
		Status:    "best-effort",
		Policy:    "strict",
		Skipped:   []string{"boot"},
		Remaining: []string{"Pair a device"},
	}); err != nil {
		t.Fatal(err)
	}
	req = authedRequest(p.UserID, "")
	rec = httptest.NewRecorder()
	if h.enforceVerifyPolicy(rec, req, p, false) {
		t.Fatal("a best-effort record must not ship silently")
	}
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "boot") {
		t.Errorf("status=%d body=%s, want 403 naming the skip", rec.Code, rec.Body.String())
	}
	req = authedRequest(p.UserID, "")
	if !h.enforceVerifyPolicy(httptest.NewRecorder(), req, p, true) {
		t.Fatal("override must ship a best-effort build")
	}
	if rows, err := st.ListAuditLog(p.ID, 10); err != nil || len(rows) != 2 {
		t.Errorf("each override attempt is its own audit row: rows=%d err=%v", len(rows), err)
	}

	// Verified: ships clean — no refusal, no audit noise.
	if err := st.SetProjectVerifyState(store.ProjectVerifyState{
		ProjectID: p.ID,
		Status:    "verified",
		Policy:    "strict",
	}); err != nil {
		t.Fatal(err)
	}
	req = authedRequest(p.UserID, "")
	rec = httptest.NewRecorder()
	if !h.enforceVerifyPolicy(rec, req, p, false) {
		t.Fatal("a verified record must ship")
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("success writes nothing: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rows, err := st.ListAuditLog(p.ID, 10); err != nil || len(rows) != 2 {
		t.Errorf("a clean ship adds no audit rows: rows=%d err=%v", len(rows), err)
	}
}

// Create-level: the policy check runs before the approval seam and the
// export job — a refused project gets the 403 and never reaches either
// (the handler's keystore/builder are nil below; reaching them would panic
// the test rather than return 403).
func TestCreateExportRefusedByVerifyPolicy(t *testing.T) {
	h, st, p := newVerifyExport(t)
	if err := st.SetProjectVerifyState(store.ProjectVerifyState{
		ProjectID: p.ID,
		Status:    "refused",
		Reason:    "code did not pass verification after 6 repair attempts",
	}); err != nil {
		t.Fatal(err)
	}

	req := authedRequest(p.UserID, `{"whats_new":"first release"}`)
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", p.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s, want 403 from verify policy", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "verification") {
		t.Errorf("body should name the policy refusal: %s", rec.Body.String())
	}
}
