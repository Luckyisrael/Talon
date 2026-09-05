package orchestrator

import (
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/runtime"
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

func TestCrashIndexCollectsDeviceSteps(t *testing.T) {
	sess := newTestSession(t)
	if sess.CrashIndex() != "" {
		t.Fatal("empty index should render empty")
	}
	sess.NoteCrash(runtime.CrashEntry{Tx: "connect", Tool: "test_wallet_roundtrip", Excerpt: "fakewallet not installed"})
	sess.NoteCrash(runtime.CrashEntry{Tool: "run_boot", Excerpt: "alive but silent"})
	idx := sess.CrashIndex()
	if !strings.Contains(idx, "test_wallet_roundtrip [connect]") || !strings.Contains(idx, "run_boot") {
		t.Errorf("index should name both steps:\n%s", idx)
	}
}

func TestIsDeviceStep(t *testing.T) {
	for _, tool := range []string{"run_boot", "run_vision", "run_interact", "test_wallet_roundtrip", "capture_screenshot", "tail_logcat"} {
		if !isDeviceStep(tool) {
			t.Errorf("%s should be a device step", tool)
		}
	}
	for _, tool := range []string{"run_typecheck", "write_file", "read_skill"} {
		if isDeviceStep(tool) {
			t.Errorf("%s should not be a device step", tool)
		}
	}
}

func TestCrashTx(t *testing.T) {
	if got := crashTx([]byte(`{"tx":"connect"}`)); got != "connect" {
		t.Errorf("tx field wins, got %q", got)
	}
	if got := crashTx([]byte(`{"intended_screen":"Wallet"}`)); got != "Wallet" {
		t.Errorf("intended_screen fallback, got %q", got)
	}
	if got := crashTx([]byte(`{}`)); got != "" {
		t.Errorf("empty args, got %q", got)
	}
}
