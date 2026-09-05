package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
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
	sess.NoteCrash(runtime.CrashEntry{Tx: "connect", Tool: "test_wallet_roundtrip", Excerpt: "demo wallet not installed"})
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

func patchTestSession(t *testing.T, content string) (*SessionState, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return newTestSession(t), dir
}

// Flag off: interception owns nothing — the session behaves as today.
func TestPatchBufferOffOwnsNothing(t *testing.T) {
	sess, _ := patchTestSession(t, "v1")
	calls := []ai.ToolUseBlock{{ID: "1", Name: "try_edit", Input: json.RawMessage(`{"path":"a.txt","mode":"edit"}`)}}
	owned := (&Orchestrator{}).applyPatchBuffer(context.Background(), sess, calls, RunOptions{})
	if len(owned) != 0 {
		t.Fatal("flag off must own no calls")
	}
	if sess.PatchBuffer != nil {
		t.Fatal("flag off must not create a buffer")
	}
	if patchBufferOn(RunOptions{}) {
		t.Fatal("env default must be off")
	}
}

// Flag on, type-breaking edit: auto-reverted with Reverted: on the result.
func TestTryEditRevertsOnTypeFailure(t *testing.T) {
	sess, dir := patchTestSession(t, "v1")
	sess.WorkspacePath = dir
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		switch name {
		case "edit_file":
			_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("BROKEN"), 0o644)
			return "edited", false
		case "run_typecheck":
			return `{"passed":false,"summary":"1 type error(s)","errors":"a.txt: error TS2322"}`, false
		}
		return "", true
	}
	call := ai.ToolUseBlock{ID: "1", Name: "try_edit", Input: json.RawMessage(`{"path":"a.txt","mode":"edit","old":"v1","new":"BROKEN"}`)}
	res := (&Orchestrator{}).applyTryEdit(context.Background(), sess, call, RunOptions{Execute: exec, PatchBufferEnabled: true})
	if !res.IsError || !strings.Contains(res.Content, "Reverted:") {
		t.Fatalf("expected Reverted: result, got %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "v1" {
		t.Fatalf("file should be reverted to v1, got %q", data)
	}
}

// Flag on, fixing edit: kept, pre-image staged for keep_edit.
func TestTryEditKeepsOnTypePass(t *testing.T) {
	sess, dir := patchTestSession(t, "v1")
	sess.WorkspacePath = dir
	exec := func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		if name == "edit_file" {
			_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2"), 0o644)
			return "edited", false
		}
		return `{"passed":true,"summary":"no type errors"}`, false
	}
	call := ai.ToolUseBlock{ID: "1", Name: "try_edit", Input: json.RawMessage(`{"path":"a.txt","mode":"edit","old":"v1","new":"v2"}`)}
	res := (&Orchestrator{}).applyTryEdit(context.Background(), sess, call, RunOptions{Execute: exec, PatchBufferEnabled: true})
	if res.IsError {
		t.Fatalf("passing edit should succeed, got %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "v2" {
		t.Fatalf("file should hold v2, got %q", data)
	}
	pre, ok := sess.PatchBuffer.PreImage("a.txt")
	if !ok || string(pre) != "v1" {
		t.Fatalf("pre-image should be staged, got %q %v", pre, ok)
	}
	keep := ai.ToolUseBlock{ID: "2", Name: "keep_edit", Input: json.RawMessage(`{"path":"a.txt"}`)}
	kres := (&Orchestrator{}).applyKeepEdit(sess, keep)
	if kres.IsError {
		t.Fatalf("keep should succeed, got %+v", kres)
	}
	if _, ok := sess.PatchBuffer.PreImage("a.txt"); ok {
		t.Fatal("keep_edit should clear the pre-image")
	}
}
