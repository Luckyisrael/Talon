// Package loop is the H2 loop-driver seam: the contract the agent loop's
// cross-cutting decisions flow through. Today those decisions (model routing,
// request-error retry, fallback latching) live inline in the orchestrator's
// loop body; this package gives them a typed interface so the loop skeleton
// can be extracted — the orchestrator keeps domain state, the loop becomes the
// only concrete driver, and every decision beyond "call the model, run the
// tools, repeat" becomes a listener a composition can swap.
//
// The first seam extracted is the request-error decision (H2 work item 4):
// when a model request fails, who decides what happens next? The fixed
// "one retry on the fallback, then die" policy becomes a listener — the
// routing composition answers retry-on-model, retry-with-backoff, or
// terminal, and the loop obeys.
package loop

import (
	"time"
)

// RequestErrorDecision is one answer to "the model request failed — now what?"
type RequestErrorDecision struct {
	// Action is what the loop does next.
	Action ErrorAction
	// RetryModel names the model to retry on (Action RetryOnModel). Empty
	// means the same model.
	RetryModel string
	// Latch pins the turn to RetryModel for the rest of the turn after a
	// successful retry — one retry that hands the turn back to a dead model
	// is theater, not reliability.
	Latch bool
	// Backoff is how long to wait before the retry. Zero retries immediately.
	Backoff time.Duration
	// Reason is logged and surfaced to the outcome — the tuning data.
	Reason string
}

// ErrorAction is the class of recovery a listener chose.
type ErrorAction int

const (
	// ErrorTerminal ends the turn: the failure is not recoverable at this
	// layer.
	ErrorTerminal ErrorAction = iota
	// ErrorRetryOnModel retries the request, on RetryModel when set,
	// otherwise the same model. Latch pins the turn to it on success.
	ErrorRetryOnModel
	// ErrorRetryWithBackoff waits Backoff, then retries on the same model —
	// for transient throttles a cheaper model cannot fix.
	ErrorRetryWithBackoff
)

// RequestErrorListener answers a failed model request. round is the loop
// round the failure happened on; model is what was tried; fellBack reports
// whether a fallback retry already happened this turn (the listener decides
// whether a second is allowed).
type RequestErrorListener func(round int, model string, err error, fellBack bool) RequestErrorDecision

// Terminal is the no-retry answer: fail the turn.
func Terminal(reason string) RequestErrorDecision {
	return RequestErrorDecision{Action: ErrorTerminal, Reason: reason}
}

// RetryOnModel is the cheaper-model answer: retry once on the named model,
// optionally latching the turn to it.
func RetryOnModel(model string, latch bool, reason string) RequestErrorDecision {
	return RequestErrorDecision{Action: ErrorRetryOnModel, RetryModel: model, Latch: latch, Reason: reason}
}

// RetryAfter is the backoff answer: same model, wait first.
func RetryAfter(d time.Duration, reason string) RequestErrorDecision {
	return RequestErrorDecision{Action: ErrorRetryWithBackoff, Backoff: d, Reason: reason}
}
