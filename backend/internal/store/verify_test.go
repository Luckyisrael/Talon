package store

import (
	"path/filepath"
	"testing"
)

func newVerifyTestProject(t *testing.T) (*Store, int64) {
	t.Helper()
	st, err := New(filepath.Join(t.TempDir(), "verify.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("verify@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "verify", "1.0.0", "verify-slug", "com.test.verify", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, p.ID
}

// A fresh project has never recorded a sweep: found=false with no error, so
// release can treat absence as its own refusal rather than a read failure.
func TestProjectVerifyStateNotFound(t *testing.T) {
	st, pid := newVerifyTestProject(t)
	found, got, err := st.GetProjectVerifyState(pid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found {
		t.Error("a fresh project must report no verification record")
	}
	if got.ProjectID != pid {
		t.Errorf("project id = %d, want %d", got.ProjectID, pid)
	}
}

// Set → Get carries every field the release rule reads.
func TestProjectVerifyStateRoundTrip(t *testing.T) {
	st, pid := newVerifyTestProject(t)
	in := ProjectVerifyState{
		ProjectID: pid,
		Status:    "refused",
		Policy:    "strict",
		Skipped:   []string{"typecheck"},
		Remaining: []string{"Add settings screen"},
		Reason:    "required gate(s) never ran: typecheck",
	}
	if err := st.SetProjectVerifyState(in); err != nil {
		t.Fatalf("set: %v", err)
	}
	found, got, err := st.GetProjectVerifyState(pid)
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if got.Status != "refused" || got.Policy != "strict" || got.Reason != in.Reason {
		t.Errorf("got %+v, want status/policy/reason from %+v", got, in)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "typecheck" {
		t.Errorf("skipped = %v, want [typecheck]", got.Skipped)
	}
	if len(got.Remaining) != 1 || got.Remaining[0] != "Add settings screen" {
		t.Errorf("remaining = %v, want the pending plan task", got.Remaining)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at should be set")
	}
}

// One row per project: the newest sweep wins — a later pass clears an
// earlier refusal, and nil slices come back as empty, not null-shaped.
func TestProjectVerifyStateUpsertLatestWins(t *testing.T) {
	st, pid := newVerifyTestProject(t)
	first := ProjectVerifyState{ProjectID: pid, Status: "refused", Reason: "ladder exhausted", Skipped: []string{"lint"}}
	if err := st.SetProjectVerifyState(first); err != nil {
		t.Fatal(err)
	}
	second := ProjectVerifyState{ProjectID: pid, Status: "verified", Policy: "strict"}
	if err := st.SetProjectVerifyState(second); err != nil {
		t.Fatal(err)
	}
	found, got, err := st.GetProjectVerifyState(pid)
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if got.Status != "verified" || got.Reason != "" {
		t.Errorf("newest sweep must win, got status=%q reason=%q", got.Status, got.Reason)
	}
	if len(got.Skipped) != 0 || len(got.Remaining) != 0 {
		t.Errorf("nil slices should read back empty, got skipped=%v remaining=%v", got.Skipped, got.Remaining)
	}
}
