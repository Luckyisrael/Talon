package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The reaper's clock lives here, next to the SQL it is. Aged rows are
// written directly — the exact shape a pre-DV4 row or a 20-minute-idle
// session has.
func TestReaperClock(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, _ := st.CreateUser("rc@e.test", "x")
	p, _ := st.CreateProject(u.ID, "p", "v", "s", "a.b", "c.d", "e")
	sess, _ := st.CreateSession(p.ID)

	// Fresh: not idle for 10 minutes.
	if idle, err := st.IdleSessions(10 * time.Minute); err != nil || len(idle) != 0 {
		t.Errorf("fault: fresh session idle (%v, %d rows)", err, len(idle))
	}

	// Aged 20 minutes: idle for the 10-minute window.
	if _, err := st.db.Exec(`UPDATE sessions SET last_active_at = datetime('now','-20 minutes') WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	idle, err := st.IdleSessions(10 * time.Minute)
	if err != nil {
		t.Fatalf("idle query: %v", err)
	}
	found := false
	for _, s := range idle {
		if s.ID == sess.ID {
			found = true
		}
	}
	if !found {
		t.Error("fault: 20-minute-old session not idle for the 10m window")
	}

	// Touch resets: leaves the idle set.
	if err := st.TouchSession(sess.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if idle, err := st.IdleSessions(10 * time.Minute); err != nil {
		t.Fatalf("idle query: %v", err)
	} else {
		for _, s := range idle {
			if s.ID == sess.ID {
				t.Error("fault: touched session still idle")
			}
		}
	}

	// A destroyed session never reappears — the reaper must not touch it.
	st.UpdateSessionState(sess.ID, "destroyed")
	if _, err := st.db.Exec(`UPDATE sessions SET last_active_at = datetime('now','-2 hours') WHERE id = ?`, sess.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	if idle, err := st.IdleSessions(10 * time.Minute); err != nil {
		t.Fatalf("idle query: %v", err)
	} else {
		for _, s := range idle {
			if s.ID == sess.ID {
				t.Error("fault: destroyed session in the idle set")
			}
		}
	}

	// The pre-DV4 fallback: a row with NULL last_active_at falls to its
	// started_at. A 2-hour-old start with NULL touch must be idle.
	older, _ := st.CreateSession(p.ID)
	if _, err := st.db.Exec(`UPDATE sessions SET last_active_at = NULL, started_at = datetime('now','-2 hours') WHERE id = ?`, older.ID); err != nil {
		t.Fatalf("null touch: %v", err)
	}
	if idle, err := st.IdleSessions(30 * time.Minute); err != nil {
		t.Fatalf("idle query: %v", err)
	} else {
		found := false
		for _, s := range idle {
			if s.ID == older.ID {
				found = true
			}
		}
		if !found {
			t.Error("fault: NULL-touch row did not fall back to started_at")
		}
	}
}
