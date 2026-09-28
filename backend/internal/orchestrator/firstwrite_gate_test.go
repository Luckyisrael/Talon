package orchestrator

import (
	"strings"
	"testing"
	"time"
)

// L17 first-artifact clock (ENGINEERING_STANDARDS §6 table, L17; S1 row).
//
// Failure modes, written first (TEST_RULES §3):
//
//  1. FM-D1a — inaction has no clock. S1 (first write ≤ 3 min) failed all
//     three runs (28m34s / 4m18s / 11m49s) under prompts that already said
//     "write early"; and Run-3's round-1 9.5-minute silent generation
//     proves a round-count budget cannot see wall time. The gate must read
//     the clock: advisory on the card at +3 min, one latched warning event
//     at +6, build mode only, silent after the first successful write.
//  2. FM-D1b — two voices on one card. While nothing has been written the
//     first-write note owns the card even after the route note becomes due;
//     the route note resumes only once the first write lands (its own
//     ladder test stays pinned).

func TestFirstWriteLadder(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)

	// Inside S1's standard: silence. Writing within three minutes is the
	// bar, not a violation of it.
	s.StartedAt = time.Now()
	if got := s.FirstWriteNote("build"); got != "" {
		t.Fatalf("note inside the first-write window: %q", got)
	}
	if s.FirstWriteWarnDue("build") {
		t.Fatal("warning due inside the first-write window")
	}

	// Past the standard: an advisory that names the standard and the
	// artifact to write — a nudge the model can act on without re-reading
	// the scorecard.
	s.StartedAt = time.Now().Add(-firstWriteNoteAt - time.Minute)
	advisory := s.FirstWriteNote("build")
	if advisory == "" {
		t.Fatalf("no advisory at +%d min with no write", int(firstWriteNoteAt.Minutes())+1)
	}
	if !strings.Contains(advisory, "S1") {
		t.Errorf("advisory must name the standard it enforces, got: %q", advisory)
	}
	if !strings.Contains(strings.ToLower(advisory), "write") {
		t.Errorf("advisory must name the action, got: %q", advisory)
	}
	if s.FirstWriteWarnDue("build") {
		t.Fatal("warning due before the warning threshold")
	}

	// Past the warning threshold: the note escalates and the event is due.
	s.StartedAt = time.Now().Add(-firstWriteWarnAt - time.Minute)
	strong := s.FirstWriteNote("build")
	if strong == "" {
		t.Fatalf("no note at +%d min with no write", int(firstWriteWarnAt.Minutes())+1)
	}
	if strong == advisory {
		t.Fatal("note did not escalate past the warning threshold")
	}
	if !s.FirstWriteWarnDue("build") {
		t.Fatal("warning not due at/after the threshold")
	}

	// The warning fires exactly once — a second claim must not re-fire it
	// (FM-L3b: repeated warnings teach the model to ignore gates).
	if !s.MarkFirstWriteWarned() {
		t.Fatal("first claim did not win the latch")
	}
	if s.MarkFirstWriteWarned() {
		t.Fatal("second claim fired the warning twice")
	}

	// The first successful write closes the gate for the turn.
	s.MarkFirstWrite()
	if got := s.FirstWriteNote("build"); got != "" {
		t.Errorf("note survived the first write: %q", got)
	}
	if s.FirstWriteWarnDue("build") {
		t.Error("warning still due after the first write")
	}

	// Plan turns never write — the gate is build-only, like L3's.
	p := NewSession(1, 1, t.TempDir(), nil)
	p.StartedAt = time.Now().Add(-firstWriteWarnAt - time.Minute)
	if got := p.FirstWriteNote("plan"); got != "" {
		t.Errorf("plan turn got the first-write note: %q", got)
	}
	if p.FirstWriteWarnDue("plan") {
		t.Error("plan turn armed the first-write warning")
	}
}

// TestGateNoteOneVoice pins FM-D1b: past +5 min with nothing written, both
// gates are technically due, but the card speaks with one voice — the
// first-write note. Once the first write lands (route still unwritten), the
// route note resumes ownership.
func TestGateNoteOneVoice(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)
	s.StartedAt = time.Now().Add(-firstWriteNoteAt - time.Minute)

	note := s.GateNote("build")
	if note == "" {
		t.Fatal("no gate note past the advisory threshold")
	}
	if !strings.Contains(note, "S1") {
		t.Errorf("first-write note should own the card, got: %q", note)
	}
	if strings.Contains(note, "ROUTE GATE") {
		t.Errorf("route note must not compete while nothing is written, got: %q", note)
	}

	// First write lands, route still unwritten: the route note takes the
	// card back.
	s.MarkFirstWrite()
	s.StartedAt = time.Now().Add(-routeScaffoldWarnAt - time.Minute)
	resumed := s.GateNote("build")
	if !strings.Contains(resumed, "ROUTE GATE") {
		t.Errorf("route note did not resume after the first write, got: %q", resumed)
	}
	if strings.Contains(resumed, "S1") {
		t.Errorf("first-write note must be gone after the first write, got: %q", resumed)
	}
}
