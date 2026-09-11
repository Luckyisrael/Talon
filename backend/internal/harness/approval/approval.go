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
	"sync"
	"time"
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
