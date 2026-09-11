package orchestrator

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/build"
	"github.com/luckyisrael/talon/backend/internal/events"
	harnessagent "github.com/luckyisrael/talon/backend/internal/harness/agent"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/runtime"
	"github.com/luckyisrael/talon/backend/internal/store"
)

type Phase string

// The phases a turn moves through. STAGE_WRITE and PROMOTE used to sit between
// GENERATE and VERIFY_STATIC, back when writes landed in a .staging directory and
// were copied over the workspace afterwards. Nothing mounted that directory, so
// the agent and the file API disagreed about what the project contained; tools
// now write to the workspace directly and checkpoints provide the undo that
// staging was supposed to.
const (
	PhaseIntake        Phase = "INTAKE"
	PhasePlan          Phase = "PLAN"
	PhaseContextBuild  Phase = "CONTEXT_BUILD"
	PhaseResearch      Phase = "RESEARCH"
	PhaseGenerate      Phase = "GENERATE"
	PhaseVerifyStatic  Phase = "VERIFY_STATIC"
	PhaseVerifyRuntime Phase = "VERIFY_RUNTIME"
	PhaseWaiting       Phase = "WAITING_FOR_USER"
	PhaseDone          Phase = "DONE"
	PhaseFailed        Phase = "FAILED_NEEDS_USER"
)

type SessionState struct {
	ID            int64
	ProjectID     int64
	WorkspacePath string
	CurrentPhase  Phase
	PatchHistory  []PatchRecord
	StartedAt     time.Time
	Emitter       *events.SessionEmitter

	// VerifyAttempts is the number of times this turn has been sent back for
	// repair after a failed verification. Owned here — the session is the single
	// source of truth — so a resumable turn can carry the count forward instead of
	// starting over at zero. The loop's verify gate reads it; it is never reset
	// within a turn because the same code stays under repair until it compiles.
	VerifyAttempts int

	// Phase 7: Engineering Intelligence
	ReadFiles   map[string]bool              // files read in current turn (enforces read-before-write)
	ADRs        []store.ArchitectureDecision // loaded ADRs for this project
	Memory      []store.ProjectMemory        // loaded project memory
	TaskTracker *TaskTracker                 // visible task breakdown

	// SeenSkills tracks skill bodies already delivered this session, whether the
	// model read one explicitly or a trigger auto-attached it. Unlike ReadFiles it
	// is NOT cleared per turn: a skill's guidance, once given, holds for the rest
	// of the build, so re-injecting it would just burn context.
	SeenSkills map[string]bool

	// projectMap is a snapshot of the workspace's structure (files, their
	// exported symbols, and their imports) injected into the cached
	// system-prompt prefix. It grounds the model in what already exists so it
	// does not guess or re-derive the layout. It refreshes itself: the loop
	// compares a cheap fingerprint of the tree each round and rebuilds the map
	// only when files actually changed, so unchanged rounds keep the cached
	// prefix byte-stable.
	projectMap   string
	projectMapFP string

	// KnownFiles tracks files the model has read or written this session, with
	// the mtime at last contact. A re-read of an unchanged file gets a note
	// pointing at the transcript instead of a second full body — the content is
	// already in context, so fetching it again only burns a round.
	KnownFiles map[string]int64

	// WriteCounts counts writes per file within the current turn. The third
	// rewrite of the same file triggers a stop-and-reread nudge: beyond that
	// point the model is thrashing, not iterating.
	WriteCounts map[string]int

	// ProbeCounts counts inspections of the same third-party package (node_modules
	// targets) within the current turn. API discovery is legitimate — unknown
	// shapes must be confirmed, not guessed — but the third look at the same
	// package means the answer is known and should be recorded (write_memory)
	// instead of re-probed.
	ProbeCounts map[string]int

	// CurrentRound is the loop's round number, updated at the top of every
	// round. Tool-result notes name it ("unchanged since round 7") so the model
	// can find the original result in its transcript instead of re-fetching.
	CurrentRound int

	// BuildRequested records whether the user's prompt asked for a device
	// build. build_apk (a 10-minute gradle run) is refused without it — the
	// E2E caught the agent spending a full build unprompted on a code task.
	BuildRequested bool

	// WritesSinceVerify counts successful file writes since the last passing
	// verification this turn. The loop enforces periodic verification off this
	// counter — prompt guidance alone demonstrably fails (a 60-round turn with
	// two verifications), so the count lives here where the model cannot skim
	// past it. Reset on every passing check and at every turn start.
	WritesSinceVerify int

	// FailureCooldowns tracks how many rounds a known-failure reminder should
	// be suppressed after the model has already been told the fix. Keys are
	// toolName+":"+normalizedError. Value is remaining rounds; zero means active.
	FailureCooldowns map[string]int

	// toolRepeats counts same-tool calls within the turn, feeding the H3
	// advisory repeat reminder.
	toolRepeats map[string]int

	// RuntimePatterns holds failure patterns loaded from project memory on
	// session start. These are merged with the default index at lookup time.
	RuntimePatterns []knowledge.FailurePattern

	// CrashEntries is the run-scoped crash index: one entry per failed
	// device step (boot, vision, interact, wallet tx) with its log excerpt.
	// It dies with the run — the next turn re-derives it from fresh logs.
	CrashEntries []runtime.CrashEntry

	// PatchBuffer is the try_edit pre-image store. Nil unless the patch
	// buffer lane is enabled for the session — nil means try_edit behaves
	// as the plain underlying edit, identical to today.
	PatchBuffer *build.PatchBuffer

	// steerLane is the H2 mid-turn message lane: user messages that arrive
	// while this turn is running. Steers land in the next model request; injects
	// ride along without redirecting. The loop drains it at round boundaries —
	// a steer delivered between requests is picked up before the next one.
	steerLane []harnessagent.Inbox
	steerSeq  int64

	// skippedGates accumulates ladder stages that reported unavailable in
	// the current sweep, so a verified outcome can name what never ran.
	skippedGates []string

	// persistTask, when set, mirrors every MarkPlanTask write into the
	// durable plan_task_status table — the project's canonical backlog.
	// Nil in tests and sessions without a store; the in-memory tracker
	// remains the working copy either way.
	persistTask func(projectID int64, t Task, byPrompt int64)

	mu sync.Mutex
}

