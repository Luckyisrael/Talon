package orchestrator

import "testing"

// W1.4: the verify-report accumulator is what the run_gate tool renders to the
// model. Its lifecycle has one subtle invariant: appendVerifyGate multiplies
// the carried OverallPassed (cur && status != "fail"), so the baseline at New
// AND after every reset must be true ("no failure recorded yet"). The old
// zero-valued reset silently made every post-chain report read fail even when
// all gates passed — a lie nothing consumed while run_gate sat unregistered.
func TestVerifyReportLifecycle(t *testing.T) {
	o := New(nil, nil, nil)

	// Fresh construction: no failures, no gates recorded.
	if vr := o.LastVerifyReport(); vr == nil || !vr.OverallPassed || len(vr.Gates) != 0 {
		t.Fatalf("fresh report: %+v", vr)
	}

	// A fail sticks to the carried verdict.
	o.appendVerifyGate("typecheck", "fail", "TS2322")
	if vr := o.LastVerifyReport(); vr.OverallPassed || len(vr.Gates) != 1 {
		t.Fatalf("fail gate: %+v", vr)
	}

	// Reset restores the passing baseline — the regression this guards.
	o.resetVerifyReport()
	if vr := o.LastVerifyReport(); vr == nil || !vr.OverallPassed || len(vr.Gates) != 0 {
		t.Fatalf("post-reset baseline must be OverallPassed=true with no gates: %+v", vr)
	}

	// A clean chain stays clean; "unavailable" never flips it (matches the
	// ladder: unavailable lets the build through and is listed, not judged).
	o.appendVerifyGate("typecheck", "pass", "ok")
	o.appendVerifyGate("boot", "unavailable", "no device")
	if vr := o.LastVerifyReport(); !vr.OverallPassed || len(vr.Gates) != 2 {
		t.Fatalf("pass+unavailable chain must stay passed: %+v", vr)
	}

	// One later fail flips the whole report.
	o.appendVerifyGate("vision", "fail", "screenshot mismatch")
	if vr := o.LastVerifyReport(); vr.OverallPassed || len(vr.Gates) != 3 {
		t.Fatalf("late fail must flip the carried verdict: %+v", vr)
	}
}
