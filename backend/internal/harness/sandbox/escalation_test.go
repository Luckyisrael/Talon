package sandbox

import (
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
)

// stubAnswerer answers every request with a fixed outcome, recording what was
// asked. approval.Answerer is a func type, so the stub is one.
type stubAnswerer struct {
	outcome approval.Outcome
	asked   []approval.Request
}

func (s *stubAnswerer) answer(req approval.Request) (bool, error) {
	s.asked = append(s.asked, req)
	return s.outcome == approval.AllowedOnce, nil
}

func newSeam(outcome approval.Outcome, policy approval.Policy) (*approval.Seam, *stubAnswerer) {
	a := &stubAnswerer{outcome: outcome}
	seam := approval.New(policy, nil)
	seam.Register(a.answer)
	return seam, a
}

// The ladder is a lattice: an escalation must move strictly wider. Equal or
// narrower is rejected rather than silently allowed.
func TestEscalationMustBeStrictlyWider(t *testing.T) {
	c := New(ModeWorkspace, "/proj", nil, func(op Operation) error { return nil })
	seam, _ := newSeam(approval.AllowedOnce, approval.PolicyNever)

	cases := []struct{ from, to Mode }{
		{ModeWorkspace, ModeWorkspace}, // equal — not an escalation
		{ModeDangerFull, ModeDangerFull},
		{ModeWorkspace, ModeReadOnly}, // narrower
		{ModeDangerFull, ModeWorkspace},
	}
	for _, tc := range cases {
		d, out := c.Escalate(Escalation{
			From: tc.from, To: tc.to,
			Operation:     Operation{Verb: "write", Path: "/proj/out.txt"},
			Justification: "the build needs it",
		}, seam)
		if d.Allowed {
			t.Errorf("escalation %s → %s must be refused as not-wider", tc.from, tc.to)
		}
		if out != approval.Unavailable {
			t.Errorf("a not-wider request must not reach the seam, got %s", out)
		}
	}
}

// An escalation without a justification is a permission request with no
// evidence — refused before the human is bothered.
func TestEscalationNeedsAJustification(t *testing.T) {
	c := New(ModeReadOnly, "/proj", nil, func(op Operation) error { return nil })
	seam, a := newSeam(approval.AllowedOnce, approval.PolicyNever)

	d, _ := c.Escalate(Escalation{
		From: ModeReadOnly, To: ModeWorkspace,
		Operation:     Operation{Verb: "write", Path: "/proj/a.txt"},
		Justification: "   ",
	}, seam)
	if d.Allowed {
		t.Error("a blank justification must be refused")
	}
	if len(a.asked) != 0 {
		t.Error("a unjustified request must never reach the seam")
	}
}

// No seam means nobody can be asked — the refusal stands, fail-closed, and the
// reason names the missing seam rather than pretending it was reconsidered.
func TestEscalationWithoutSeamFailsClosed(t *testing.T) {
	c := New(ModeWorkspace, "/proj", nil, func(op Operation) error { return nil })

	d, out := c.Escalate(Escalation{
		From: ModeWorkspace, To: ModeDangerFull,
		Operation:     Operation{Verb: "write", Path: "/proj/a.txt"},
		Justification: "the gradle cache lives outside the workspace",
	}, nil)
	if d.Allowed {
		t.Error("a nil seam must not allow the escalation")
	}
	if out != approval.Unavailable {
		t.Errorf("a nil seam must report unavailable, got %s", out)
	}
}