type PatchRecord struct {
	Phase     Phase
	Files     []string
	Error     string
	Timestamp time.Time
}

// TaskTracker maintains a visible task breakdown during multi-step work.
//
// version bumps on every real mutation (a task added, or a task whose status or
// result actually changed). The loop's rebuild guard tracks it so the system
// prompt — which renders the plan checklist — is re-sent only when the rendered
// plan would differ, keeping the cached prefix intact across rounds where nothing
// changed.
type TaskTracker struct {
	Tasks   []Task `json:"tasks"`
	version int
	mu      sync.Mutex
}

type Task struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"` // "pending", "in_progress", "completed", "failed"
	Result      string `json:"result,omitempty"`
}

func NewSession(id, projectID int64, workspacePath string, emitter *events.SessionEmitter) *SessionState {
	return &SessionState{
		ID:               id,
		ProjectID:        projectID,
		WorkspacePath:    workspacePath,
		CurrentPhase:     PhaseIntake,
		StartedAt:        time.Now(),
		Emitter:          emitter,
		ReadFiles:        make(map[string]bool),
		SeenSkills:       make(map[string]bool),
		KnownFiles:       make(map[string]int64),
		WriteCounts:      make(map[string]int),
		ProbeCounts:      make(map[string]int),
		FailureCooldowns: make(map[string]int),
		TaskTracker:      NewTaskTracker(),
	}
}

func NewTaskTracker() *TaskTracker {
	return &TaskTracker{
		Tasks: make([]Task, 0),
	}
}

