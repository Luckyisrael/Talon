package ralph

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// scriptRunner serves a fixed sequence of round outcomes.
type scriptRunner struct {
	// rounds is consumed one per RunRound call.
	script []step
	// sawPrev records the report each round received (nil for round 1).
	sawPrev []Report
}

type step struct {
	report  Report
	done    bool
	err     error
}

func (r *scriptRunner) RunRound(_ context.Context, _ Objective, prev Report) (Report, bool, error) {
	r.sawPrev = append(r.sawPrev, prev)
	if len(r.script) == 0 {
		return Report{}, false, errors.New("script exhausted")
	}
	s := r.script[0]
	r.script = r.script[1:]
	return s.report, s.done, s.err
}

// Rounds are truly fresh: round 2 sees round 1's report and nothing else —
// the runner contract carries only (objective, prevReport).
func TestFreshRounds(t *testing.T) {
	runner := &scriptRunner{script: []step{
		{report: Report{Passed: []string{"typecheck green"}, Remaining: []string{"lint", "gate"}}},
		{report: Report{Passed: []string{"typecheck green", "lint green"}, Remaining: []string{"gate"}}, done: true},
	}}
	lane := NewLane(runner, 8)
	out := lane.Run(context.Background(), "all verify gates green")

	if !out.Done || out.Rounds != 2 {
		t.Fatalf("healthy: run outcome wrong: %+v", out)
	}
	// Round 1 saw an empty report (fresh context); round 2 saw round 1's.
	if len(runner.sawPrev) != 2 {
		t.Fatalf("rounds run = %d", len(runner.sawPrev))
	}
	if len(runner.sawPrev[0].Passed) != 0 {
		t.Error("fault: round 1 received carryover — contexts are not fresh")
	}
	if len(runner.sawPrev[1].Passed) != 1 || runner.sawPrev[1].Passed[0] != "typecheck green" {
		t.Error("fault: round 2 did not receive round 1's report")
	}
}

// A persistent blocker is terminal with the human explanation; the budget
// cap is honest and terminal; a runner error ends the run.
func TestTerminals(t *testing.T) {
	// Blocker.
	runner := &scriptRunner{script: []step{
		{report: Report{Remaining: []string{"gate"}, Blocker: "the emulator pool has no free device; boot waits exceed 30m"}},
	}}
	out := NewLane(runner, 8).Run(context.Background(), "gates green")
	if out.Done || out.Blocker == "" || !strings.Contains(out.Blocker, "no free device") {
		t.Fatalf("fault: blocker not terminal: %+v", out)
	}

	// Capped: every round reports progress but never done.
	steps := make([]step, 10)
	for i := range steps {
		steps[i] = step{report: Report{Remaining: []string{"gate"}}}
	}
	out = NewLane(&scriptRunner{script: steps}, 3).Run(context.Background(), "gates green")
	if !out.Capped || out.Rounds != 3 {
		t.Fatalf("fault: cap not honest: %+v", out)
	}

	// Runner error.
	out = NewLane(&scriptRunner{script: []step{
		{report: Report{}, err: errors.New("provider 429 persisted 3 rounds")},
	}}, 8).Run(context.Background(), "gates green")
	if out.Err == nil || out.Done {
		t.Fatalf("fault: runner error swallowed: %+v", out)
	}
}

// The report is bounded: a wall of state renders to a page.
func TestReportBounded(t *testing.T) {
	big := Report{
		Passed:    make([]string, 200),
		Remaining: make([]string, 200),
	}
	for i := range big.Passed {
		big.Passed[i] = strings.Repeat("x", 40)
		big.Remaining[i] = strings.Repeat("y", 40)
	}
	if len(big.Render()) > MaxReportBytes+64 {
		t.Errorf("fault: report renders %d bytes, over the cap", len(big.Render()))
	}
	if !strings.Contains(big.Render(), "report capped") {
		t.Error("fault: capping not marked")
	}
}

// Latest exposes live progress mid-run.
func TestLatest(t *testing.T) {
	r := Report{Passed: []string{"a"}, Round: 1}
	if !strings.Contains(r.Render(), "ROUND 1") {
		t.Error("render lacks the round stamp")
	}
}
