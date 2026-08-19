package events

import (
	"encoding/json"
	"sync"
)

type EventType string

const (
	PhaseEvent          EventType = "phase"
	ToolCallEvent       EventType = "tool_call"
	VerifyEvent         EventType = "verify"
	GuardrailEvent      EventType = "guardrail_reject"
	ErrorEvent          EventType = "error"
	DoneEvent           EventType = "done"
	TextEvent           EventType = "text"
	ThinkingEvent       EventType = "thinking"
	RebuildEvent        EventType = "rebuild"
	TaskUpdateEvent     EventType = "task_update"
	UsageEvent          EventType = "usage"
	CommandOutputEvent  EventType = "command_output"
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
	PhaseHeal          Phase = "HEAL"
	PhaseDone          Phase = "DONE"
	PhaseFailed        Phase = "FAILED_NEEDS_USER"
)

type Event struct {
	Type    EventType   `json:"type"`
	Phase   Phase       `json:"phase,omitempty"`
	Tool    string      `json:"tool,omitempty"`
	Path    string      `json:"path,omitempty"`
	Name    string      `json:"name,omitempty"`
	Status  string      `json:"status,omitempty"`
	Error   string      `json:"error,omitempty"`
	Text    string      `json:"text,omitempty"`
	Attempt int         `json:"attempt,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// SessionEmitter fans one build's progress out to whoever is watching.
//
// The event channel is never closed, and every send races a done channel that
// Close shuts. That combination is what makes the emitter safe to use from the
// build goroutine: a client that hangs up mid-build cannot wedge the producer,
// and a producer mid-send cannot panic on a closed channel.
type SessionEmitter struct {
	sessionID int64
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
}

func NewSessionEmitter(sessionID int64, bufferSize int) *SessionEmitter {
	if bufferSize < 64 {
		bufferSize = 64
	}
	return &SessionEmitter{
		sessionID: sessionID,
		events:    make(chan Event, bufferSize),
		done:      make(chan struct{}),
	}
}

// Emit delivers an event, or returns immediately if the emitter is closed.
func (e *SessionEmitter) Emit(evt Event) {
	select {
	case <-e.done:
		return
	default:
	}
	select {
	case e.events <- evt:
	case <-e.done:
	}
}

// Close stops delivery. Safe to call more than once and from any goroutine.
func (e *SessionEmitter) Close() {
	e.closeOnce.Do(func() { close(e.done) })
}

// Events is the stream to read. It is never closed — range over it with a
// select on Done() rather than a bare for-range.
func (e *SessionEmitter) Events() <-chan Event { return e.events }

// Done is closed when the emitter is closed.
func (e *SessionEmitter) Done() <-chan struct{} { return e.done }

// Pending reports how many events are buffered, so a consumer can drain what is
// already queued after the producer finishes.
func (e *SessionEmitter) Pending() int { return len(e.events) }

func (e *SessionEmitter) SessionID() int64 { return e.sessionID }

// EmitPhase reports a transition in the build pipeline.
func (e *SessionEmitter) EmitPhase(phase Phase, status string) {
	e.Emit(Event{Type: PhaseEvent, Phase: phase, Status: status})
}

// EmitToolCall reports a tool starting, finishing or failing.
func (e *SessionEmitter) EmitToolCall(tool, path, name, status string) {
	e.Emit(Event{Type: ToolCallEvent, Tool: tool, Path: path, Name: name, Status: status})
}

// EmitVerify reports the outcome of a static check.
func (e *SessionEmitter) EmitVerify(stage, status, errMsg string) {
	e.Emit(Event{Type: VerifyEvent, Tool: stage, Status: status, Error: errMsg})
}

// EmitGuardrail reports a rejected action.
func (e *SessionEmitter) EmitGuardrail(rule, reason string) {
	e.Emit(Event{Type: GuardrailEvent, Tool: rule, Error: reason, Status: "rejected"})
}

// EmitError reports a failure the user needs to see.
func (e *SessionEmitter) EmitError(errMsg string) {
	e.Emit(Event{Type: ErrorEvent, Error: errMsg, Status: "error"})
}

// EmitDone reports the build finishing.
func (e *SessionEmitter) EmitDone(status string) {
	e.Emit(Event{Type: DoneEvent, Status: status})
}

// EmitText forwards the agent's own prose so the user reads its reasoning as it
// works rather than only at the end.
func (e *SessionEmitter) EmitText(text string) {
	if text == "" {
		return
	}
	e.Emit(Event{Type: TextEvent, Text: text})
}

// EmitThinking forwards reasoning output, where the provider exposes it.
func (e *SessionEmitter) EmitThinking(text string) {
	if text == "" {
		return
	}
	e.Emit(Event{Type: ThinkingEvent, Text: text})
}

// EmitUsage reports token spend so the UI can show the cost of a turn.
func (e *SessionEmitter) EmitUsage(in, out, cacheRead int) {
	e.Emit(Event{Type: UsageEvent, Data: map[string]int{
		"input_tokens":      in,
		"output_tokens":     out,
		"cache_read_tokens": cacheRead,
	}})
}

// EmitRebuild reports progress of a background native rebuild.
func (e *SessionEmitter) EmitRebuild(status, detail string) {
	e.Emit(Event{Type: RebuildEvent, Status: status, Error: detail})
}

// EmitTaskUpdate reports progress on one item of the task list the UI shows.
func (e *SessionEmitter) EmitTaskUpdate(taskID, description, status, result string) {
	e.Emit(Event{Type: TaskUpdateEvent, Tool: taskID, Name: description, Status: status, Error: result})
}

// EmitCommandOutput forwards the live stdout/stderr of a command the agent is
// running. Without this the user watches a build happen with no idea what the
// tool is actually doing — the shell and the build output pane were blind to
// the very commands producing the APK.
func (e *SessionEmitter) EmitCommandOutput(text string) {
	if text == "" {
		return
	}
	e.Emit(Event{Type: CommandOutputEvent, Text: text})
}

// ToJSON serialises an event.
func ToJSON(evt Event) []byte {
	b, _ := json.Marshal(evt)
	return b
}
