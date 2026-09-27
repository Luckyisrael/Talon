package orchestrator

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L3 routes-first gate (ENGINEERING_STANDARDS §6 table, L3; S2/S4 rows).
//
// Failure modes, written first (TEST_RULES §3):
//
//  1. FM-L3a — prose-only ordering. Run 2 (2026-09-26) ran under a prompt
//     that already ordered "write early, not after exhaustive survey" and
//     still landed its first app/ route file at 46m05s: ~16 min of
//     node_modules typings archaeology and infra-first sequencing. A rule
//     the model can skim is not a gate — the clock must live in the loop,
//     read every round.
//  2. FM-L3b — false-positive nagging. The gate must close on ANY
//     app/**.tsx write (the S2 standard is "first route file" — skeleton
//     or full), stay silent inside the 5-minute orientation window, stay
//     silent in plan turns (they never write), ignore errored writes, and
//     fire at most one warning event — or the model learns warnings are
//     noise and the next real gate dies with them.

func TestRouteScaffoldLadder(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)

	// Inside the orientation window: silence. The first minutes of a build
	// legitimately belong to reading the map.
	s.StartedAt = time.Now()
	if got := s.RouteScaffoldNote("build"); got != "" {
		t.Fatalf("note inside the %v orientation window: %q", routeScaffoldNoteAt, got)
	}
	if s.RouteScaffoldWarnDue("build") {
		t.Fatal("warning due inside the orientation window")
	}

	// Past the advisory threshold: a note naming the standard and the action.
	s.StartedAt = time.Now().Add(-routeScaffoldNoteAt - time.Minute)
	advisory := s.RouteScaffoldNote("build")
	if advisory == "" {
		t.Fatalf("no advisory note at +%d min", int(routeScaffoldNoteAt.Minutes())+1)
	}

	// Past the warning threshold: the note escalates and the event is due.
	s.StartedAt = time.Now().Add(-routeScaffoldWarnAt - time.Minute)
	strong := s.RouteScaffoldNote("build")
	if strong == "" {
		t.Fatalf("no note at +%d min", int(routeScaffoldWarnAt.Minutes())+1)
	}
	if strong == advisory {
		t.Fatal("note did not escalate past the warning threshold")
	}
	if !s.RouteScaffoldWarnDue("build") {
		t.Fatal("warning not due at/after the threshold")
	}

	// One route write closes the gate for the rest of the turn (FM-L3b).
	s.MarkRouteScaffold()
	if got := s.RouteScaffoldNote("build"); got != "" {
		t.Fatalf("note after a route write: %q", got)
	}
	if s.RouteScaffoldWarnDue("build") {
		t.Fatal("warning still due after a route write")
	}
}

func TestRouteScaffoldSilentInPlanMode(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)
	s.StartedAt = time.Now().Add(-time.Hour)
	if got := s.RouteScaffoldNote("plan"); got != "" {
		t.Fatalf("plan turn carries a route note: %q", got)
	}
	if s.RouteScaffoldWarnDue("plan") {
		t.Fatal("plan turn made the warning due")
	}
}

func TestRouteWarnLatchFiresOnce(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)
	s.StartedAt = time.Now().Add(-time.Hour)
	if !s.RouteScaffoldWarnDue("build") {
		t.Fatal("expected due at one hour without a route")
	}
	if !s.MarkRouteWarned() {
		t.Fatal("first warn call must fire")
	}
	if s.MarkRouteWarned() {
		t.Fatal("second warn call must not re-fire — every-round warnings spam the SSE stream (S12)")
	}
}

// The detection hook: fsWrite is what the loop already runs on every
// successful write/edit with a parsed path — the gate reads the same event.
func TestFSWriteClosesRouteGate(t *testing.T) {
	s := NewSession(1, 1, t.TempDir(), nil)
	s.StartedAt = time.Now().Add(-time.Hour)
	seam := loopSeam{} // driver off: legacy bookkeeping only, no publish

	// A non-route write leaves the gate armed.
	seam.fsWrite(s, ai.ToolUseBlock{Name: "write_file",
		Input: json.RawMessage(`{"path":"src/store/appState.ts"}`)}, false)
	if !s.RouteScaffoldWarnDue("build") {
		t.Fatal("non-route write closed the route gate")
	}
	// An errored write never counts.
	seam.fsWrite(s, ai.ToolUseBlock{Name: "write_file",
		Input: json.RawMessage(`{"path":"app/broken.tsx"}`)}, true)
	if !s.RouteScaffoldWarnDue("build") {
		t.Fatal("errored write closed the route gate")
	}
	// The first successful route write — skeleton or full — closes it.
	seam.fsWrite(s, ai.ToolUseBlock{Name: "edit_file",
		Input: json.RawMessage(`{"path":"app/(tabs)/index.tsx"}`)}, false)
	if s.RouteScaffoldWarnDue("build") {
		t.Fatal("route write did not close the gate")
	}
}

func TestRoutePathPatternMatchesScorecardS2(t *testing.T) {
	// Parity with the scorecard's SCREEN_RE: the ladder enforces exactly
	// what S2 measures, or it can pass a run the scorecard fails.
	for _, p := range []string{"app/index.tsx", "app/(tabs)/index.tsx", "src/app/home.tsx", "app/_layout.tsx"} {
		if !routePathRe.MatchString(p) {
			t.Errorf("routePathRe misses %q", p)
		}
	}
	for _, p := range []string{"app.json", "src/components/Button.tsx", "App.tsx", "app/utils/format.ts"} {
		if routePathRe.MatchString(p) {
			t.Errorf("routePathRe matches non-route %q", p)
		}
	}
}
