package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// W5 gate-zero tests: the lsp rung exists only under TALONSTUDIO_LSP,
// an unavailable server degrades to a recorded skip (never a blocked
// build), and a real failure stops the ladder at gate zero WITHOUT
// consulting tsc — the eval gate's three clauses.

func gateStages(gates []verifyGate) []string {
	out := make([]string, 0, len(gates))
	for _, g := range gates {
		out = append(out, g.stage)
	}
	return out
}

func hasSkip(got []string, want string) bool {
	for _, s := range got {
		if s == want {
			return true
		}
	}
	return false
}

// TestVerifyGatesLSPFlagGated pins flag-off byte-identity of the ladder:
// deployment runs every flag unset, and a permanently-skipped "lsp" entry
// in every deployment's SkippedGates would be a behavior change, not a
// note. Flag on, lsp_diagnostics rides first — before tsc.
func TestVerifyGatesLSPFlagGated(t *testing.T) {
	o := New(nil, nil, nil)
	baseline := []string{"typecheck", "lint", "bundle", "gradle", "boot", "vision", "interact"}
	if got := gateStages(o.verifyGates()); !equalStrings(got, baseline) {
		t.Fatalf("flag-off ladder = %v, want byte-identical %v", got, baseline)
	}

	t.Setenv("TALONSTUDIO_LSP", "v1")
	want := append([]string{"lsp"}, baseline...)
	if got := gateStages(o.verifyGates()); !equalStrings(got, want) {
		t.Fatalf("flag-on ladder = %v, want %v", got, want)
	}
}

// TestVerifyLadderLSPUnavailableContinues pins the degradation clause:
// the tool's LSP_UNAVAILABLE error result makes runGate classify the gate
// "unavailable" — the skip lands in SkippedGates and the ladder continues
// to tsc as before. A missing checker never blocks the build.
func TestVerifyLadderLSPUnavailableContinues(t *testing.T) {
	t.Setenv("TALONSTUDIO_LSP", "v1")
	o := New(nil, nil, nil)
	sess := newTestSession(t)
	var calls []string
	opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
		"lsp_diagnostics": {out: "LSP_UNAVAILABLE: no language server could start", isError: true},
		"run_typecheck":   {out: verifyJSON(t, true, "", "no type errors")},
		"run_lint":        {out: verifyJSON(t, true, "", "no lint errors")},
	}, &calls)}

	v := o.verify(context.Background(), sess, opts)
	if !v.ok {
		t.Fatalf("an unavailable lsp gate must not block the build: %q", v.feedback)
	}
	want := []string{"lsp_diagnostics", "run_typecheck", "run_lint",
		"run_bundle", "run_gradle", "run_boot", "run_vision", "run_interact"}
	if !equalStrings(calls, want) {
		t.Errorf("gates ran %v, want %v (skip recorded, ladder continues)", calls, want)
	}
	if got := sess.SkippedGates(); !hasSkip(got, "lsp") {
		t.Errorf("SkippedGates = %v, want the lsp skip recorded", got)
	}
}

// TestVerifyLadderLSPFailureNamesErrorWithoutTsc pins the eval gate's
// first clause: a planted error is named with ONLY lsp_diagnostics
// consulted — run_typecheck never runs — and the repair guidance carries
// the tsc-shaped line plus the confirmation path, so the failure index's
// run_typecheck patterns match gate-zero output unchanged.
func TestVerifyLadderLSPFailureNamesErrorWithoutTsc(t *testing.T) {
	t.Setenv("TALONSTUDIO_LSP", "v1")
	o := New(nil, nil, nil)
	sess := newTestSession(t)
	var calls []string
	opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
		"lsp_diagnostics": {out: verifyJSON(t, false,
			"src/app.ts(4,1): error TS2322: Type 'string' is not assignable to type 'number'.",
			"1 error(s)")},
		"run_typecheck": {out: verifyJSON(t, false, "run_typecheck must not be reached", "1 type error(s)")},
	}, &calls)}

	v := o.verify(context.Background(), sess, opts)
	if v.ok {
		t.Fatal("an lsp error must stop the ladder at gate zero")
	}
	if len(calls) != 1 || calls[0] != "lsp_diagnostics" {
		t.Errorf("gates ran %v, want only lsp_diagnostics (tsc never runs on a gate-zero failure)", calls)
	}
	for _, want := range []string{"language server", "src/app.ts", "lsp_diagnostics", "run_typecheck"} {
		if !strings.Contains(v.feedback, want) {
			t.Errorf("feedback missing %q:\n%s", want, v.feedback)
		}
	}
}
