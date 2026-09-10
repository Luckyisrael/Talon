// Package ralph is the H14 fresh-agent lane: N fresh-child rounds against
// the shared workspace toward one immutable objective, for when a single
// context cannot finish ("all verify gates green" after a long build).
//
// The contract (from DSH's ralph tool, adapted to SeekerStudio's loop):
//
//   - The objective is immutable for the whole run. Rounds cannot renegotiate
//     it; if the objective is wrong, the run stops and the human decides.
//   - Each round is a FRESH context: no conversation carryover. The only
//     memory is the shared workspace (files on disk) and the previous
//     round's bounded report — what passed, what remains, the one blocker.
//   - The report crosses rounds in a fixed shape, capped in bytes, so a
//     round reads the state of the world in a page, not a transcript.
//   - maxRounds defaults to 8 (not 64 — our rounds are minutes, not
//     seconds). The cap is the budget law; the offer is always explicit,
//     never automatic: a ralph run costs real tokens.
//   - Terminal conditions: done (objective achieved), blocker (a concrete
//     condition persisted, with the human explanation), or capped (the
//     round budget spent — also honest, also terminal).
package ralph

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Objective is the immutable goal of a run.
type Objective string

// Report is the bounded state that crosses rounds. Fixed shape, capped
// bytes — a page, not a transcript.
type Report struct {
	// Passed lists what verifiably works now (verify gates green, files
	// that compile).
	Passed []string
	// Remaining lists what is left, concretely.
	Remaining []string
	// Blocker is the one concrete thing in the way, when one exists.
	Blocker string
	// Round is which round produced this report.
	Round int
}

// MaxReportBytes caps a report — the fresh context reads a page.
const MaxReportBytes = 4096

// Render formats the report the next round reads.
func (r Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ROUND %d STATE OF THE WORLD\n", r.Round)
	if len(r.Passed) > 0 {
		b.WriteString("Passed (verified, do not redo):\n")
		for _, p := range r.Passed {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	if len(r.Remaining) > 0 {
		b.WriteString("Remaining:\n")
		for _, m := range r.Remaining {
			fmt.Fprintf(&b, "  - %s\n", m)
		}
	}
	if r.Blocker != "" {
		fmt.Fprintf(&b, "The one blocker: %s\n", r.Blocker)
	}
	out := b.String()
	if len(out) > MaxReportBytes {
		out = out[:MaxReportBytes] + "\n…(report capped)\n"
	}
	return out
}

// Runner executes one fresh round: it receives the objective and the
// previous report ("" for round 1), runs to completion against the shared
// workspace, and returns its own report plus whether the objective is done.
type Runner interface {
	RunRound(ctx context.Context, obj Objective, prev Report) (next Report, done bool, err error)
}

// Outcome is the run's terminal state.
type Outcome struct {
	// Done: the objective was achieved within the round budget.
	Done bool
	// Capped: the round budget was spent first — honest, terminal.
	Capped bool
	// Blocker: a concrete condition persisted with the human explanation.
	Blocker string
	// Rounds is how many rounds ran.
	Rounds int
	// Final is the last report.
	Final Report
	// Err carries a runner failure (distinct from blocker/done).
	Err error
}

// Lane runs the fresh-agent loop.
type Lane struct {
	runner   Runner
	maxRounds int
	// reportMu guards the latest report read-modify-write across a
	// concurrent Steer — the report is the only mutable crossing.
	mu     sync.Mutex
	latest Report
	lastAt time.Time
}

// NewLane builds the lane. maxRounds < 1 defaults to 8 — the budget law.
func NewLane(runner Runner, maxRounds int) *Lane {
	if maxRounds < 1 {
		maxRounds = 8
	}
	return &Lane{runner: runner, maxRounds: maxRounds}
}

// Run iterates fresh rounds toward the objective until done, a blocker
// persists, a round errors, or the budget is spent. Each round is a fresh
// context: only the workspace and the previous report cross.
func (l *Lane) Run(ctx context.Context, obj Objective) Outcome {
	var prev Report
	for round := 1; round <= l.maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return Outcome{Rounds: round - 1, Final: prev, Err: err}
		}
		next, done, err := l.runner.RunRound(ctx, obj, prev)
		if err != nil {
			// A runner error ends the run — the round's own outcome is the
			// honest state; retrying inside ralph would hide it.
			return Outcome{Rounds: round, Final: next, Err: err}
		}
		next.Round = round
		l.mu.Lock()
		l.latest = next
		l.lastAt = time.Now()
		l.mu.Unlock()

		if done {
			return Outcome{Done: true, Rounds: round, Final: next}
		}
		// A blocker that persists across a full round is terminal: the next
		// fresh context would hit the same wall. (A round may clear its own
		// blocker; only carrying one forward stops the run.)
		if next.Blocker != "" {
			return Outcome{Blocker: next.Blocker, Rounds: round, Final: next}
		}
		prev = next
	}
	// The budget was spent working — honest, terminal, not a failure.
	return Outcome{Capped: true, Rounds: l.maxRounds, Final: prev}
}

// Latest reports the most recent round's state — the live progress surface.
func (l *Lane) Latest() (Report, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.latest, l.lastAt
}
