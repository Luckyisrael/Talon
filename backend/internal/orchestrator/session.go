package orchestrator

import (
	"sync"
	"time"

	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/staging"
)

type Phase string

const (
	PhaseIntake        Phase = "INTAKE"
	PhasePlan          Phase = "PLAN"
	PhaseContextBuild  Phase = "CONTEXT_BUILD"
	PhaseResearch      Phase = "RESEARCH"
	PhaseGenerate      Phase = "GENERATE"
	PhaseStageWrite    Phase = "STAGE_WRITE"
	PhaseInstall       Phase = "INSTALL"
	PhaseVerifyStatic  Phase = "VERIFY_STATIC"
	PhasePromote       Phase = "PROMOTE"
	PhaseVerifyRuntime Phase = "VERIFY_RUNTIME"
	PhaseDone          Phase = "DONE"
	PhaseFailed        Phase = "FAILED_NEEDS_USER"
)

type SessionState struct {
	ID              int64
	ProjectID       int64
	WorkspacePath   string
	StagingPath     string
	CurrentPhase    Phase
	HealAttempts    int
	MaxHealAttempts int
	PatchHistory    []PatchRecord
	StartedAt       time.Time
	Emitter         *events.SessionEmitter
	Staging         *staging.Manager

	mu sync.Mutex
}

type PatchRecord struct {
	Phase     Phase
	Files     []string
	Error     string
	Timestamp time.Time
}

func NewSession(id, projectID int64, workspacePath, stagingPath string, emitter *events.SessionEmitter) *SessionState {
	return &SessionState{
		ID:              id,
		ProjectID:       projectID,
		WorkspacePath:   workspacePath,
		StagingPath:     stagingPath,
		CurrentPhase:    PhaseIntake,
		HealAttempts:    0,
		MaxHealAttempts: 3,
		StartedAt:       time.Now(),
		Emitter:         emitter,
		Staging:         staging.NewManager(stagingPath, workspacePath),
	}
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
