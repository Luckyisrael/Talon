package redroid

import (
	"path/filepath"
	"testing"

	"github.com/seekersstudio/backend/internal/store"
)

// A session whose container is gone must be marked destroyed and its port
// released — otherwise a crash leaves phantom sessions holding ports
// forever. Uses the real docker daemon for listing (read-only here: the
// fixture container id matches nothing, so nothing is created or removed).
func TestReconcileDestroysGoneContainers(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon unavailable")
	}
	st, err := store.New(filepath.Join(t.TempDir(), "reconcile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("rec@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A container id that cannot exist: 64 hex chars, zero match in docker.
	if err := st.UpdateSessionContainer(sess.ID, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "gone", 15555); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(NewPortAllocator(15555, 15560), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reconcile(st); err != nil {
		t.Fatalf("reconcile must not fail on a missing container: %v", err)
	}

	got, err := st.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "destroyed" {
		t.Errorf("expected destroyed, got %q", got.State)
	}
	if got.ContainerID != "" || got.ADBPort != 0 {
		t.Errorf("expected container refs cleared, got %+v", got)
	}
}

func dockerAvailable() bool {
	mgr, err := NewManager(NewPortAllocator(15555, 15560), 1)
	if err != nil {
		return false
	}
	_ = mgr
	return true
}
