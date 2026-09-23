// Package approval is the H4 consent seam: one place where a sensitive action
// asks the user, once, and the answer binds.
//
// The contract (from DSH, adapted to Talon's turn shapes):
//
//   - Every sensitive action requests approval through this seam with a
//     scope (a stable string naming the class of action) and the exact
//     thing to be done.
//   - A request yields exactly one of: allowed-once, rejected, cancelled,
//     unavailable. "Once" is literal — a grant covers this action, this
//     turn; the next sensitive action asks again.
//   - Policy is ask (default: prompt the user) or never (refuse without
//     prompting — for headless/CI compositions where nobody can answer).
//   - Every decision — grant, refusal, cancellation, unavailability — is
//     audited to the session log. The user can see what was asked and what
//     was allowed; nobody has to trust the agent's own summary.
//
// Talon already has the bones: the ask_user tool collects answers,
// GrantApproval(ScopeDeviceBuild) records them, turn_approvals is the binding
// table. This package names the seam those bones hang on.
package approval

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luckyisrael/talon/backend/internal/hooks"
)

// Outcome is the terminal result of one approval request.
type Outcome int

const (
	// AllowedOnce means the user consented to this action, this once.
	AllowedOnce Outcome = iota
	// Rejected means the user saw the request and said no.
	Rejected
	// Cancelled means the request died before an answer (timeout, turn end).
	Cancelled
	// Unavailable means nobody could be asked: no answerer registered, or
	// policy is never. Fail-closed — never a silent yes.
	Unavailable
)

func (o Outcome) String() string {
	switch o {
	case AllowedOnce:
		return "allowed-once"
	case Rejected:
		return "rejected"
	case Cancelled:
		return "cancelled"
	case Unavailable:
		return "unavailable"
	}
	return "unknown"
}

// Policy decides how requests resolve when the user is not standing by.
type Policy int

const (
	// PolicyAsk prompts the user and waits (bounded) for an answer.
	PolicyAsk Policy = iota
	// PolicyNever refuses without prompting: headless compositions where
	// nobody can answer. Deterministic, auditable, fail-closed.
	PolicyNever
)

// Request is one sensitive action asking for consent.
type Request struct {
	// Scope names the class of action ("device-build", "tier2-shell", …).
	Scope string
	// Action is the exact thing to be done, shown verbatim to the user.
	Action string
	// PromptID / SessionID locate the turn asking.
	PromptID int64
	// Mode is the requester's permission posture (W9). The zero value is
	// the empty spelling of ModeAsk — every pre-W9 caller keeps today's
	// prompting behavior byte for byte.
	Mode Mode
	// Workspace is the project root, when known — the location standing
	// grants (.talon/approvals.json) are read from and recorded into.
	// Empty means no standing-grant lookup or recording for this request.
	Workspace string
}

// AuditRecord is one immutable decision, as appended to the session log.
type AuditRecord struct {
	Scope    string
	Action   string
	Outcome  Outcome
	At       time.Time
	Reason   string // why, when the outcome is not AllowedOnce
	PromptID int64
}

// Answerer delivers the question to the user and reports their answer.
// ok=false means delivery failed — an unavailable, not a rejection.
type Answerer func(req Request) (approved bool, err error)

// Seam is the single approval gate. Construct one per composition; wire its
// Audit sink to the session log (H1) so every decision lands in the
// append-only record.
type Seam struct {
	policy Policy

	mu        sync.Mutex
	answerers []Answerer
	audit     func(AuditRecord)

	// HardDeny is W9's unwaivable class (secrets, egress exfil, path
	// escapes), injected at composition time so this package neither
	// duplicates the shell classifier nor imports its consumers. It runs
	// BEFORE auto mode and BEFORE any standing grant: when it names a
	// rule, the request is rejected and no allowlist entry can revive it.
	// Nil means the composition declared no seam-level hard-deny class —
	// the shell's tierBlocked still holds upstream of the seam.
	HardDeny func(Request) (rule string, denied bool)

	// grants counts per-scope grants in this composition's lifetime. A grant
	// is one-shot by contract, but the count lets the audit surface how many
	// times a scope was asked.
	asked map[string]int
}

// New builds the seam. policy is ask (interactive) or never (headless).
// audit, when non-nil, receives every decision — grants, refusals,
// cancellations, unavailability alike.
func New(policy Policy, audit func(AuditRecord)) *Seam {
	return &Seam{policy: policy, audit: audit, asked: map[string]int{}}
}

// Register adds an answerer. The last registered is asked first; a nil
// answerer is not stored. Composition-time wiring, not per-request.
func (s *Seam) Register(a Answerer) {
	if a == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answerers = append(s.answerers, a)
}

