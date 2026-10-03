package api

// L54 / FM-VRFYa — the wire claims only what could have been checked.
//
// The gap, caught on camera: every emitter shipped `verified:false`
// whenever no check ran — the no-changes path leaves the zero value —
// and both clients key the red chip on `verified`'s PRESENCE, so
// "checks did not pass · 1 round" came out on every single turn, a claim
// the backend never made. The wire contract already says absent keys
// claim nothing; the emitters just never omitted the key.
//
// Fails-first pins (TEST_RULES rule 1):
//
// V1. A no-changes success outcome must produce a `done` frame with NO
//     verified key — absence IS the no-claim state. Pre-fix the frame
//     ships `"verified":false` (the exact `done-no-changes after 1
//     rounds` shape from the live log).
//                                [TestDoneFrameOmitsVerifiedWhenNothingRan]
//
// V2. Polarity: when checks COULD have run (files touched, a policy that
//     classified the sweep) the verdict still rides — a genuine failure
//     stays claimable on both clients.        [TestDoneFrameKeeps… ×2]
//
// V3. The shared rule (verdictClaim) and the marker's payload builder
//     (turnEndEvidence) — added WITH the fix (Go has no module-red seam:
//     a missing symbol fails compilation and masks every other red), and
//     pinned here so the three emitters cannot drift apart.
//         [TestClaimVerdictRule] [TestTurnEndEvidenceOmitsVerdictWhenNothingRan]

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/orchestrator"
)

// finishFrame runs finishBuildStream against a recorder and returns the
// `done` event's payload. httptest.ResponseRecorder implements Flusher.
func finishFrame(t *testing.T, outcome *orchestrator.BuildOutcome) map[string]any {
	t.Helper()
	h := &PromptHandler{}
	rec := httptest.NewRecorder()
	h.finishBuildStream(rec, rec, outcome, nil) //nolint:errcheck // wire shape is the assertion
	for _, part := range strings.Split(rec.Body.String(), "\n\n") {
		if !strings.HasPrefix(part, "event: done") {
			continue
		}
		idx := strings.Index(part, "data: ")
		if idx < 0 {
			t.Fatalf("done event has no data line: %q", part)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(part[idx+len("data: "):]), &payload); err != nil {
			t.Fatalf("done payload is not JSON: %v", err)
		}
		return payload
	}
	t.Fatalf("no done frame in %q", rec.Body.String())
	return nil
}

func TestDoneFrameOmitsVerifiedWhenNothingRan(t *testing.T) {
	// The live log's shape: a turn that completed with nothing to change —
	// one round, no files, no sweep. No check could have run, so there is
	// no verdict to carry.
	frame := finishFrame(t, &orchestrator.BuildOutcome{
		Rounds:    1,
		FinalText: "Nothing to change.",
	})

	if v, ok := frame["verified"]; ok {
		t.Errorf("no-changes done frame must omit verified (absence = no claim), got %v", v)
	}
	if v, ok := frame["verified_best_effort"]; ok {
		t.Errorf("no-changes done frame must omit verified_best_effort too, got %v", v)
	}
	// The rest of the frame still reports what the turn did.
	if frame["status"] != "success" {
		t.Errorf("status = %v, want success", frame["status"])
	}
	if r, ok := frame["rounds"].(float64); !ok || r != 1 {
		t.Errorf("rounds = %v, want 1", frame["rounds"])
	}
}

func TestDoneFrameKeepsTheVerdictWhenChecksRan(t *testing.T) {
	// Polarity: files touched + a policy that classified the sweep — the
	// verdict is earned and must survive, false included (a real failure
	// is exactly when the red chip is honest).
	frame := finishFrame(t, &orchestrator.BuildOutcome{
		Rounds:       2,
		FilesTouched: []string{"app/wallet.tsx"},
		VerifyPolicy: "strict",
		Verified:     false,
	})

	v, ok := frame["verified"].(bool)
	if !ok {
		t.Fatalf("verified must stay present when checks ran, key=%v", frame["verified"])
	}
	if v {
		t.Errorf("verified = true, want false for the failing sweep")
	}
	if p, _ := frame["verify_policy"].(string); p != "strict" {
		t.Errorf("verify_policy = %v, want strict", frame["verify_policy"])
	}
}

