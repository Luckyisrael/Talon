// Package hooks: W10 — lifecycle callbacks users and we can rely on.
//
// Hooks are subscribers with a decision. The event taxonomy is a subset of
// the kinds the session log already carries; each configured hook observes
// one event and answers with the decision contract:
//
//	{decision: allow|deny|ask, reason?, additionalContext?}
//
// deny.reason feeds the model exactly like guardrail messages do today —
// a blocked call returns a failed tool_result naming the hook and why.
// "ask" falls through to the approval seam (or refuses, fail closed, when
// no seam is composed).
//
// Handler types: shell commands (stdin JSON payload, stdout JSON decision,
// exit 0 = proceed, exit 2 = block with stderr as the reason) and in-process
// Go handlers (the three built-ins, which prove the pattern: block-env-edits,
// run-tests-after-write, slack-on-turn-fail). HTTP/MCP handlers come later
// behind the same Handler interface.
//
// Trust class: hooks are user-authored code in the workspace — the same
// trust already granted to AGENTS.md and .talon/skills. Nothing in this
// package executes anything the workspace did not declare.
//
// Flag: TALONSTUDIO_HOOKS truthy (1|true|on|v1) gates loading. Absent or
// off, Load returns nil and every Dispatch is a no-op — byte-identical
// behavior, no config read, no log append.
//
// DSH's law, enforced by every caller: extension failure never corrupts the
// loop. A sync hook error closes the turn (honest FinalText, nil error) or
// fails that one request closed; an async hook error is logged. Neither
// ever panics or silently proceeds.
package hooks

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Event is one lifecycle point a hook can subscribe to. The set is fixed
// (the plan's ten); config keys are exactly these spellings.
type Event string

const (
	// PreToolUse fires before a tool call dispatches. It gates: deny
	// prevents dispatch and yields the model-visible reason.
	PreToolUse Event = "PreToolUse"
	// PostToolUse fires after a successful tool call. Sync handlers'
	// additionalContext/deny surface on the tool result; async handlers
	// (run tests) run after Write without delaying the round.
	PostToolUse Event = "PostToolUse"
	// PostToolUseFailure is PostToolUse for an errored call.
	PostToolUseFailure Event = "PostToolUseFailure"
	// UserPromptSubmit fires when a user prompt arrives, before it is
	// stored or sent: deny refuses the prompt; additionalContext rides it.
	UserPromptSubmit Event = "UserPromptSubmit"
	// TurnStart fires when an agent loop opens a turn. deny closes the
	// turn before round one; additionalContext extends the system prompt.
	TurnStart Event = "TurnStart"
	// TurnStop fires when the loop closes a turn, carrying the outcome.
	// Notify-class: the turn is already over, so failures log only.
	TurnStop Event = "TurnStop"
	// SessionStart fires when a build session is constructed for a
	// request. Notify-class (the session is not yet a turn).
	SessionStart Event = "SessionStart"
	// SubagentStart fires before a child turn spawns; deny blocks it with
	// the reason visible to the parent model.
	SubagentStart Event = "SubagentStart"
	// SubagentStop fires when a child turn ends, carrying its outcome.
	SubagentStop Event = "SubagentStop"
	// ApprovalRequest fires inside the approval seam, after PolicyNever
	// and read-only (both absolute) and before the mode switch: deny
	// refuses this approval, allow grants it, ask continues the normal
	// mode/answerer flow.
	ApprovalRequest Event = "ApprovalRequest"
)

// Decisions. Unknown spellings from shell stdout are contract violations
// (an error, never a guess).
const (
	Allow = "allow"
	Deny  = "deny"
	Ask   = "ask"
)

