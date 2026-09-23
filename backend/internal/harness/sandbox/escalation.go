package sandbox

// escalation.go — the one-shot strictly-wider retry.
//
// A confinement refusal is not always the end of the operation. The workspace
// mode refuses a write outside the project; the write is exactly what the build
// needs — a gradle cache the tool cannot relocate, a symlink target the
// platform owns. The honest options are "refuse and fail the build" or "ask
// once, with the justification, and run it under a strictly wider mode." This
// is the second option.
//
// Three rules, all load-bearing:
//
//  1. Strictly wider, never equal or narrower. An escalation from
//     ModeWorkspace to ModeWorkspace is a no-op dressed as permission; from
//     ModeDangerFull there is nowhere to escalate to. The ladder is a lattice,
//     not a loop — a refused escalation to the same mode is a bug in the
//     caller, and it is rejected rather than silently allowed.
//  2. One-shot. The wider mode applies to the single operation named in the
//     request, never to the session. A session that escalated once and then
//     stayed escalated is a session that lost its confinement by asking for
//     it once — the approval is for the action, not for the mode.
//  3. Justified and audited. The request carries the reason the wider mode is
//     needed; a request without one is refused. The reason lands in the audit
//     record so an operator can tell "the build needed the gradle cache" from
//     "the tool wanted to write anywhere".
//
// A denied escalation is final for that operation: the caller may not retry the
// same request with a different justification, because that is how an
// escalation ladder becomes an approval loop.

import (
	"errors"
	"fmt"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
)

// Escalation is one request to run a refused operation under a wider mode.
type Escalation struct {
	// From is the mode that refused. It must be strictly below To.
	From Mode
	// To is the mode the operation is asking to run under.
	To Mode
	// Operation is what was refused and is now being asked for.
	Operation Operation
	// Justification is why the wider mode is needed. Required: an escalation
	// without a reason is a permission request with no evidence.
	Justification string
	// PromptID locates the turn asking, for the audit record.
	PromptID int64
	// Permission is the session's W9 permission mode, carried into the
	// seam's decision (read-only refuses, auto allows, accept-edits
	// allows the workspace-write rung, ask prompts — today). Empty = ask.
	Permission approval.Mode
	// Workspace is the project root for standing-grant lookup/recording
	// (W9.3). Empty disables both for this escalation.
	Workspace string
}

// ErrNotWider names a request that does not actually escalate.
var ErrNotWider = errors.New("the escalation must move to a strictly wider mode")

// ErrUnjustified names a request with no reason.
var ErrUnjustified = errors.New("an escalation needs a justification")

// wider reports whether dst is strictly above src on the ladder. The order is
// the lattice: read-only < workspace < danger.
func wider(src, dst Mode) bool {
	switch {
	case src == ModeReadOnly && (dst == ModeWorkspace || dst == ModeDangerFull):
		return true
	case src == ModeWorkspace && dst == ModeDangerFull:
		return true
	}
	return false
}

// Escalate asks the approval seam for one strictly-wider run.
//
// The capability's own mode is untouched: the escalation builds a throwaway
// capability for the wider mode, checks the operation under it, and hands the
// caller the verdict — never a capability it can keep. A nil seam means nobody
// can be asked, and the request fails closed exactly as the original refusal
// did; an unavailable seam is not a silent yes.
//
// The caller must honour AllowedOnce for exactly one operation.
func (c *Capability) Escalate(e Escalation, seam *approval.Seam) (Decision, approval.Outcome) {
	if !wider(e.From, e.To) {
		return Decision{false, ErrNotWider.Error()}, approval.Unavailable
	}
	if trimSpaces(e.Justification) == "" {
		return Decision{false, ErrUnjustified.Error()}, approval.Unavailable
	}
	if seam == nil {
		// Nobody to ask. The refusal stands, and the reason names the missing
		// seam rather than pretending the operation was reconsidered.
		return Decision{false, "no approval seam is registered; the escalation cannot be asked"}, approval.Unavailable
	}

	// Re-check the operation under the wider mode before asking. There is no
	// point putting a question to a person for an operation the wider mode
	// would still refuse — that burns an approval on a guaranteed no.
	widerCap := New(e.To, c.root, c.tmpRoots, c.enforce)
	precheck := widerCap.AllowsPath(verbOf(e.Operation), e.Operation.Path)
	if !precheck.Allowed {
		return Decision{false, "the wider mode would still refuse: " + precheck.Reason}, approval.Rejected
	}

	outcome := seam.Ask(approval.Request{
		Scope:     "sandbox-escalation:" + e.To.String(),
		Action:    escalationAction(e.Operation, e.Justification),
		PromptID:  e.PromptID,
		Mode:      e.Permission,
		Workspace: e.Workspace,
	})
	if outcome != approval.AllowedOnce {
		return Decision{false, "the escalation was " + outcome.String()}, outcome
	}
	return precheck, outcome
}

// escalationAction names what is being asked for. A shell operation carries
// no path — the command itself is what the audit must show, so an operator
// reading the record sees "shell git push — …" rather than "shell  — …".
func escalationAction(op Operation, justification string) string {
	what := op.Path
	if op.Verb == "shell" && op.Command != "" {
		what = op.Command
	}
	return fmt.Sprintf("%s %s — %s", op.Verb, what, justification)
}

// verbOf maps an operation to the AllowsPath verb. A shell operation under
// escalation is judged by the mode it runs in, which the wider capability
// already encodes.
func verbOf(op Operation) string {
	switch op.Verb {
	case "read":
		return "read"
	case "shell":
		// A shell under a wider mode is permitted by the mode itself; the path
		// check is the workspace boundary, which a shell does not name.
		return "write"
	default:
		return op.Verb
	}
}

// trimSpaces is strings.TrimSpace without importing strings here for one call.
func trimSpaces(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}
