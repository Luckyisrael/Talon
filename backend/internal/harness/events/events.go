// Package events is the harness event taxonomy: the typed vocabulary every
// harness component speaks. Components do not call each other; they append
// events to the session log and listen for the events they care about. The
// taxonomy is the contract — adding a kind is additive; changing the meaning of
// an existing kind is a migration.
//
// Surface vs log events: surface events (user/message, assistant/message,
// tool/result, question/*) declare how they join the model-visible ordered
// surface when the log is derived into messages. Non-surface events (phase
// marks, usage, anchors, shadows) stay in the log for replay and auditing
// without ever producing a message. This split is what lets compaction shadow
// older surface entries without deleting history: replay re-derives the exact
// conversation from the same events.
package events

import "encoding/json"

// Kind is one typed session-log event name. The string forms are stable
// identifiers persisted in the log, so renaming one is a schema migration.
type Kind string

const (
	// ── Surface events: these produce model-visible messages ──

	// UserMessage appends one user turn. Payload: MessagePayload.
	UserMessage Kind = "user/message"
	// AssistantMessage appends one assistant turn (the durable anchor for one
	// completed model call, or the delivered prefix of an interrupted one).
	// Payload: MessagePayload.
	AssistantMessage Kind = "assistant/message"
	// ToolResult appends one tool result paired to a call. Payload: ToolResultPayload.
	ToolResult Kind = "tool/result"

	// ── Question events (surface: they carry ask_user turns) ──

	// QuestionAsked records the agent pausing on a question. Payload: QuestionPayload.
	QuestionAsked Kind = "question/asked"
	// QuestionAnswered records the user's answer resuming the turn. Payload: QuestionAnswerPayload.
	QuestionAnswered Kind = "question/answered"

	// ── Non-surface events: durable, auditable, never messages ──

	// SessionStarted marks the first event of a session. Payload: SessionStartPayload.
	SessionStarted Kind = "session/started"
	// TurnStarted marks one build turn opening. Payload: TurnPayload.
	TurnStarted Kind = "turn/started"
	// TurnFinished marks one build turn closing (done, stalled, failed…).
	// Payload: TurnPayload.
	TurnFinished Kind = "turn/finished"
	// PhaseMark records a phase transition without emitting transport events
	// (the transport emitter remains the SSE source of truth). Payload: PhasePayload.
	PhaseMark Kind = "phase/mark"
	// ToolDispatch records a tool call being dispatched (running). Paired with
	// ToolResult by the call id. Payload: ToolDispatchPayload.
	ToolDispatch Kind = "tool/dispatch"
	// UsageRecord records token/cost spend for one model call. Payload: UsagePayload.
	UsageRecord Kind = "usage/record"
	// Interrupted records a cancelled/interrupted model stream with the
	// delivered prefix so a resumed request contains exactly what the user
	// saw, never more. Payload: InterruptedPayload.
	Interrupted Kind = "stream/interrupted"
	// CompactionShadow marks a derivation boundary: everything before it is
	// shadowed (replaced by a summary) for derivation purposes. The log rows
	// before the boundary are never deleted; replay re-derives the full
	// history. Payload: CompactionPayload.
	CompactionShadow Kind = "compaction/shadow"
	// SessionCompacted records the summary produced by one compaction.
	// Payload: CompactionSummaryPayload.
	SessionCompacted Kind = "session/compacted"
	// ApprovalDecided records a one-shot permission decision for audit.
	// Payload: ApprovalPayload.
	ApprovalDecided Kind = "approval/decided"
	// RetryLogged records one model-call retry: which attempt, why it failed,
	// and how long the backoff was. A recovered transient failure is otherwise
	// invisible after a reload — this is the durable record that a gateway
	// error was retried away rather than becoming the turn's outcome.
	// Payload: RetryPayload.
	RetryLogged Kind = "llm/retry"
)

// SurfaceKind reports whether events of this kind join the model-visible
// surface during derivation. Non-surface events are durable audit records only.
func SurfaceKind(k Kind) bool {
	switch k {
	case UserMessage, AssistantMessage, ToolResult, QuestionAsked, QuestionAnswered:
		return true
	}
	return false
}

// MessagePayload is the body of user/message and assistant/message.
type MessagePayload struct {
	Role   string         `json:"role"` // "user" | "assistant"
	Blocks []BlockPayload `json:"blocks,omitempty"`
	// Interrupted is true when this assistant anchor carries only the
	// delivered prefix of a cancelled stream; the next request must include
	// exactly this prefix and nothing past it.
	Interrupted bool `json:"interrupted,omitempty"`
	// PromptID names the turn this message belongs to, for filtering.
	PromptID int64 `json:"prompt_id,omitempty"`
}