func TestDoneFrameKeepsAVerifiedClaimWhenTheLadderPassed(t *testing.T) {
	// The other half of the polarity: verified:true rides with its policy.
	frame := finishFrame(t, &orchestrator.BuildOutcome{
		Rounds:       3,
		FilesTouched: []string{"app/wallet.tsx"},
		VerifyPolicy: "strict",
		Verified:     true,
	})
	if v, ok := frame["verified"].(bool); !ok || !v {
		t.Errorf("verified = %v (present=%v), want true present", frame["verified"], ok)
	}
}

// TestClaimVerdictRule pins the shared rule all three emitters (done frame,
// turn_end marker, non-stream response) read: files/policy/skips could have
// been checked and the turn was not paused or round-exhausted out of its
// checks.
func TestClaimVerdictRule(t *testing.T) {
	noClaim := map[string]*orchestrator.BuildOutcome{
		"no changes":        {Rounds: 1, FinalText: "Nothing to change."},
		"paused":            {FilesTouched: nil, Rounds: 2, Paused: true},
		"round exhausted":   {Rounds: 5, Exhausted: true},
		"failed, no work":   {Rounds: 1, FinalText: "provider errored"},
		"stalled rambling":  {Rounds: 2, Stalled: true},
		"paused with files": {FilesTouched: []string{"a.ts"}, Paused: true},
	}
	for name, o := range noClaim {
		if verdictClaim(o) {
			t.Errorf("%s: verdictClaim = true, want false (nothing could have been checked)", name)
		}
	}
	if verdictClaim(nil) {
		t.Errorf("verdictClaim(nil) = true, want false")
	}

	claim := map[string]*orchestrator.BuildOutcome{
		"files touched":    {FilesTouched: []string{"app/wallet.tsx"}, VerifyPolicy: "strict"},
		"policy, no files": {VerifyPolicy: "besteffort", Verified: true},
		"skips recorded":   {SkippedGates: []string{"vision"}, VerifyPolicy: "strict", Verified: true},
	}
	for name, o := range claim {
		if !verdictClaim(o) {
			t.Errorf("%s: verdictClaim = false, want true (checks could have run)", name)
		}
	}
}

// TestTurnEndEvidenceOmitsVerdictWhenNothingRan pins the marker's payload
// shape: files/skips/policy always written (an empty payload still means
// "finished, nothing to verify"), verdict keys only when a check could have
// run.
func TestTurnEndEvidenceOmitsVerdictWhenNothingRan(t *testing.T) {
	quiet := turnEndEvidence(&orchestrator.BuildOutcome{Rounds: 1})
	if _, ok := quiet["verified"]; ok {
		t.Errorf("no-changes evidence must omit verified, got %v", quiet["verified"])
	}
	if _, ok := quiet["verified_best_effort"]; ok {
		t.Errorf("no-changes evidence must omit verified_best_effort, got %v", quiet["verified_best_effort"])
	}
	if _, ok := quiet["files_touched"]; !ok {
		t.Errorf("files_touched must stay written (present + empty = nothing to verify)")
	}
	if _, ok := quiet["verify_policy"]; !ok {
		t.Errorf("verify_policy must stay written")
	}

	checked := turnEndEvidence(&orchestrator.BuildOutcome{
		FilesTouched: []string{"app/wallet.tsx"},
		VerifyPolicy: "strict",
		Verified:     false,
	})
	if v, ok := checked["verified"].(bool); !ok || v {
		t.Errorf("verified = %v (present=%v), want false present when checks ran", checked["verified"], ok)
	}
}