// Decision is the contract's answer shape.
type Decision struct {
	Decision          string `json:"decision"`
	Reason            string `json:"reason,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// Payload is the stdin JSON a shell handler receives and the in-process
// handlers inspect. Fields are omitempty: absent means inapplicable.
type Payload struct {
	Event     Event           `json:"event"`
	Tool      string          `json:"tool,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"isError,omitempty"`
	Result    string          `json:"result,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Outcome   string          `json:"outcome,omitempty"` // TurnStart/Stop/SubagentStop: "success" | "failure"
	Reason    string          `json:"reason,omitempty"`  // failure text for TurnStop-class
	Workspace string          `json:"workspace,omitempty"`
	ProjectID int64           `json:"project_id,omitempty"`
	PromptID  int64           `json:"prompt_id,omitempty"`
}

// Handler runs one hook. An error is an extension failure: the caller
// fails closed (block the call, close the turn, or log for async) — never
// proceeds as if the hook had allowed, never panics.
type Handler interface {
	Handle(ctx context.Context, p Payload) (Decision, error)
}

// configured is one config entry resolved to a runnable handler.
type configured struct {
	name    string // audit label: "command:..." / "builtin:..."
	async   bool
	timeout time.Duration
	tools   map[string]bool // non-empty → only fire for these tool names
	handler Handler
}

// Registry is the resolved hook config for one workspace. The zero/nil
// Registry dispatches nothing: every method is nil-receiver safe so a
// flag-off or absent-config session behaves byte-identically.
type Registry struct {
	mu      sync.RWMutex
	byEvent map[Event][]configured
}

// Enabled reads TALONSTUDIO_HOOKS with the project's truthy set
// (1|true|on|v1, trimmed, case-insensitive).
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_HOOKS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// entry is one hook in .talon/config.json's "hooks" object. Exactly one of
// command/builtin must be set.
type entry struct {
	Name      string   `json:"name,omitempty"`
	Command   string   `json:"command,omitempty"`
	Builtin   string   `json:"builtin,omitempty"`
	Webhook   string   `json:"webhook,omitempty"`
	Async     bool     `json:"async,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}

// config is the W10 view of .talon/config.json — the same file W6
// (verify_gates) and W9 (permission_mode) already read.
type config struct {
	Hooks map[string][]entry `json:"hooks,omitempty"`
}

// Load resolves workspacePath/.talon/config.json's "hooks" object into a
// Registry. Returns nil (hooks off, byte-identical) when: the flag is off,
// the workspace is empty, no config exists, "hooks" is absent/empty, or the
// config is invalid — an invalid hook config disables hooks entirely and
// logs loudly (partial enforcement would be a silent lie; the log line is
// where the operator finds out).
func Load(workspacePath string) *Registry {
	if !Enabled() || workspacePath == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".talon", "config.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("hooks: cannot read project config, hooks disabled: %v", err)
		}
		return nil
	}
	var cfg config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Printf("hooks: invalid project config JSON, hooks disabled: %v", err)
		return nil
	}
	if len(cfg.Hooks) == 0 {
		return nil
	}
	reg := &Registry{byEvent: make(map[Event][]configured, len(cfg.Hooks))}
	for evKey, entries := range cfg.Hooks {
		ev := Event(evKey)
		if !validEvent(ev) {
			log.Printf("hooks: unknown event %q in config, hooks disabled", evKey)
			return nil
		}
		for i, e := range entries {
			c, err := resolve(e)
			if err != nil {
				log.Printf("hooks: config entry %s[%d]: %v — hooks disabled", evKey, i, err)
				return nil
			}
			reg.byEvent[ev] = append(reg.byEvent[ev], c)
		}
	}
	return reg
}

func validEvent(ev Event) bool {
	switch ev {
	case PreToolUse, PostToolUse, PostToolUseFailure, UserPromptSubmit,
		TurnStart, TurnStop, SessionStart, SubagentStart, SubagentStop,
		ApprovalRequest:
		return true
	}
	return false
}

