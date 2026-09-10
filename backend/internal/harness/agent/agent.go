// Package agent is the H2 agent contract: a live registry of running agents,
// each with a steer lane. An agent handle is how the rest of the system
// talks to a turn in flight — steer it, inject context, cancel it — without
// knowing anything about the loop driving it.
//
// The contract follows the DSH agent split: the orchestrator stops being "the
// thing that runs the loop" and becomes the domain-state owner that hands an
// Agent handle to whoever drives. The loop (its extraction is the next step
// of H2) is the only concrete driver; everything else holds a handle.
//
// Steer semantics (the user-facing contract): a message typed while a build
// runs lands in the running turn — the next model request contains it —
// instead of queueing for a future turn. Inject is context without waking:
// the text rides into the next request but does not change what the turn is
// doing. Followup is today's behavior: queue for the next turn.
package agent

import (
	"sync"
)

// Class is how a user message routes to a running agent.
type Class string

const (
	// Followup queues the message for the next turn — today's only behavior.
	Followup Class = "followup"
	// Steer wakes the current turn with next-step input: the message lands in
	// the next model request the running loop makes. The turn is not aborted;
	// the model is redirected mid-build.
	Steer Class = "steer"
	// Inject adds context without waking: the text rides into the next
	// request whenever it happens, but no turn is started or redirected for it.
	Inject Class = "inject"
)

// Inbox is the per-agent pending-message lane, ordered by arrival.
type Inbox struct {
	// Class tells the loop what the arrival means.
	Class Class
	// Text is the message body.
	Text string
	// At is the arrival sequence, for honest "steered at round 7" reporting.
	At int64
}

// Steerable is what an agent handle wraps: a turn in flight that can accept
// mid-turn messages. The orchestrator's session implements it.
type Steerable interface {
	// Deliver appends one message to the agent's lane and returns its arrival
	// sequence. Called from any goroutine (the SSE steer route); the
	// implementation must be safe for concurrent use.
	Deliver(class Class, text string) int64
}

// Agent is the public handle for one running turn. Frontends and API routes
// hold this — never the loop, never the session internals.
type Agent struct {
	id       int64
	mu       sync.Mutex
	inbox    []Inbox
	seq      int64
	session  Steerable
	promptID int64
}

// Handle builds an Agent for a turn in flight. The Steerable is the seam to
// the running loop's session; nil makes a detached handle (registry-only),
// which tests use.
func Handle(id, promptID int64, session Steerable) *Agent {
	return &Agent{id: id, promptID: promptID, session: session}
}

// ID returns the agent's id (the prompt id — one prompt, one turn, one agent).
func (a *Agent) ID() int64 { return a.id }

// PromptID returns the prompt driving this agent.
func (a *Agent) PromptID() int64 { return a.promptID }

// Steer delivers a mid-turn message of the given class (Steer redirects the
// build; Inject adds context without redirecting). If the agent has a live
// session the message goes to it immediately; otherwise it queues in the
// handle's inbox and the loop drains it on the next round boundary.
func (a *Agent) Steer(class Class, text string) int64 {
	if a.session != nil {
		if n := a.session.Deliver(class, text); n > 0 {
			return n
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	a.inbox = append(a.inbox, Inbox{Class: class, Text: text, At: a.seq})
	return a.seq
}

// Drain returns and clears the handle's inbox — the messages that arrived
// while no session was attached. The loop calls this at round boundaries.
func (a *Agent) Drain() []Inbox {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.inbox
	a.inbox = nil
	return out
}

// Pending reports how many messages are waiting in the handle's inbox.
func (a *Agent) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.inbox)
}

// Registry tracks the live agents: one per running prompt. The SSE steer
// route looks the agent up here; the loop publishes here on start and
// unpublishes on exit — publish is rollback-covered so a setup failure never
// leaks a half-registered agent.
type Registry struct {
	mu     sync.Mutex
	agents map[int64]*Agent
}

// NewRegistry builds an empty live registry.
func NewRegistry() *Registry {
	return &Registry{agents: map[int64]*Agent{}}
}

// Publish registers a running agent. The caller publishes after the agent is
// fully set up — a publish-then-fail leaves a phantom agent that a steer
// would vanish into, so setup happens first, publish last.
func (r *Registry) Publish(a *Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[a.id] = a
}

// Unpublish removes a finished agent. Idempotent — the loop's deferred
// unpublish and a cancel path can both run.
func (r *Registry) Unpublish(id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.agents, id)
}

// Get returns the live agent for a prompt id, or nil if none is running.
func (r *Registry) Get(id int64) *Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agents[id]
}

// Len reports how many agents are live — for metrics and tests.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.agents)
}
