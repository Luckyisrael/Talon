package orchestrator

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// W5 — diagnostics feed the failure index.
//
// Two feeds converge here: tool errors (as always) and failing
// verification VERDICTS — passed=false output whose errors ARE the failure
// text. Gate zero's lsp_diagnostics prints tsc-shaped lines by design
// (lsp.Diagnostic.FormatTsc), so its verdict text reaches the same
// signatures tsc output does, including the run_typecheck-scoped seeds:
// consulted by SHAPE on the verdict path because a verify verdict speaks
// compiler text — the ts\d+ patterns are that shape, whatever checker
// printed it. Recorded patterns (record_failure_pattern) are normalized
// at load (runtimePattern) so a match recorded as "TS2322" matches the
// lowercased diagnostic it was learned from.

// failureReminder returns the one-line known-issue reminder for a tool
// result, or "" when nothing matches or the cooldown is still running.
func failureReminder(sess *SessionState, callName, output string, isError bool) string {
	text := ""
	if isError {
		text = output
	} else if strings.Contains(output, `"passed":false`) {
		// Only failing verification verdicts feed the index; the cheap
		// substring gate keeps the common passing path allocation-free.
		var vr verifyResult
		if json.Unmarshal([]byte(output), &vr) == nil && !vr.Passed {
			text = vr.Errors
		}
	}
	if text == "" {
		return ""
	}

	patterns := knowledge.DefaultFailureIndex()
	for _, rp := range sess.RuntimePatterns {
		if rp.Match != nil {
			patterns = append(patterns, rp)
		}
	}
	if pat, ok := knowledge.Lookup(patterns, callName, text); ok {
		return cooldownReminder(sess, callName, text, pat)
	}
	// The verdict path also consults the run_typecheck-scoped seeds: their
	// scope is the tsc-shaped text, which gate zero reproduces byte for
	// byte, so scoping by the printing tool would hide a known fix behind
	// a name change. Error paths keep their exact scoping — a random tool
	// error that happens to quote a ts code is not a typecheck failure.
	if !isError && callName != "run_typecheck" {
		if pat, ok := knowledge.Lookup(patterns, "run_typecheck", text); ok {
			return cooldownReminder(sess, callName, text, pat)
		}
	}
	return ""
}

// cooldownReminder fires the reminder only when the cooldown for this
// (tool, normalized failure) has expired, then arms it — one nudge per
// window while the model works the fix.
func cooldownReminder(sess *SessionState, callName, text string, pat knowledge.FailurePattern) string {
	norm := knowledge.NormalizeError(text)
	if sess.CooldownLeft(callName, norm) != 0 {
		return ""
	}
	sess.NoteCooldown(callName, norm, pat.CooldownRounds)
	return "\n\n" + pat.Reminder()
}

// runtimePattern builds a FailurePattern from a record_failure_pattern
// line — normalizing the match into the same shape NormalizeError gives
// the text it will be tested against, so an LSP/tsc code recorded as
// "TS2322" matches the lowercased diagnostic it was learned from.
func runtimePattern(tool, match, cause, fix, skill string) (knowledge.FailurePattern, bool) {
	re, err := regexp.Compile(knowledge.NormalizeError(match))
	if err != nil {
		return knowledge.FailurePattern{}, false
	}
	return knowledge.FailurePattern{
		Tool:           tool,
		Match:          re,
		Cause:          cause,
		Fix:            fix,
		Skill:          skill,
		CooldownRounds: 3,
	}, true
}