// resolve validates one entry into a runnable configured hook. A builtin
// entry carries its own parameters (run-tests takes its command, slack its
// webhook) — newBuiltin validates those; a generic entry must set command.
func resolve(e entry) (configured, error) {
	if e.Builtin == "" && e.Command == "" {
		return configured{}, errBothOrNeither
	}
	c := configured{
		name:    e.Name,
		timeout: time.Duration(e.TimeoutMS) * time.Millisecond,
	}
	if c.name == "" {
		if e.Builtin != "" {
			c.name = "builtin:" + e.Builtin
		} else {
			c.name = "command:" + truncate(e.Command, 60)
		}
	}
	if len(e.Tools) > 0 {
		c.tools = make(map[string]bool, len(e.Tools))
		for _, t := range e.Tools {
			c.tools[t] = true
		}
	}
	if e.Builtin != "" {
		h, async, err := newBuiltin(e)
		if err != nil {
			return configured{}, err
		}
		c.handler = h
		c.async = async // built-ins that notify or run work after the fact
		// are async by construction; a gating built-in stays sync.
		return c, nil
	}
	if c.timeout == 0 {
		c.timeout = 10 * time.Second
	}
	c.handler = &commandHandler{command: e.Command}
	c.async = e.Async
	return c, nil
}

// errBothOrNeither is the entry contract: exactly one handler source.
type bothOrNeitherError struct{}

func (bothOrNeitherError) Error() string {
	return "entry must set exactly one of \"command\" or \"builtin\""
}

var errBothOrNeither = bothOrNeitherError{}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ExecRecord describes one hook's execution for the audit trail: which
// hook answered, with what, how long it took, and whether it broke.
// Async marks a fire-and-forget execution — callers that take action on
// a decision must count only sync records (async entries never gate).
type ExecRecord struct {
	Hook     string
	Event    Event
	Decision Decision
	Err      error
	Took     time.Duration
	Async    bool
}

// Check runs every hook configured for p.Event. Sync entries run inline in
// config order and their decisions combine: first deny wins immediately; a
// later deny beats an earlier ask; allow is the default; additionalContext
// from all consulted entries is concatenated. Async entries (run-tests,
// notifications) spawn here — their decisions never gate, the caller's
// round never waits on them, and their outcomes log and audit from their
// own goroutine. A sync handler error returns immediately — the caller
// fails closed with it.
//
// A nil Registry (flag off / no config) returns Allow with no I/O: the
// pre-W10 path, byte for byte.
func (r *Registry) Check(ctx context.Context, p Payload) (Decision, error) {
	return r.check(ctx, p, nil)
}

// CheckAudited is Check with a per-execution callback: every consulted
// hook reports its ExecRecord before the combined decision returns (async
// entries report from their own goroutine). onExec nil = Check.
func (r *Registry) CheckAudited(ctx context.Context, p Payload, onExec func(ExecRecord)) (Decision, error) {
	return r.check(ctx, p, onExec)
}

// asyncCeiling caps a background hook that configured no timeout of its
// own — an async hook must never leak a goroutine forever.
const asyncCeiling = 10 * time.Minute

