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
	QuestionEvent       EventType = "question"
	DecisionEvent       EventType = "decision"
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
	PhaseWaiting       Phase = "WAITING_FOR_USER"
	PhaseDone          Phase = "DONE"
	PhaseFailed        Phase = "FAILED_NEEDS_USER"
)

type Event struct {
	Type    EventType   `json:"type"`
	Phase   Phase       `json:"phase,omitempty"`
	// ID correlates the two tool_call events of one call — start and resolve —
	// so a watcher updates the row it opened instead of matching by (tool,path)
	// and stacking a duplicate when the same file is touched twice in a turn.
	ID      string      `json:"id,omitempty"`
	Tool    string      `json:"tool,omitempty"`
	Path    string      `json:"path,omitempty"`
	Name    string      `json:"name,omitempty"`
	Status  string      `json:"status,omitempty"`
	Error   string      `json:"error,omitempty"`
	Text    string      `json:"text,omitempty"`
	Attempt int         `json:"attempt,omitempty"`
	// PromptID names the prompt turn this event belongs to. It is set only on the
	// question event, where the client needs it to address POST /answer: a fresh
	// build turn learns its prompt id no other way, since the stream is the
	// response to a request the server assigned the id inside.
	PromptID int64       `json:"prompt_id,omitempty"`
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

// EmitToolCall reports a tool starting, finishing or failing. id ties the
// start and resolve of one call together; path is the raw target (a file path,
// command or search pattern) the UI shows beside the verb; name is the
// human-readable verb phrase ("Editing app/index.tsx") used as the label for
// tools the UI does not model itself.
func (e *SessionEmitter) EmitToolCall(id, tool, path, name, status string) {
	e.Emit(Event{Type: ToolCallEvent, ID: id, Tool: tool, Path: path, Name: name, Status: status})
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

// EmitQuestion surfaces an ask_user pause: the question the agent needs answered
// and any suggested choices. id is the pending ask_user tool_use the user's
// answer resolves on resume — the frontend echoes it back so the answer lands on
// the right call. The emitter's session id is the prompt id (CreateSession is
// keyed by it), so it is carried too: POST /answer is addressed by prompt id,
// and a fresh build turn has no other way to learn it.
func (e *SessionEmitter) EmitQuestion(id, question string, choices []string) {
	e.Emit(Event{Type: QuestionEvent, ID: id, Text: question, Data: choices, PromptID: e.sessionID})
}

// Decision is the payload of a DecisionEvent: an architecture decision the agent
// recorded (write_adr) or a durable memory it wrote (write_memory). The build
// stream carries it so the chat can render the same record the "What the agent
// knows" panel shows, inline at the point the agent made it. Variant selects
// which fields are meaningful — the two tools store different shapes — and the
// field set mirrors what that panel displays (decision+outcome for an ADR; the
// category, the fact, and the area it applies to for a memory), so a plain
// tool_call row (which carries no content) never had to.
type Decision struct {
	Variant  string `json:"variant"` // "adr" | "memory"
	// adr
	Decision string `json:"decision,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	// memory
	Category string `json:"category,omitempty"`
	Content  string `json:"content,omitempty"`
	Metadata string `json:"metadata,omitempty"`
}

// EmitDecision surfaces a recorded decision so the chat shows it as a card. The
// orchestrator emits this only after the underlying write succeeded — a card
// stands for a decision actually on record, never an attempt that failed.
func (e *SessionEmitter) EmitDecision(d Decision) {
	e.Emit(Event{Type: DecisionEvent, Data: d})
}

// ToJSON serialises an event.
func ToJSON(evt Event) []byte {
	b, _ := json.Marshal(evt)
	return b
}
