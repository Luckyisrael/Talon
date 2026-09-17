// Package goals is the H11 durable objective: one current goal per
// project, surviving restarts, mutated only through compare-and-swap so a
// stale view can never clobber newer state.
//
// The contract (from DSH's goal service, adapted to Talon's shape):
//
//   - A goal is (objective text, phase, rounds used, revision). One goal is
//     active per project at a time; creating a new one completes the old.
//   - Every mutation carries the revision it read; applying it against a
//     moved revision fails with ErrStale — the caller re-reads and retries.
//     This is the CAS law that makes concurrent UI + loop safe.
//   - A goal ends complete (objective achieved) or blocked (a concrete
//     condition persisted across rounds, with a human explanation). "Hard"
//     is not a terminal state here — the loop keeps rounds going until the
//     cap; blocked is the honest "cannot continue" answer.
//   - Round accounting is append-only: the loop records each round's pass
//     through the goal, and a resumed goal counts from where it stopped —
//     a restart must never double-apply or re-run completed rounds.
package goals

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrStale is the CAS refusal: the revision moved since this actor read.
var ErrStale = errors.New("GOAL_STALE: the goal changed since you read it — re-read and retry")

// ErrNoGoal is the "nothing active" answer, not an error condition.
var ErrNoGoal = errors.New("no active goal for this project")

// Phase is the goal's lifecycle stage.
type Phase string

const (
	PhaseActive   Phase = "active"
	PhaseComplete Phase = "complete"
	PhaseBlocked  Phase = "blocked"
	// PhasePaused is a deliberate stop for a human: the goal waits on a
	// decision, an answer, or a resource, and resumes when it arrives. It is
	// not a failure — blocked names a condition that persisted; paused names
	// one that is waiting. Keeping them apart is what lets an operator read a
	// paused goal as "someone needs to act" and a blocked one as "this needs
	// rework" — conflating them makes every question look like a dead end.
	PhasePaused Phase = "paused"
)

// blockedAfterConsecutiveRounds is the floor on premature blocking: a goal may
// not be marked blocked until it has stalled for at least this many consecutive
// rounds. One bad round is a hiccup — the model hit a rate limit, misread a
// file, or took a dead end it will correct. Letting it declare blocked on that
// evidence turns recoverable friction into an abandoned objective, and trains
// the person watching the chip that "blocked" means nothing. The floor is what
// makes the blocked phase worth reading.
const blockedAfterConsecutiveRounds = 3

// Goal is one durable objective.
type Goal struct {
	ID        int64
	Project   int64
	Objective string
	Phase     Phase
	// Rounds is how many loop rounds have run against this goal — the
	// honest counter the chip renders and the cap reads.
	Rounds int
	// MaxRounds is the round cap this goal was created with.
	MaxRounds int
	// Revision is the CAS token: every applied mutation increments it.
	Revision int64
	// BlockedReason carries the human explanation when Phase == blocked.
	BlockedReason string
	// StalledRounds counts consecutive rounds that produced no forward
	// movement. It is the evidence behind a blocked declaration, and it resets
	// the moment a round advances the goal — a goal that stalls, recovers, and
	// stalls again must accumulate from zero, not from history.
	StalledRounds int
	// PausedReason carries what the paused goal is waiting on.
	PausedReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Mutation is one proposed state change, keyed on the read revision.
type Mutation struct {
	GoalID   int64
	Revision int64
	// Phase change, when this mutation ends the goal.
	Phase Phase
	// Rounds adds this many completed rounds (append-only accounting).
	AddRounds int
	// BlockedReason sets the explanation (with Phase blocked).
	BlockedReason string
	// PausedReason sets what a paused goal is waiting on (with Phase paused).
	PausedReason string
	// AddStalled accumulates consecutive stalled rounds; a non-negative
	// StallReset clears them.
	AddStalled int
	StallReset bool
}

// Store is the persistence contract. The in-memory store is the first
// backend; SQLite lands beside it when the API wires goals.
type Store interface {
	// Create stores a new goal for the project, completing any active one.
	Create(g Goal) (Goal, error)
	// Current returns the project's active goal, or ErrNoGoal.
	Current(project int64) (Goal, error)
	// Apply performs the CAS mutation.
	Apply(m Mutation) (Goal, error)
}

// MemStore is the in-memory backend — tests and headless runs.
type MemStore struct {
	mu     sync.Mutex
	goals  map[int64]*Goal // by ID
	byProj map[int64]int64 // project → active goal ID
	nextID int64
}

// NewMemStore builds the in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{goals: map[int64]*Goal{}, byProj: map[int64]int64{}, nextID: 1}
}

// Create stores a goal and completes the project's previous active one.
func (s *MemStore) Create(g Goal) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g.ID = s.nextID
	s.nextID++
	g.Phase = PhaseActive
	g.Revision = 1
	g.CreatedAt = time.Now()
	g.UpdatedAt = g.CreatedAt
	if g.MaxRounds <= 0 {
		g.MaxRounds = 60
	}
	// One active goal per project: the old one ends complete-by-supersession.
	if old, ok := s.byProj[g.Project]; ok {
		if oldGoal, exists := s.goals[old]; exists && oldGoal.Phase == PhaseActive {
			oldGoal.Phase = PhaseComplete
			oldGoal.UpdatedAt = time.Now()
		}
	}
	stored := g
	s.goals[g.ID] = &stored
	s.byProj[g.Project] = g.ID
	return g, nil
}

// Current returns the active goal.
func (s *MemStore) Current(project int64) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byProj[project]
	if !ok {
		return Goal{}, ErrNoGoal
	}
	g, exists := s.goals[id]
	if !exists {
		return Goal{}, ErrNoGoal
	}
	return *g, nil
}

