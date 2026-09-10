package goals

import (
	"errors"
	"strings"
	"testing"
)

// One goal per project: starting a new one completes the old. Current
// reflects the new one.
func TestOneActiveGoal(t *testing.T) {
	svc := NewService(NewMemStore())
	first, err := svc.Start(1, "ship the wallet flow", 30)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if first.Phase != PhaseActive || first.Revision != 1 {
		t.Fatalf("healthy: fresh goal wrong: %+v", first)
	}
	second, err := svc.Start(1, "fix the release build", 10)
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	cur, err := svc.Current(1)
	if err != nil || cur.ID != second.ID {
		t.Fatalf("current should be the new goal: %+v %v", cur, err)
	}
	if cur.ID == first.ID {
		t.Fatal("fault: old goal still current")
	}
}

// CAS: a stale mutation refuses with ErrStale; a fresh one applies; the
// revision moves.
func TestCAS(t *testing.T) {
	svc := NewService(NewMemStore())
	g, _ := svc.Start(1, "objective", 5)

	// Read the same revision twice; the second apply must go stale.
	r1 := g.Revision
	if _, err := svc.store.Apply(Mutation{GoalID: g.ID, Revision: r1, AddRounds: 1}); err != nil {
		t.Fatalf("fresh apply failed: %v", err)
	}
	if _, err := svc.store.Apply(Mutation{GoalID: g.ID, Revision: r1, AddRounds: 1}); !errors.Is(err, ErrStale) {
		t.Fatalf("fault: stale apply not refused: %v", err)
	}
	cur, _ := svc.Current(1)
	if cur.Rounds != 1 {
		t.Errorf("fault: stale apply leaked through: rounds = %d", cur.Rounds)
	}
	if cur.Revision != 2 {
		t.Errorf("revision = %d, want 2", cur.Revision)
	}
}

// Round accounting is append-only and a resumed goal counts from where it
// stopped — the restart no-double-apply law.
func TestRoundAccountingAppendOnly(t *testing.T) {
	svc := NewService(NewMemStore())
	_, _ = svc.Start(1, "long build", 60)
	// Simulate: 3 rounds run, "restart", 2 more rounds.
	if _, err := svc.RecordRounds(1, 3); err != nil {
		t.Fatalf("record 3: %v", err)
	}
	cur, _ := svc.Current(1)
	rev := cur.Revision
	// The resumed service reads the same store — the state survives.
	if cur.Rounds != 3 {
		t.Fatalf("rounds after restart-read = %d, want 3", cur.Rounds)
	}
	if _, err := svc.RecordRounds(1, 2); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	cur, _ = svc.Current(1)
	if cur.Rounds != 5 || cur.Revision != rev+1 {
		t.Errorf("resumed accounting wrong: rounds=%d rev=%d", cur.Rounds, cur.Revision)
	}
	if !CapReached(Goal{Rounds: 60, MaxRounds: 60}) || CapReached(Goal{Rounds: 59, MaxRounds: 60}) {
		t.Error("cap check broken")
	}
}

// Block carries the concrete condition verbatim; the chip renders it.
func TestBlock(t *testing.T) {
	svc := NewService(NewMemStore())
	_, _ = svc.Start(1, "objective", 10)
	_, _ = svc.RecordRounds(1, 4)
	if _, err := svc.Block(1, ""); err == nil {
		t.Error("fault: empty blocking condition accepted")
	}
	g, err := svc.Block(1, "the gradle cache dir is read-only on this host; builds cannot run")
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if g.Phase != PhaseBlocked {
		t.Fatalf("phase = %s, want blocked", g.Phase)
	}
	// Blocked state survives re-read (the simulated restart).
	cur, _ := svc.Current(1)
	if cur.Phase != PhaseBlocked || !strings.Contains(cur.BlockedReason, "gradle cache") {
		t.Fatalf("blocked state lost across re-read: %+v", cur)
	}
	chip := ChipText(cur)
	if !strings.Contains(chip, "blocked") || !strings.Contains(chip, "gradle cache") {
		t.Errorf("chip does not carry the explanation: %s", chip)
	}

	// Complete renders its own chip.
	if _, err := svc.Complete(1); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// No active goal is a typed answer, not a nil crash.
func TestNoGoal(t *testing.T) {
	svc := NewService(NewMemStore())
	if _, err := svc.Current(99); !errors.Is(err, ErrNoGoal) {
		t.Fatalf("fault: missing goal not typed: %v", err)
	}
}