func (r *Registry) check(ctx context.Context, p Payload, onExec func(ExecRecord)) (Decision, error) {
	if r == nil {
		return Decision{Decision: Allow}, nil
	}
	r.mu.RLock()
	entries := append([]configured(nil), r.byEvent[p.Event]...)
	r.mu.RUnlock()
	if len(entries) == 0 {
		return Decision{Decision: Allow}, nil
	}

	sawAsk := false
	var askFrom string
	var contextParts []string
	for _, c := range entries {
		if len(c.tools) > 0 && p.Tool != "" && !c.tools[p.Tool] {
			continue // entry is scoped to other tools
		}
		if c.async {
			// Fire and forget: the round does not wait, and this entry's
			// decision cannot gate what already happened.
			go spawn(c, p, onExec)
			continue
		}
		d, took, err := runOne(ctx, c, p)
		if onExec != nil {
			onExec(ExecRecord{Hook: c.name, Event: p.Event, Decision: d, Err: err, Took: took})
		}
		if err != nil {
			return Decision{}, &ExecutionError{Hook: c.name, Event: p.Event, Err: err}
		}
		switch d.Decision {
		case Deny:
			// Deny wins outright — reason names the hook that said no.
			if d.Reason == "" {
				d.Reason = "blocked by hook " + c.name
			}
			return Decision{Decision: Deny, Reason: d.Reason, AdditionalContext: joinContext(append(contextParts, d.AdditionalContext)...)}, nil
		case Ask:
			if !sawAsk {
				sawAsk, askFrom = true, c.name
			}
			if d.AdditionalContext != "" {
				contextParts = append(contextParts, d.AdditionalContext)
			}
		case Allow, "":
			if d.AdditionalContext != "" {
				contextParts = append(contextParts, d.AdditionalContext)
			}
		default:
			return Decision{}, &ExecutionError{Hook: c.name, Event: p.Event,
				Err: invalidDecisionError(d.Decision)}
		}
	}
	if sawAsk {
		return Decision{Decision: Ask, Reason: "hook " + askFrom + " asks for approval", AdditionalContext: joinContext(contextParts...)}, nil
	}
	return Decision{Decision: Allow, AdditionalContext: joinContext(contextParts...)}, nil
}

// runOne executes a single entry under its timeout, logs the execution,
// and reports how long it took.
func runOne(ctx context.Context, c configured, p Payload) (Decision, time.Duration, error) {
	hctx := ctx
	var cancel context.CancelFunc
	if c.timeout > 0 {
		hctx, cancel = context.WithTimeout(ctx, c.timeout)
	}
	start := time.Now()
	d, err := c.handler.Handle(hctx, p)
	took := time.Since(start)
	if cancel != nil {
		cancel()
	}
	logExec(c.name, p.Event, d, err, took)
	return d, took, err
}

// spawn runs an async entry on its own goroutine with its own lifetime —
// it must outlive the caller's ctx (the turn may end first) but never
// leaks past the ceiling or the entry's own timeout.
func spawn(c configured, p Payload, onExec func(ExecRecord)) {
	ctx, cancel := context.WithTimeout(context.Background(), asyncCeiling)
	defer cancel()
	d, took, err := runOne(ctx, c, p)
	if onExec != nil {
		onExec(ExecRecord{Hook: c.name, Event: p.Event, Decision: d, Err: err, Took: took, Async: true})
	}
	if err != nil {
		// Async failure never closes a turn that already moved on:
		// runOne already logged it; surface it here too.
		log.Printf("hooks: async %s hook failed: %v", p.Event, err)
	}
}

// ExecutionError is an extension failure: the hook itself broke (spawn
// error, timeout, bad JSON, contract violation). Callers fail closed with
// it — close the turn / block the call — per DSH's law.
type ExecutionError struct {
	Hook  string
	Event Event
	Err   error
}

func (e *ExecutionError) Error() string {
	return "hook " + e.Hook + " (" + string(e.Event) + ") failed: " + e.Err.Error()
}

func (e *ExecutionError) Unwrap() error { return e.Err }

type invalidDecisionError string

func (e invalidDecisionError) Error() string {
	return "invalid decision " + string(e) + " (want allow|deny|ask)"
}

// logExec appends one hook execution's record to the server log — the
// always-on audit line. Session-log appends (hook/exec events) are the
// callers' job, because they own the log handle.
func logExec(name string, ev Event, d Decision, err error, took time.Duration) {
	switch {
	case err != nil:
		log.Printf("hooks: %s %s failed after %s: %v", ev, name, took.Round(time.Millisecond), err)
	case d.Decision == Deny:
		log.Printf("hooks: %s %s denied after %s: %s", ev, name, took.Round(time.Millisecond), d.Reason)
	default:
		log.Printf("hooks: %s %s %s after %s", ev, name, d.Decision, took.Round(time.Millisecond))
	}
}

func joinContext(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}
