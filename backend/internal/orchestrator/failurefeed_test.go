package orchestrator

import (
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/events"
)

// W5 — diagnostics feed the failure index: a failing verification verdict
// consults the seeded signatures the way a tool error always did, a
// tsc-shaped lsp_diagnostics verdict hits the run_typecheck-scoped ts
// patterns by SHAPE, passing verdicts never consult, and recorded
// patterns are normalized at load so an LSP code recorded as "TS17004"
// matches the lowercased text it will be tested against.

func feedSession(t *testing.T) *SessionState {
	t.Helper()
	return NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
}

const lspVerdict = `{"passed":false,"errors":"src/app.ts(4,1): error TS2322: Type 'string' is not assignable to type 'number'.","summary":"1 error(s)"}`

func TestFailureReminderFeedsFromLSPVerdict(t *testing.T) {
	sess := feedSession(t)
	rem := failureReminder(sess, "lsp_diagnostics", lspVerdict, false)
	if !strings.Contains(rem, "Known issue") || !strings.Contains(rem, "known fix") {
		t.Fatalf("verdict must hit the seeded ts signature, got %q", rem)
	}
	if !strings.Contains(rem, "narrow instead of casting") {
		t.Errorf("want the run_typecheck-scoped known fix delivered by shape, got %q", rem)
	}

	// Same failure next call: the cooldown suppresses the repeat.
	if again := failureReminder(sess, "lsp_diagnostics", lspVerdict, false); again != "" {
		t.Errorf("cooldown must suppress the repeat, got %q", again)
	}
}

func TestFailureReminderIgnoresPassingAndCleanText(t *testing.T) {
	sess := feedSession(t)
	if rem := failureReminder(sess, "lsp_diagnostics", `{"passed":true,"summary":"no diagnostics"}`, false); rem != "" {
		t.Errorf("passing verdict must not consult: %q", rem)
	}
	if rem := failureReminder(sess, "lsp_diagnostics", `{"passed":false,"summary":"1 warning(s)"}`, false); rem != "" {
		t.Errorf("verdict with no error text must not consult: %q", rem)
	}
	if rem := failureReminder(sess, "grep_codebase", "nothing found", false); rem != "" {
		t.Errorf("plain non-error output must not consult: %q", rem)
	}
	// Errors keep the old contract: the error text itself is consulted
	// under the tool's own name, with no shape fallback.
	if rem := failureReminder(sess, "grep_codebase", "unmatched error text", true); rem != "" {
		t.Errorf("unmatched error must not remind: %q", rem)
	}
}

// Recorded patterns normalize at load: a match recorded in LSP's own case
// fires against the normalized diagnostic, and text no seed covers proves
// it is the recorded pattern speaking (seeds would answer first).
func TestRuntimePatternNormalizesLSPCodes(t *testing.T) {
	rp, ok := runtimePattern("", "TS17004", "bad lib ref", "use the new import path", "")
	if !ok {
		t.Fatal("recorded pattern must compile")
	}
	sess := feedSession(t)
	sess.RuntimePatterns = append(sess.RuntimePatterns, rp)

	rem := failureReminder(sess, "lsp_diagnostics",
		`{"passed":false,"errors":"src/a.ts(2,1): error TS17004: bad lib ref.","summary":"1 error(s)"}`, false)
	if !strings.Contains(rem, "use the new import path") {
		t.Fatalf("normalized recorded pattern must fire, got %q", rem)
	}
}