func (tt *TaskTracker) AddTask(id, description string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	tt.Tasks = append(tt.Tasks, Task{
		ID:          id,
		Description: description,
		Status:      "pending",
	})
	tt.version++
}

// UpdateTask sets a task's status and result, returning true only when something
// actually changed. A redundant update (same status and result) is a no-op that
// leaves version untouched, so the rebuild guard does not bust the prompt cache
// for a status the model re-asserted without moving.
func (tt *TaskTracker) UpdateTask(id, status, result string) bool {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	for i := range tt.Tasks {
		if tt.Tasks[i].ID == id {
			if tt.Tasks[i].Status == status && tt.Tasks[i].Result == result {
				return false
			}
			tt.Tasks[i].Status = status
			tt.Tasks[i].Result = result
			tt.version++
			return true
		}
	}
	return false
}

// Version returns a counter that bumps on every real mutation. The loop's rebuild
// guard tracks it to decide when the plan checklist in the cached system prompt is
// stale.
func (tt *TaskTracker) Version() int {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	return tt.version
}

func (tt *TaskTracker) GetTasks() []Task {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	tasks := make([]Task, len(tt.Tasks))
	copy(tasks, tt.Tasks)
	return tasks
}

func (tt *TaskTracker) Clear() {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	if len(tt.Tasks) > 0 {
		tt.version++
	}
	tt.Tasks = make([]Task, 0)
}

func (s *SessionState) TransitionTo(phase Phase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentPhase = phase
	s.Emitter.EmitPhase(events.Phase(phase), "started")
}

func (s *SessionState) RecordPatch(files []string, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PatchHistory = append(s.PatchHistory, PatchRecord{
		Phase:     s.CurrentPhase,
		Files:     files,
		Error:     errMsg,
		Timestamp: time.Now(),
	})
}

// GetPhase reports the current build phase.
func (s *SessionState) GetPhase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CurrentPhase
}

// GetVerifyAttempts returns how many times this turn has been sent back for
// repair after a failed verification. The loop's verify gate reads it before
// running the ladder; the count lives on the session so a resumable turn keeps it.
func (s *SessionState) GetVerifyAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.VerifyAttempts
}

// IncrementVerifyAttempt records one more failed verification. Returns the new
// count so the caller can log or gate on it.
func (s *SessionState) IncrementVerifyAttempt() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.VerifyAttempts++
	return s.VerifyAttempts
}

// MarkFileRead records that a file has been read in the current turn.
func (s *SessionState) MarkFileRead(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReadFiles[path] = true
}

// HasReadFile checks if a file has been read in the current turn.
func (s *SessionState) HasReadFile(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ReadFiles[path]
}

// ClearReadFiles resets the read tracking for a new turn.
func (s *SessionState) ClearReadFiles() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReadFiles = make(map[string]bool)
}

// maxKnownFiles bounds the cross-turn read cache. Each entry is a path plus an
// mtime (~60 bytes in the worst case), so the cap holds the whole structure
// under 10 KB even if it never appears in the prompt at all.
const maxKnownFiles = 150

// KnownFileMtime returns the file's mtime as the model last saw it (read or
// written), or 0 when unknown — the H5 version stamp the write guard compares
// against the on-disk truth.
func (s *SessionState) KnownFileMtime(path string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.KnownFiles == nil {
		return 0
	}
	return s.KnownFiles[path]
}

// NoteFileKnown records that path was read or written with the given mtime.
// When the cache is full the recording is skipped — an unbounded map is worse
// than a missing hint.
func (s *SessionState) NoteFileKnown(path string, mtime int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.KnownFiles[path]; !ok && len(s.KnownFiles) >= maxKnownFiles {
		return
	}
	s.KnownFiles[path] = mtime
}

