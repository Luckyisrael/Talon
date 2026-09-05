package agent

import (
	"regexp"
	"strings"
)

// FailurePattern is one known (tool, error) → fix mapping. The runtime index
// consults these before re-asking the model, so a known failure gets its
// known fix in one line instead of another reasoning round.
type FailurePattern struct {
	// Tool scopes the pattern; "" matches any tool.
	Tool string
	// Match is a regex over the normalized error text.
	Match *regexp.Regexp
	// Cause and Fix render as the one-line reminder. Keep Fix to one action.
	Cause string
	Fix   string
	// Skill names a skill whose body helps, or "" for none.
	Skill string
	// CooldownRounds suppresses re-injection while the model works the fix.
	CooldownRounds int
}

// NormalizeError lowercases, collapses paths/numbers/hashes/addresses to
// placeholders, and squeezes whitespace — so one signature matches the same
// failure across files, lines, and runs.
func NormalizeError(s string) string {
	s = strings.ToLower(s)
	replacements := []struct{ re, to string }{
		{`[0-9a-f]{7,40}`, "<hash>"},
		{`0x[0-9a-f]+`, "<hex>"},
		{`/[^\s:'"]+`, "<path>"},
		{`[a-z_]+/[a-z0-9_./-]+\.(tsx?|jsx?|json|gradle|kt|java)`, "<file>"},
		{`\b\d+\b`, "<n>"},
		{`\s+`, " "},
	}
	for _, r := range replacements {
		s = regexp.MustCompile(r.re).ReplaceAllString(s, r.to)
	}
	return strings.TrimSpace(s)
}

func mustPattern(tool, match, cause, fix, skill string, cooldown int) FailurePattern {
	return FailurePattern{
		Tool:           tool,
		Match:          regexp.MustCompile(match),
		Cause:          cause,
		Fix:            fix,
		Skill:          skill,
		CooldownRounds: cooldown,
	}
}

// DefaultFailureIndex is the hand-curated seed, drawn from
// failure-patterns/SKILL.md (which remains the human reference and the
// auto-attach path). Patterns match NORMALIZED text (see NormalizeError):
// lowercase, standalone numbers as <n>, paths as <path>. Deterministic and
// unit-tested; runtime additions arrive via record_failure_pattern.
func DefaultFailureIndex() []FailurePattern {
	const cd = 3
	return []FailurePattern{
		mustPattern("run_typecheck", `ts\d+.*not assignable`,
			"wrong type passed", "read the type definition, not the call site; narrow instead of casting", "", cd),
		mustPattern("run_typecheck", `ts\d+.*cannot find name`,
			"missing import", "check the import, the spelling, then whether the package is installed", "", cd),
		mustPattern("run_typecheck", `ts\d+.*has no exported member`,
			"API differs from memory", "read node_modules/<pkg>/index.d.ts for the real export name", "", cd),
		mustPattern("run_typecheck", `ts\d+.*possibly 'null'`,
			"unnarrowed null", "use ?. or an explicit check; no !-asserts in app code", "", cd),
		mustPattern("", `cannot find module`,
			"module not installed or unresolvable", "inspect_package first, then check tsconfig paths", "", cd),
		mustPattern("", `unable to resolve module`,
			"wrong import path", "verify the relative path or @/ alias setup", "expo-router-nav", cd),
		mustPattern("", `enoent`,
			"path does not exist", "list the parent directory instead of guessing the path", "", cd),
		mustPattern("", `eacces|permission denied`,
			"permission problem", "check ownership/mode; never chmod 777", "", cd),
		mustPattern("", `enospc|no space left`,
			"disk full", "clear caches and build outputs, not source", "", cd),
		mustPattern("", `user rejected|rejected the request|user cancelled|dismissed`,
			"user dismissed the wallet sheet", "treat as cancelled, restore resting UI; it is not a crash", "wallet-mwa", cd),
		mustPattern("", `rate limit|too many requests|quota exceeded`,
			"provider throttling", "back off and retry with a smaller step", "", cd),
		mustPattern("build_apk", `gradle|assemble|lintvital|aapt`,
			"native build failure", "read the failing task output from the bottom up; fix one task at a time", "", cd),
		mustPattern("", `hermes|syntaxerror|unexpected token`,
			"bundler syntax failure", "fix the flagged file's syntax; check imports resolve", "expo-router-nav", cd),
		mustPattern("", `device not found|no devices|device unauthorized|device offline`,
			"device unreachable", "check adb devices, reconnect, verify the target", "", cd),
		mustPattern("run_boot", `alive but silent|produced no log output`,
			"process up but app code never ran", "check the launch activity and read the logcat excerpt for the real crash", "", cd),
		mustPattern("run_boot", `did not come up|returned empty`,
			"process never started", "reinstall and check logcat *:E for the install or launch crash", "", cd),
		mustPattern("run_vision", `blank screen`,
			"screen drew nothing", "check the route renders content; look for an early return or a missing component", "", cd),
	}
}

// Lookup returns the first matching pattern for a tool failure, or false.
// Empty-tool patterns match every tool; tool-scoped patterns only theirs.
func Lookup(patterns []FailurePattern, tool, errText string) (FailurePattern, bool) {
	norm := NormalizeError(errText)
	for _, p := range patterns {
		if p.Tool != "" && p.Tool != tool {
			continue
		}
		if p.Match.MatchString(norm) {
			return p, true
		}
	}
	return FailurePattern{}, false
}

// Reminder renders the one-line system reminder injected ahead of the retry.
func (p FailurePattern) Reminder() string {
	s := "Known issue (" + p.Cause + ") — known fix: " + p.Fix + "."
	if p.Skill != "" {
		s += " See the " + p.Skill + " skill."
	}
	return s
}
