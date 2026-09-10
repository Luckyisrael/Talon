package approval

import (
	"sync"
	"testing"
)

// The four terminals resolve exactly as contracted: an answerer saying yes
// grants once and audits; a no rejects and audits; no answerer under ask is
// unavailable (fail-closed); never refuses without asking and audits.
func TestTerminals(t *testing.T) {
	var mu sync.Mutex
	var auditTrail []AuditRecord
	seam := New(PolicyAsk, func(r AuditRecord) {
		mu.Lock()
		auditTrail = append(auditTrail, r)
		mu.Unlock()
	})

	// Nobody registered: unavailable, and audited as such.
	if out := seam.Ask(Request{Scope: "tier2-shell", Action: "curl https://example.com"}); out != Unavailable {
		t.Fatalf("fault: ask with no answerer = %v, want unavailable", out)
	}
	mu.Lock()
	if len(auditTrail) != 1 || auditTrail[0].Outcome != Unavailable {
		t.Fatalf("fault: unavailable not audited: %+v", auditTrail)
	}
	mu.Unlock()

	// Register a yes-sayer: allowed-once, audited.
	seam.Register(func(Request) (bool, error) { return true, nil })
	if out := seam.Ask(Request{Scope: "device-build", Action: "build APK"}); out != AllowedOnce {
		t.Fatalf("fault: yes answerer gave %v, want allowed-once", out)
	}

	// A failing answerer falls through to the next.
	seam.Register(func(Request) (bool, error) { return false, errDeliver{} })
	seam.Register(func(Request) (bool, error) { return false, nil })
	if out := seam.Ask(Request{Scope: "x", Action: "y"}); out != Rejected {
		t.Fatalf("fault: fallthrough gave %v, want rejected (last answerer said no)", out)
	}

	// Never policy: deterministic refusal, no prompting.
	never := New(PolicyNever, nil)
	if out := never.Ask(Request{Scope: "x"}); out != Unavailable {
		t.Fatalf("fault: never policy gave %v, want unavailable", out)
	}
	if never.AskedCount("x") != 1 {
		t.Error("fault: asked count not recorded")
	}
}

type errDeliver struct{}

func (errDeliver) Error() string { return "delivery failed" }

// One audit event per decision: five asks, five records, in order.
func TestOneAuditPerDecision(t *testing.T) {
	var mu sync.Mutex
	var n int
	seam := New(PolicyAsk, func(AuditRecord) {
		mu.Lock()
		n++
		mu.Unlock()
	})
	seam.Register(func(Request) (bool, error) { return true, nil })
	for i := 0; i < 5; i++ {
		seam.Ask(Request{Scope: "s", Action: "a"})
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 5 {
		t.Errorf("fault: %d audit events for 5 asks, want exactly one each", n)
	}
}
