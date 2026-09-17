package goals

import "testing"

// A goal that stalls for one or two rounds is not blocked — one bad round is
// recoverable friction, and marking it blocked devalues the phase for every
// goal after it. The floor is three consecutive stalled rounds.
func TestBlockRefusesPremature(t *testing.T) {
	svc := NewService(NewMemStore())
	if _, err := svc.Start(1, "ship the wallet screen", 20); err != nil {
		t.Fatal(err)
	}

	for i := 1; i < blockedAfterConsecutiveRounds; i++ {
		if _, err := svc.RecordStall(1, false); err != nil {
			t.Fatal(err)
		}
		g, err := svc.Block(1, "the build keeps failing typecheck")
		if err != ErrPrematureBlock {
			t.Fatalf("after %d stalled rounds the goal must not be blockable, got err=%v g=%+v", i, err, g)
		}
		if g.Phase != PhaseActive {
			t.Errorf("a refused block must leave the goal active, got %s", g.Phase)
		}
	}

	// The floor met: the block lands.
	if _, err := svc.RecordStall(1, false); err != nil {
		t.Fatal(err)
	}
	g, err := svc.Block(1, "the build keeps failing typecheck")
	if err != nil {
		t.Fatalf("after %d stalled rounds the goal must be blockable: %v", blockedAfterConsecutiveRounds, err)
	}
	if g.Phase != PhaseBlocked {
		t.Errorf("got phase %s, want blocked", g.Phase)
	}
	if g.BlockedReason != "the build keeps failing typecheck" {
		t.Errorf("blocked reason must be carried verbatim, got %q", g.BlockedReason)
	}
}

// A round that advances the goal resets the stall counter: a goal that stalls,
// recovers, and stalls again accumulates from zero, never from history.
func TestStallResetsOnAdvance(t *testing.T) {
	svc := NewService(NewMemStore())
	if _, err := svc.Start(1, "ship the wallet screen", 20); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < blockedAfterConsecutiveRounds-1; i++ {
		if _, err := svc.RecordStall(1, false); err != nil {
			t.Fatal(err)
		}
	}
	// One advancing round undoes the accumulation.
	if _, err := svc.RecordStall(1, true); err != nil {
		t.Fatal(err)
	}
	g, err := svc.Block(1, "persistent failure")
	if err != ErrPrematureBlock {
		t.Fatalf("an advancing round must reset the stall count, got err=%v g=%+v", err, g)
	}
	if g.StalledRounds != 0 {
		t.Errorf("the counter must reset to 0 after an advancing round, got %d", g.StalledRounds)
	}
	if StalledEnough(g) {
		t.Error("StalledEnough must be false after a reset")
	}
}

// Paused is a deliberate stop for a human, not a failure. It resumes; blocked
// does not.
func TestPauseAndResume(t *testing.T) {
	svc := NewService(NewMemStore())
	if _, err := svc.Start(1, "ship the wallet screen", 20); err != nil {
		t.Fatal(err)
	}
	g, err := svc.Pause(1, "the user to pick a signing library")
	if err != nil {
		t.Fatal(err)
	}
	if g.Phase != PhasePaused {
		t.Fatalf("got phase %s, want paused", g.Phase)
	}
	if g.PausedReason != "the user to pick a signing library" {
		t.Errorf("paused reason must be carried, got %q", g.PausedReason)
	}
	if chip := ChipText(g); chip == "" {
		t.Error("a paused goal must render a chip line")
	} else if !contains(chip, "paused") {
		t.Errorf("the chip must say paused, got %q", chip)
	}

	// Resuming returns it to active.
	if g, err = svc.Resume(1); err != nil {
		t.Fatal(err)
	}
	if g.Phase != PhaseActive {
		t.Errorf("got phase %s, want active after resume", g.Phase)
	}

	// A blocked goal does not resume — it ended for a reason that persisted.
	for i := 0; i < blockedAfterConsecutiveRounds; i++ {
		if _, err := svc.RecordStall(1, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.Block(1, "persistent failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resume(1); err != ErrNotPaused {
		t.Errorf("a blocked goal must not resume, got err=%v", err)
	}
}

// An empty pause reason is a bug in the caller, not a silent no-op.
func TestPauseNeedsAReason(t *testing.T) {
	svc := NewService(NewMemStore())
	if _, err := svc.Start(1, "ship the wallet screen", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pause(1, "  "); err == nil {
		t.Error("a blank pause reason must be rejected")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