// KnownFresh reports whether path was previously read or written and has not
// changed since (same mtime). A fresh file's contents are already in the
// model's transcript, so fetching them again only burns a round.
func (s *SessionState) KnownFresh(path string, mtime int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	known, ok := s.KnownFiles[path]
	return ok && known == mtime
}

// NoteFileWrite counts another write to path within the current turn and
// returns the new count. The caller nudges the model to stop and re-read the
// errors once the count reaches rewriteWatchThreshold.
func (s *SessionState) NoteFileWrite(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WriteCounts[path]++
	return s.WriteCounts[path]
}

// ResetWriteCounts clears the per-turn rewrite counters. Called with
// ClearReadFiles at the loop top: a new turn is a new file, a new chance.
func (s *SessionState) ResetWriteCounts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WritesSinceVerify = 0
	s.WriteCounts = make(map[string]int)
	s.ProbeCounts = make(map[string]int)
}

// CooldownLeft returns the remaining rounds a known-failure reminder is
// suppressed. Zero means active (fire the reminder).
func (s *SessionState) CooldownLeft(tool, norm string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.FailureCooldowns[tool+":"+norm]
}

// NoteCooldown sets the suppression window for a known failure after the
// reminder has been delivered.
func (s *SessionState) NoteCooldown(tool, norm string, rounds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FailureCooldowns[tool+":"+norm] = rounds
}

// TickCooldowns decrements all active cooldowns by one round.
func (s *SessionState) TickCooldowns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.FailureCooldowns {
		if v <= 1 {
			delete(s.FailureCooldowns, k)
		} else {
			s.FailureCooldowns[k] = v - 1
		}
	}
}

// NoteCrash appends a run-scoped crash entry. The excerpt should already be
// trimmed (CrashExcerpt) — the index is for triage, not full logs.
func (s *SessionState) NoteCrash(e runtime.CrashEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Round == 0 {
		e.Round = s.CurrentRound
	}
	s.CrashEntries = append(s.CrashEntries, e)
}

// CrashIndex renders the run's crash entries for prompt injection: one line
// per failed device step, newest last.
func (s *SessionState) CrashIndex() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.CrashEntries) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range s.CrashEntries {
		label := e.Tool
		if e.Tx != "" {
			label += " [" + e.Tx + "]"
		}
		b.WriteString("- round " + strconv.Itoa(e.Round) + " " + label + ": " + firstLine(e.Excerpt) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// probeConsolidateThreshold is the inspection count that triggers the
// record-and-move-on nudge: two looks confirm, the third repeats.
const probeConsolidateThreshold = 3

// NoteProbe records another inspection of a third-party package target and
// returns the new count for this turn.
func (s *SessionState) NoteProbe(target string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ProbeCounts[target]++
	return s.ProbeCounts[target]
}

// SetBuildRequested records whether this turn may run device builds.
func (s *SessionState) SetBuildRequested(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.BuildRequested = v
}

// WantsBuild reports whether the user asked for a device build this turn.
func (s *SessionState) WantsBuild() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.BuildRequested
}

// SetRound records the loop's current round number for result notes.
func (s *SessionState) SetRound(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentRound = n
}

// GetRound returns the loop's current round number.
func (s *SessionState) GetRound() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CurrentRound
}

// Deliver appends one mid-turn message to the steer lane (the harness
// agent.Steerable seam). Safe from any goroutine — the SSE steer route calls
// it while the loop is between requests. The arrival sequence is honest
// ordering, not a timestamp: two steers in one round keep their order.
func (s *SessionState) Deliver(class harnessagent.Class, text string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steerSeq++
	s.steerLane = append(s.steerLane, harnessagent.Inbox{Class: class, Text: text, At: s.steerSeq})
	return s.steerSeq
}

// DrainSteers returns and clears the pending mid-turn messages. The loop
// calls it at every round boundary: a steer delivered while the model was
// generating lands in the very next request, never later.
func (s *SessionState) DrainSteers() []harnessagent.Inbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.steerLane
	s.steerLane = nil
	return out
}

