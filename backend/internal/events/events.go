package events

import (
	"encoding/json"
	"sync"
)

type EventType string

const (
	PhaseEvent       EventType = "phase"
	ToolCallEvent    EventType = "tool_call"
	VerifyEvent      EventType = "verify"
	GuardrailEvent   EventType = "guardrail_reject"
	ErrorEvent       EventType = "error"
	DoneEvent        EventType = "done"
	TextEvent        EventType = "text"
	ThinkingEvent    EventType = "thinking"
	RebuildEvent     EventType = "rebuild"
	TaskUpdateEvent  EventType = "task_update"
)

type Phase string

const (
	PhaseIntake       Phase = "INTAKE"
	PhasePlan         Phase = "PLAN"
	PhaseContextBuild Phase = "CONTEXT_BUILD"
	PhaseResearch     Phase = "RESEARCH"
	PhaseGenerate     Phase = "GENERATE"
	PhaseStageWrite   Phase = "STAGE_WRITE"
	PhaseInstall      Phase = "INSTALL"
	PhaseVerifyStatic Phase = "VERIFY_STATIC"
	PhasePromote      Phase = "PROMOTE"
	PhaseVerifyRuntime Phase = "VERIFY_RUNTIME"
	PhaseHeal         Phase = "HEAL"
	PhaseDone         Phase = "DONE"
	PhaseFailed       Phase = "FAILED_NEEDS_USER"
)

type Event struct {
	Type      EventType  `json:"type"`
	Phase     Phase      `json:"phase,omitempty"`
	Tool      string     `json:"tool,omitempty"`
	Path      string     `json:"path,omitempty"`
	Name      string     `json:"name,omitempty"`
	Status    string     `json:"status,omitempty"`
	Error     string     `json:"error,omitempty"`
	Attempt   int        `json:"attempt,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

type SessionEmitter struct {
	sessionID int64
	events    chan Event
	mu        sync.Mutex
	closed    bool
}

func NewSessionEmitter(sessionID int64, bufferSize int) *SessionEmitter {
	return &SessionEmitter{
		sessionID: sessionID,
		events:    make(chan Event, bufferSize),
	}
}

func (e *SessionEmitter) Emit(evt Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.events <- evt
}

func (e *SessionEmitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		close(e.events)
	}
}

func (e *SessionEmitter) Events() <-chan Event {
	return e.events
}

func (e *SessionEmitter) SessionID() int64 {
	return e.sessionID
}

// EmitPhase is a convenience method for emitting phase events.
func (e *SessionEmitter) EmitPhase(phase Phase, status string) {
	e.Emit(Event{
		Type:   PhaseEvent,
		Phase:  phase,
		Status: status,
	})
}

// EmitToolCall is a convenience method for emitting tool call events.
func (e *SessionEmitter) EmitToolCall(tool, path, name, status string) {
	e.Emit(Event{
		Type:   ToolCallEvent,
		Tool:   tool,
		Path:   path,
		Name:   name,
		Status: status,
	})
}

// EmitVerify is a convenience method for emitting verification events.
func (e *SessionEmitter) EmitVerify(stage, status, errMsg string) {
	e.Emit(Event{
		Type:   VerifyEvent,
		Tool:   stage,
		Status: status,
		Error:  errMsg,
	})
}

// EmitGuardrail is a convenience method for emitting guardrail rejection events.
func (e *SessionEmitter) EmitGuardrail(rule, reason string) {
	e.Emit(Event{
		Type:   GuardrailEvent,
		Tool:   rule,
		Error:  reason,
		Status: "rejected",
	})
}

// EmitError is a convenience method for emitting error events.
func (e *SessionEmitter) EmitError(errMsg string) {
	e.Emit(Event{
		Type:   ErrorEvent,
		Error:  errMsg,
		Status: "error",
	})
}

// EmitDone is a convenience method for emitting done events.
func (e *SessionEmitter) EmitDone(status string) {
	e.Emit(Event{
		Type:   DoneEvent,
		Status: status,
	})
}

// EmitRebuild is a convenience method for emitting rebuild status events.
func (e *SessionEmitter) EmitRebuild(status, detail string) {
	e.Emit(Event{
		Type:   RebuildEvent,
		Status: status,
		Error:  detail,
	})
}

// EmitTaskUpdate is a convenience method for emitting task tracking events.
func (e *SessionEmitter) EmitTaskUpdate(taskID, description, status, result string) {
	e.Emit(Event{
		Type:   TaskUpdateEvent,
		Tool:   taskID,
		Name:   description,
		Status: status,
		Error:  result,
	})
}

// ToJSON serializes an event to JSON bytes.
func ToJSON(evt Event) []byte {
	b, _ := json.Marshal(evt)
	return b
}