// Apply performs the CAS mutation and returns the goal's new state.
func (s *MemStore) Apply(m Mutation) (Goal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.goals[m.GoalID]
	if !ok {
		return Goal{}, ErrNoGoal
	}
	if g.Revision != m.Revision {
		return Goal{}, ErrStale
	}
	if m.AddRounds > 0 {
		g.Rounds += m.AddRounds
	}
	if m.StallReset {
		g.StalledRounds = 0
	}
	if m.AddStalled > 0 {
		g.StalledRounds += m.AddStalled
	}
	if m.Phase != "" {
		g.Phase = m.Phase
	}
	if m.BlockedReason != "" {
		g.BlockedReason = m.BlockedReason
	}
	if m.PausedReason != "" {
		g.PausedReason = m.PausedReason
	}
	g.Revision++
	g.UpdatedAt = time.Now()
	return *g, nil
}

// Service is the goal seam the loop and UI share.
type Service struct {
	store Store
}

// NewService builds the service over a store.
func NewService(st Store) *Service { return &Service{store: st} }

// Start creates the project's current goal (superseding any active one).
func (s *Service) Start(project int64, objective string, maxRounds int) (Goal, error) {
	if objective == "" {
		return Goal{}, errors.New("a goal needs objective text")
	}
	return s.store.Create(Goal{Project: project, Objective: objective, MaxRounds: maxRounds})
}

// Current returns the active goal.
func (s *Service) Current(project int64) (Goal, error) {
	return s.store.Current(project)
}

// RecordRounds appends completed rounds under CAS, retrying once on stale
// (the loop is the only writer during a run; a UI read cannot race a write
// here because reads do not mutate).
func (s *Service) RecordRounds(project int64, rounds int) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, AddRounds: rounds})
}

// Complete ends the goal as achieved.
func (s *Service) Complete(project int64) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, Phase: PhaseComplete})
}

// Block ends the goal as blocked with the concrete condition and a human
// explanation. The condition must be stable across restarts — it names
// what persisted, not what flapped.
//
// The floor: a goal that has not stalled for blockedAfterConsecutiveRounds
// consecutive rounds may not be blocked — one bad round is recoverable
// friction, and marking it blocked devalues the phase for every goal after it.
// The caller that wants to honor this reads RecordStall first and only calls
// Block when the floor is met; this method is the last line of defence and
// refuses the premature call.
func (s *Service) Block(project int64, condition string) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	if strings.TrimSpace(condition) == "" {
		return Goal{}, errors.New("a blocked goal needs the concrete blocking condition")
	}
	if g.StalledRounds < blockedAfterConsecutiveRounds {
		return g, ErrPrematureBlock
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, Phase: PhaseBlocked,
		BlockedReason: condition})
}

// ErrPrematureBlock names the floor: the goal has not stalled long enough for
// "blocked" to be honest. Callers surface it as "still working" rather than a
// failure — the goal stays active and the loop continues.
var ErrPrematureBlock = errors.New("the goal has not stalled for enough consecutive rounds to be blocked")

// Pause stops the goal for a human decision without marking it a failure. What
// it waits on is recorded so a paused goal reads as an ask, not a dead end.
func (s *Service) Pause(project int64, waitingOn string) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	if strings.TrimSpace(waitingOn) == "" {
		return Goal{}, errors.New("a paused goal needs what it is waiting on")
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, Phase: PhasePaused,
		PausedReason: waitingOn})
}

// Resume returns a paused goal to active. A blocked goal does not resume — it
// ended for a reason that persisted; fixing that reason starts a new goal.
func (s *Service) Resume(project int64) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	if g.Phase != PhasePaused {
		return g, ErrNotPaused
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, Phase: PhaseActive})
}

// ErrNotPaused names a resume attempted on a goal that is not paused.
var ErrNotPaused = errors.New("only a paused goal can be resumed")

// RecordStall accumulates the evidence a Block needs, or clears it. A round
// that advanced the goal resets the counter: stall-then-recover-then-stall
// accumulates from zero, so a goal that repeatedly recovers is never blocked by
// the sum of its bad moments.
func (s *Service) RecordStall(project int64, advanced bool) (Goal, error) {
	g, err := s.store.Current(project)
	if err != nil {
		return Goal{}, err
	}
	if advanced {
		return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, StallReset: true})
	}
	return s.store.Apply(Mutation{GoalID: g.ID, Revision: g.Revision, AddStalled: 1})
}

// StalledEnough reports whether the floor is met — the check a caller makes
// before it even considers declaring blocked.
func StalledEnough(g Goal) bool {
	return g.StalledRounds >= blockedAfterConsecutiveRounds
}

// ChipText renders the goal chip line for the chat rail: objective, rounds,
// phase; the blocked explanation verbatim.
func ChipText(g Goal) string {
	switch g.Phase {
	case PhaseBlocked:
		return fmt.Sprintf("● goal blocked (%d/%d rounds) — %s", g.Rounds, g.MaxRounds, g.BlockedReason)
	case PhasePaused:
		return fmt.Sprintf("● goal paused (%d/%d rounds) — waiting on %s", g.Rounds, g.MaxRounds, g.PausedReason)
	case PhaseComplete:
		return fmt.Sprintf("● goal complete (%d rounds)", g.Rounds)
	default:
		return fmt.Sprintf("● goal active (%d/%d rounds) — %s", g.Rounds, g.MaxRounds, g.Objective)
	}
}

// CapReached reports whether the goal spent its round budget — the loop's
// stop-check for objective-driven runs.
func CapReached(g Goal) bool {
	return g.Rounds >= g.MaxRounds
}