// verifyEnforceThreshold is the write count that triggers mandatory
// verification: roughly two screens' worth of code. Small enough to catch
// drift early, large enough not to nag mid-thought.
const verifyEnforceThreshold = 8

// NoteWrite records one successful file write, returning the writes-since-
// verify count. The loop appends a verify-now nudge when the count reaches
// verifyEnforceThreshold, and names the changed files past twice that.
func (s *SessionState) NoteWrite() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WritesSinceVerify++
	return s.WritesSinceVerify
}

// NoteVerified resets the writes-since-verify count after a passing check.
func (s *SessionState) NoteVerified() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.WritesSinceVerify = 0
}

// WritesUnverified reports the current writes-since-verify count.
func (s *SessionState) WritesUnverified() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.WritesSinceVerify
}

// rewriteWatchThreshold is the rewrite count that triggers the stop-and-reread
// nudge: two rewrites are iteration, the third is thrash.
const rewriteWatchThreshold = 3

// repeatReminderThresholds are the same-tool call counts that fire the
// advisory repeat reminder (H3, generalized from the write-watch): three
// repeats is suspicious, five is a loop, eight is pathological. The reminder
// is advisory — it rides the tool_result, the model keeps control — unlike the
// file-write nudge which names a concrete corrective action.
var repeatReminderThresholds = map[int]bool{3: true, 5: true, 8: true}

// ResetToolRepeats clears the per-turn repeat counters at turn start.
func (s *SessionState) ResetToolRepeats() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolRepeats = map[string]int{}
}

// NoteToolRepeat counts one more call of the named tool this turn and returns
// the count. The loop consults repeatReminderThresholds for the reminder.
func (s *SessionState) NoteToolRepeat(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolRepeats == nil {
		s.toolRepeats = map[string]int{}
	}
	s.toolRepeats[name]++
	return s.toolRepeats[name]
}

// repeatReminder is the advisory text for a repeated tool. Escalating, honest,
// never more than one line — a wall of text gets skimmed, a line gets read.
func repeatReminder(name string, count int) string {
	switch count {
	case 3:
		return "\n\nNote: this is your third " + name + " call this turn. If the earlier results answer your question, re-read them from the transcript instead of calling again."
	case 5:
		return "\n\nNote: fifth " + name + " call this turn — you are repeating yourself. State what you are looking for and read it from the results you already have."
	default:
		return "\n\nNote: eighth " + name + " call this turn. Something is wrong with the approach — stop, describe what you actually need, and step back."
	}
}

// MarkSkillSeen records that a skill's body has been delivered this session and
// reports whether this was the first time. Auto-attach injects only when it gets
// true back, so a body reaches the model at most once regardless of how many tool
// calls trip its triggers. It is safe under the parallel read-only tool execution.
func (s *SessionState) MarkSkillSeen(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SeenSkills[name] {
		return false
	}
	s.SeenSkills[name] = true
	return true
}

// LoadADRs loads the Architecture Decision Records for this project.
func (s *SessionState) LoadADRs(adrs []store.ArchitectureDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ADRs = adrs
}

// GetADRs returns the loaded ADRs.
func (s *SessionState) GetADRs() []store.ArchitectureDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	adrs := make([]store.ArchitectureDecision, len(s.ADRs))
	copy(adrs, s.ADRs)
	return adrs
}

// LoadMemory loads the project memory for this project.
func (s *SessionState) LoadMemory(memory []store.ProjectMemory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Memory = memory
}

// GetMemory returns the loaded project memory.
func (s *SessionState) GetMemory() []store.ProjectMemory {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory := make([]store.ProjectMemory, len(s.Memory))
	copy(memory, s.Memory)
	return memory
}

// SetProjectMap stores the build-start project-structure snapshot. It is set once,
// before the loop starts, by the integration layer that has the workspace; the
// enriched system prompt reads it back through ProjectMap. Setting it to "" (an
// empty or fresh project) leaves nothing injected.
func (s *SessionState) SetProjectMap(m string) {
	s.SetProjectMapFP(m, "")
}