// BlockPayload is one provider-neutral content block. It mirrors ai.ContentBlock
// without importing the ai package — the taxonomy owns its own wire shapes so
// harness components never depend on the provider layer.
type BlockPayload struct {
	Type      string   `json:"type"` // text | thinking | tool_use | tool_result | plan
	Text      string   `json:"text,omitempty"`
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	Input     *RawJSON `json:"input,omitempty"`
	Content   string   `json:"content,omitempty"`
	IsError   bool     `json:"is_error,omitempty"`
	Signature string   `json:"signature,omitempty"` // thought signature passthrough
	// Plan carries a structured plan block (raw JSON) — persisted for the
	// restore path, deliberately not a provider-facing shape.
	Plan *RawJSON `json:"plan,omitempty"`
}

// RawJSON is raw JSON carried verbatim through the log. Marshals as-is.
type RawJSON json.RawMessage

func (r RawJSON) MarshalJSON() ([]byte, error) { return json.RawMessage(r).MarshalJSON() }
func (r *RawJSON) UnmarshalJSON(b []byte) error {
	(*json.RawMessage)(r).UnmarshalJSON(b)
	return nil
}

// ToolDispatchPayload is the body of tool/dispatch.
type ToolDispatchPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Target string `json:"target,omitempty"` // display hint (path, command…)
}

// ToolResultPayload is the body of tool/result.
type ToolResultPayload struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// QuestionPayload is the body of question/asked.
type QuestionPayload struct {
	QuestionID string   `json:"question_id"`
	Text       string   `json:"text"`
	Choices    []string `json:"choices,omitempty"`
}

// QuestionAnswerPayload is the body of question/answered.
type QuestionAnswerPayload struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
}

// SessionStartPayload is the body of session/started.
type SessionStartPayload struct {
	ProjectID int64  `json:"project_id"`
	CWD       string `json:"cwd,omitempty"`
}

// TurnPayload is the body of turn/started and turn/finished.
type TurnPayload struct {
	PromptID int64  `json:"prompt_id"`
	Outcome  string `json:"outcome,omitempty"` // success | stalled | failed | waiting | cancelled
	Rounds   int    `json:"rounds,omitempty"`
}

// PhasePayload is the body of phase/mark.
type PhasePayload struct {
	Phase string `json:"phase"`
	Note  string `json:"note,omitempty"`
}

// UsagePayload is the body of usage/record.
type UsagePayload struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CacheRead    int     `json:"cache_read_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	Model        string  `json:"model,omitempty"`
}

// InterruptedPayload is the body of stream/interrupted.
type InterruptedPayload struct {
	PromptID int64  `json:"prompt_id"`
	Reason   string `json:"reason,omitempty"`
	// DeliveredPrefix is the text already streamed to the user before the
	// interruption — the anchor the resumed request carries forward.
	DeliveredPrefix string `json:"delivered_prefix,omitempty"`
}

// CompactionPayload is the body of compaction/shadow.
type CompactionPayload struct {
	// ShadowFromSeq shadows every surface event at or before this seq for
	// derivation. The log itself is untouched.
	ShadowFromSeq int64  `json:"shadow_from_seq"`
	Reason        string `json:"reason,omitempty"`
}

// CompactionSummaryPayload is the body of session/compacted.
type CompactionSummaryPayload struct {
	Summary  string `json:"summary"`
	FromTurn int    `json:"from_turn,omitempty"`
	ToTurn   int    `json:"to_turn,omitempty"`
}

// ApprovalPayload is the body of approval/decided.
type ApprovalPayload struct {
	Scope    string `json:"scope"` // e.g. "device-build", "shell-tier2"
	Allowed  bool   `json:"allowed"`
	Reason   string `json:"reason,omitempty"`
	PromptID int64  `json:"prompt_id,omitempty"`
}

// RetryPayload is the body of llm/retry.
type RetryPayload struct {
	// Attempt is 1-based: attempt 1 is the first retry, after the first request
	// already failed.
	Attempt    int `json:"attempt"`
	MaxRetries int `json:"max_retries"`
	// Reason names the failure class as the retry loop saw it: a rate limit, a
	// server error, an empty response, a transport error. The exact status or
	// error string rides in Detail.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	// BackoffMs is the wait before this retry runs.
	BackoffMs int64  `json:"backoff_ms"`
	Model     string `json:"model,omitempty"`
	PromptID  int64  `json:"prompt_id,omitempty"`
}