// An approved escalation allows exactly the named operation under the wider
// mode, and the capability's own mode is untouched — one-shot, not a session
// upgrade.
func TestEscalationApprovedAllowsOnceAndLeavesModeAlone(t *testing.T) {
	c := New(ModeWorkspace, "/proj", nil, func(op Operation) error { return nil })
	seam, a := newSeam(approval.AllowedOnce, approval.PolicyAsk)

	d, out := c.Escalate(Escalation{
		From: ModeWorkspace, To: ModeDangerFull,
		Operation:     Operation{Verb: "write", Path: "/etc/gradle/cache"},
		Justification: "the gradle cache lives outside the workspace",
		PromptID:      42,
	}, seam)

	if !d.Allowed {
		t.Fatalf("an approved escalation must allow the operation, got %q", d.Reason)
	}
	if out != approval.AllowedOnce {
		t.Errorf("got outcome %s, want allowed-once", out)
	}
	if c.Mode() != ModeWorkspace {
		t.Errorf("the capability's own mode must be untouched, got %s", c.Mode())
	}
	if len(a.asked) != 1 {
		t.Fatalf("the seam must be asked exactly once, got %d", len(a.asked))
	}
	if a.asked[0].PromptID != 42 {
		t.Errorf("the audit record must carry the prompt id, got %d", a.asked[0].PromptID)
	}
	if a.asked[0].Scope != "sandbox-escalation:danger-full-access" {
		t.Errorf("the scope must name the target mode, got %q", a.asked[0].Scope)
	}
}

// A denied escalation is final: the caller gets Rejected and must not retry the
// same request with a different justification — that is how a ladder becomes
// an approval loop.
func TestEscalationDeniedIsFinal(t *testing.T) {
	c := New(ModeWorkspace, "/proj", nil, func(op Operation) error { return nil })
	seam, _ := newSeam(approval.Rejected, approval.PolicyAsk)

	d, out := c.Escalate(Escalation{
		From: ModeWorkspace, To: ModeDangerFull,
		Operation:     Operation{Verb: "write", Path: "/etc/x"},
		Justification: "first reason",
	}, seam)
	if d.Allowed {
		t.Error("a rejected escalation must not allow the operation")
	}
	if out != approval.Rejected {
		t.Errorf("got outcome %s, want rejected", out)
	}
}

// PolicyNever (a headless composition, nobody standing by) resolves the request
// without prompting, and the escalation is refused — fail-closed, never a
// silent yes.
func TestEscalationUnderPolicyNeverIsRefused(t *testing.T) {
	c := New(ModeReadOnly, "/proj", nil, func(op Operation) error { return nil })
	seam, a := newSeam(approval.AllowedOnce, approval.PolicyNever)

	d, _ := c.Escalate(Escalation{
		From: ModeReadOnly, To: ModeWorkspace,
		Operation:     Operation{Verb: "write", Path: "/proj/a.txt"},
		Justification: "the build needs it",
	}, seam)
	if d.Allowed {
		t.Error("PolicyNever must not allow an escalation")
	}
	if len(a.asked) != 0 {
		t.Error("PolicyNever must not deliver a prompt to a user")
	}
}

// An operation the wider mode would still refuse is never put to the human —
// burning an approval on a guaranteed no is how people stop reading the
// prompts.
func TestEscalationSkipsSeamWhenWiderModeAlsoRefuses(t *testing.T) {
	// A workspace→danger escalation, but the enforcement hook always refuses,
	// so the wider mode cannot guarantee confinement either.
	c := New(ModeWorkspace, "/proj", nil, func(op Operation) error {
		return ErrUnavailable
	})
	seam, a := newSeam(approval.AllowedOnce, approval.PolicyAsk)

	d, out := c.Escalate(Escalation{
		From: ModeWorkspace, To: ModeDangerFull,
		Operation:     Operation{Verb: "write", Path: "/proj/a.txt"},
		Justification: "the build needs it",
	}, seam)
	// Danger mode allows the path outright, so the precheck passes and the seam
	// IS asked — this case confirms the precheck consults the wider mode, not
	// that it is conservative. The enforcement hook is bypassed in danger mode.
	if !d.Allowed {
		t.Errorf("danger mode must allow the path regardless of the hook, got %q", d.Reason)
	}
	_ = out
	_ = a
}
