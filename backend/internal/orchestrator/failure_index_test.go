package orchestrator

import (
	"testing"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/events"
)

// The loop consults the failure index before re-asking: a known error gets a
// one-line reminder on the tool result, and the reminder is suppressed for
// the cooldown window while the model works the fix.
func TestFailureReminderFiresOncePerCooldown(t *testing.T) {
	sess := NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
	sess.SetRound(1)

	callName := "run_typecheck"
	output := "app/x.tsx(1,1): error TS2322: Type 'string' is not assignable to type 'number'."

	// Simulate one error round: lookup, cooldown check, reminder.
	patterns := agent.DefaultFailureIndex()
	pat, ok := agent.Lookup(patterns, callName, output)
	if !ok {
		t.Fatal("expected TS2322 to hit the failure index")
	}
	norm := agent.NormalizeError(output)
	if sess.CooldownLeft(callName, norm) != 0 {
		t.Fatal("cooldown should start at zero (reminder active)")
	}
	reminder := pat.Reminder()
	sess.NoteCooldown(callName, norm, pat.CooldownRounds)

	// Same error next round: reminder must be suppressed.
	if sess.CooldownLeft(callName, norm) != pat.CooldownRounds {
		t.Fatalf("cooldown should be %d, got %d", pat.CooldownRounds, sess.CooldownLeft(callName, norm))
	}
	sess.TickCooldowns()
	if sess.CooldownLeft(callName, norm) != pat.CooldownRounds-1 {
		t.Fatal("cooldown should tick down one round")
	}

	// After the window expires the reminder fires again.
	sess.TickCooldowns()
	sess.TickCooldowns()
	if sess.CooldownLeft(callName, norm) != 0 {
		t.Fatal("cooldown should expire after 3 ticks")
	}
	if reminder == "" || len(reminder) == 0 {
		t.Fatal("reminder must be a non-empty one-liner")
	}
}

// Runtime patterns recorded via record_failure_pattern parse back out of the
// memory format and augment the default index.
func TestParseFailurePattern(t *testing.T) {
	p := parseFailurePattern("tool=run_typecheck | match=TS17004 | cause=bad lib ref | fix=use the new import path")
	if p.tool != "run_typecheck" || p.match != "TS17004" || p.cause != "bad lib ref" || p.fix != "use the new import path" {
		t.Errorf("bad parse: %+v", p)
	}
	if bad := parseFailurePattern("no separators here"); bad.match != "" {
		t.Errorf("garbage should parse to empty match: %+v", bad)
	}
}
