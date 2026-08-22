package orchestrator

import (
	"sync"
	"time"

	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/store"
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
	PhaseDone          Phase = "DONE"
	PhaseFailed        Phase = "FAILED_NEEDS_USER"
)

type SessionState struct {
	ID              int64
	ProjectID       int64
	WorkspacePath   string
	CurrentPhase    Phase
	HealAttempts    int
	MaxHealAttempts int
	PatchHistory    []PatchRecord
	StartedAt       time.Time
	Emitter         *events.SessionEmitter

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

	mu sync.Mutex
}

type PatchRecord struct {
	Phase     Phase
	Files     []string
	Error     string
	Timestamp time.Time
}

// TaskTracker maintains a visible task breakdown during multi-step work.
type TaskTracker struct {
	Tasks []Task `json:"tasks"`
	mu    sync.Mutex
}

type Task struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"` // "pending", "in_progress", "completed", "failed"
	Result      string `json:"result,omitempty"`
}

func NewSession(id, projectID int64, workspacePath string, emitter *events.SessionEmitter) *SessionState {
	return &SessionState{
		ID:              id,
		ProjectID:       projectID,
		WorkspacePath:   workspacePath,
		CurrentPhase:    PhaseIntake,
		HealAttempts:    0,
		MaxHealAttempts: 6,
		StartedAt:       time.Now(),
		Emitter:         emitter,
		ReadFiles:       make(map[string]bool),
		SeenSkills:      make(map[string]bool),
		TaskTracker:     NewTaskTracker(),
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
}

func (tt *TaskTracker) UpdateTask(id, status, result string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	for i := range tt.Tasks {
		if tt.Tasks[i].ID == id {
			tt.Tasks[i].Status = status
			tt.Tasks[i].Result = result
			return
		}
	}
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

func (s *SessionState) IncrementHealAttempt() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HealAttempts++
	return s.HealAttempts
}

func (s *SessionState) ResetHealAttempts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HealAttempts = 0
}

func (s *SessionState) CanHeal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HealAttempts < s.MaxHealAttempts
}

func (s *SessionState) GetPhase() Phase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.CurrentPhase
}

func (s *SessionState) GetHealAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HealAttempts
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

// EmitTaskUpdate sends a task tracking event to the frontend.
func (s *SessionState) EmitTaskUpdate(taskID, description, status, result string) {
	s.TaskTracker.UpdateTask(taskID, status, result)
	s.Emitter.EmitTaskUpdate(taskID, description, status, result)
}