// SetProjectMapFP stores the map with the fingerprint it was built from, so
// the loop can tell a changed tree from an unchanged one without rebuilding.
func (s *SessionState) SetProjectMapFP(m, fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectMap = m
	s.projectMapFP = fp
}

// ProjectMapFP returns the fingerprint the stored map was built from.
func (s *SessionState) ProjectMapFP() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.projectMapFP
}

// ProjectMap returns the build-start project-structure snapshot, or "" when none
// was captured (a fresh project, or a caller that did not set one).
func (s *SessionState) ProjectMap() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.projectMap
}

// EmitTaskUpdate sends a task tracking event to the frontend.
func (s *SessionState) EmitTaskUpdate(taskID, description, status, result string) {
	s.TaskTracker.UpdateTask(taskID, status, result)
	s.Emitter.EmitTaskUpdate(taskID, description, status, result)
}

// TaskVersion exposes the task-graph version so the loop's rebuild guard can tell
// when the injected plan checklist has gone stale and the cached system prompt
// must be rebuilt.
func (s *SessionState) TaskVersion() int {
	return s.TaskTracker.Version()
}

// GetTasks returns a copy of the current task list — used to render the plan
// checklist injected into the system prompt.
func (s *SessionState) GetTasks() []Task {
	return s.TaskTracker.GetTasks()
}

// AddTask seeds one plan task. Used by seedPlanTasks at build start.
func (s *SessionState) AddTask(id, description string) {
	s.TaskTracker.AddTask(id, description)
}

// MarkPlanTask updates a seeded plan task and emits to the frontend only when the
// status or result actually changed. Emitting unconditionally would bust the
// prompt cache and spam the trace with a task re-asserting a status it already
// held; this keeps both the cache and the checklist quiet until something moves.
// When a persistence hook is attached, the write also lands in
// plan_task_status so the backlog survives restarts.
func (s *SessionState) MarkPlanTask(id, description, status, result string) {
	if s.TaskTracker.UpdateTask(id, status, result) {
		s.Emitter.EmitTaskUpdate(id, description, status, result)
		if s.persistTask != nil {
			s.persistTask(s.ProjectID, Task{ID: id, Description: description, Status: status, Result: result}, s.ID)
		}
	}
}

// AttachTaskPersistence wires the durable-backlog mirror. Call once, at
// session creation; MarkPlanTask writes through from then on.
func (s *SessionState) AttachTaskPersistence(fn func(projectID int64, t Task, byPrompt int64)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persistTask = fn
}

// EmitPlanSeed sends the seeded plan tasks to the frontend as their initial
// (pending) state, so the live checklist shows the whole plan — screens and
// features still to build, not only the ones that later flip status. Called once
// at build start, after the emitter is attached to the stream. A project with no
// saved plan seeds nothing, so this emits nothing.
func (s *SessionState) EmitPlanSeed() {
	for _, t := range s.TaskTracker.GetTasks() {
		s.Emitter.EmitTaskUpdate(t.ID, t.Description, t.Status, t.Result)
	}
}

// BeginGateSweep resets the per-sweep unavailable-gate list. One sweep is one
// ladder walk; the final sweep's unavailability is what a verified outcome
// reports (a gate unavailable in an early sweep that ran in the final one
// was repaired, not skipped).
func (s *SessionState) BeginGateSweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skippedGates = nil
}

// NoteGateUnavailable records a ladder stage that could not run.
func (s *SessionState) NoteGateUnavailable(stage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.skippedGates {
		if g == stage {
			return
		}
	}
	s.skippedGates = append(s.skippedGates, stage)
}

// SkippedGates returns the unavailable stages from the final ladder sweep.
func (s *SessionState) SkippedGates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.skippedGates...)
}