// Request asks for consent once. The outcome is one of the four terminals —
// never "unknown", never retried here. Every path audits.
func (s *Seam) Ask(req Request) Outcome {
	s.mu.Lock()
	s.asked[req.Scope]++
	pol := s.policy
	// Copy the answerers under the lock, then ask outside it: an answerer
	// may block on the user, and holding the seam lock across that would
	// serialize unrelated turns.
	answerers := make([]Answerer, len(s.answerers))
	copy(answerers, s.answerers)
	s.mu.Unlock()

	if pol == PolicyNever {
		s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Unavailable,
			Reason: "policy never: headless compositions refuse without prompting", PromptID: req.PromptID})
		return Unavailable
	}

	// W9 permission modes compose here, after the composition's never
	// (headless fail-closed is absolute) and before any prompt. Each
	// branch audits exactly one event; with mode empty/ask, flag off and
	// no HardDeny hook wired, every branch is a no-op and the answerer
	// loop below runs byte-identically to pre-W9.
	mode := req.Mode
	if mode == "" {
		mode = ModeAsk
	}
	hdRule, hardDenied := "", false
	if s.HardDeny != nil {
		hdRule, hardDenied = s.HardDeny(req)
	}

	// W10 ApprovalRequest: the workspace's hooks see every approval ask —
	// after PolicyNever (headless fail-closed is absolute) and computed
	// alongside the hard-deny class. deny refuses this act with the hook's
	// reason; a *hook's* allow grants it except under read-only (the
	// posture is absolute) or when hard-deny matched (no hook waives the
	// unwaivable). ask — and a hook's own failure, failed closed as a
	// rejection — continues into the mode switch and the answerer loop
	// below. Three-layer gate so hookless flows stay byte-identical: nil
	// registry (flag off / no config), no ApprovalRequest entries (a
	// loaded registry's empty-event Check also answers Allow — only a
	// sync entry that actually ran may act), and async entries never gate.
	// (Approval-layer executions audit through this record — the reason
	// names the hook; the loop's dispatches also get hook/exec rows.)
	if reg := hooks.Load(req.Workspace); reg != nil {
		var syncRan atomic.Int32
		d, herr := reg.CheckAudited(context.Background(), hooks.Payload{
			Event: hooks.ApprovalRequest, Prompt: req.Action,
			Workspace: req.Workspace, PromptID: req.PromptID,
		}, func(rec hooks.ExecRecord) {
			if !rec.Async {
				syncRan.Add(1)
			}
		})
		switch {
		case herr != nil:
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Rejected,
				Reason: "approval hook failed (W10, failed closed): " + herr.Error(), PromptID: req.PromptID})
			return Rejected
		case d.Decision == hooks.Deny:
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Rejected,
				Reason: "hook deny (W10): " + d.Reason, PromptID: req.PromptID})
			return Rejected
		case syncRan.Load() > 0 && d.Decision == hooks.Allow && mode != ModeReadOnly && !hardDenied:
			reason := "hook allow (W10)"
			if d.Reason != "" {
				reason += ": " + d.Reason
			}
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: AllowedOnce,
				Reason: reason, PromptID: req.PromptID})
			return AllowedOnce
		}
	}
	switch mode {
	case ModeReadOnly:
		// The posture that must not mutate: every seam act is a structured
		// refusal, escalation included — asking "may I write?" from a mode
		// that was chosen to never write is a contradiction.
		s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Rejected,
			Reason: "permission mode read-only refused " + req.Scope, PromptID: req.PromptID})
		return Rejected
	case ModeAuto:
		if IsHumanInitiated(req.Scope) {
			// A person's button press always confirms — auto is the
			// posture for MODEL-initiated acts, not for replacing the
			// confirmation dialog the person just opened. Falls through
			// to the ask below (grant replay is human-guarded too).
			break
		}
		if hardDenied {
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Rejected,
				Reason: "hard-deny:" + hdRule + " — auto mode cannot waive it", PromptID: req.PromptID})
			return Rejected
		}
		s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: AllowedOnce,
			Reason: "auto mode (W9): allowed without prompting", PromptID: req.PromptID})
		return AllowedOnce
	case ModeAcceptEdits:
		if IsEditClass(req) {
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: AllowedOnce,
				Reason: "accept-edits (W9): edit-class act auto-allowed", PromptID: req.PromptID})
			return AllowedOnce
		}
		// Non-edit acts fall through: grant replay, then the prompt.
	}

	// Standing-grant replay (W9.3): exact scope+action match with a valid
	// hash, flag on, model-initiated scope only — a human's button press
	// always confirms. A hard-deny match beats the grant: no allowlist
	// entry waives the unwaivable class, so the ask still happens (or,
	// under auto, the rejection above already landed).
	if !IsHumanInitiated(req.Scope) {
		if ok, hash := lookupGrant(req.Workspace, req.Scope, req.Action); ok && !hardDenied {
			s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: AllowedOnce,
				Reason: "standing-grant replay (W9): sha256 " + hash[:12], PromptID: req.PromptID})
			return AllowedOnce
		}
	}

	for i := len(answerers) - 1; i >= 0; i-- {
		approved, err := answerers[i](req)
		if err != nil {
			// Delivery failed: try the next answerer; if none remain, this
			// is unavailable, not rejection — the user never saw the ask.
			continue
		}
		out := Rejected
		if approved {
			out = AllowedOnce
			// W9.3: with standing grants on, a live approval of a
			// model-initiated act becomes a recorded standing grant —
			// the next identical ask replays instead of prompting.
			// recordGrant is itself flag- and input-gated.
			if !IsHumanInitiated(req.Scope) {
				recordGrant(req.Workspace, req.Scope, req.Action)
			}
		}
		s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: out, PromptID: req.PromptID})
		return out
	}

	s.record(AuditRecord{Scope: req.Scope, Action: req.Action, Outcome: Unavailable,
		Reason: "no answerer available", PromptID: req.PromptID})
	return Unavailable
}

// record appends to the audit sink, if wired.
func (s *Seam) record(r AuditRecord) {
	s.mu.Lock()
	audit := s.audit
	s.mu.Unlock()
	if audit != nil {
		audit(r)
	}
}

// AskedCount reports how many times a scope has been asked in this seam's
// lifetime — the honest-meter surface for the audit UI.
func (s *Seam) AskedCount(scope string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked[scope]
}
