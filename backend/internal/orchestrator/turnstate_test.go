package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// Builders for a transcript in the stored shape: a settled previous turn
// (turn_end marker), the opener, then alternating tool_use / tool_result.
func tsTurnEnd() ai.Message {
	return ai.Message{Role: "assistant", Blocks: []ai.Block{{Type: "turn_end", Status: "completed", Rounds: 3}}}
}

func tsUse(id, name, input string) ai.Message {
	return ai.Message{Role: "assistant", Blocks: []ai.Block{
		{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage(input)},
	}}
}

func tsResult(id, content string, isErr bool) ai.Message {
	return ai.Message{Role: "user", Blocks: []ai.Block{
		{Type: "tool_result", ToolUseID: id, Content: content, IsError: isErr},
	}}
}

// spiralTranscript is the observed failure shape: a previous settled turn,
// a fresh "add a screen" opener, then a round spiral that re-reads the same
// file repeatedly and writes nothing — plus one check verdict and one error.
func spiralTranscript() []ai.Message {
	return []ai.Message{
		tsTurnEnd(),
		ai.UserText("Add a profile tab to the app"),
		tsUse("r1", "read_file", `{"path":"app/tabs.tsx"}`),
		tsResult("r1", "export const Tabs = …", false),
		tsUse("r2", "read_file", `{"path":"app/tabs.tsx","offset":80}`),
		tsResult("r2", "export const Tabs = …", false),
		tsUse("r3", "grep_codebase", `{"pattern":"Profile"}`),
		tsResult("r3", "app/main.tsx:12", false),
		tsUse("c1", "run_typecheck", `{}`),
		tsResult("c1", `{"passed":false,"summary":"TS2304 in app/profile.tsx"}`, false),
		tsUse("w1", "write_file", `{"path":"app/profile.tsx"}`),
		tsResult("w1", "wrote app/profile.tsx", false),
		tsUse("e1", "read_file", `{"path":"app/missing.tsx"}`),
		tsResult("e1", "ENOENT: no such file", true),
	}
}

func TestTurnStateStartFindsTurnBoundary(t *testing.T) {
	msgs := spiralTranscript()
	if got := turnStateStart(msgs); got != 1 {
		t.Errorf("turnStateStart = %d, want 1 (just after the turn_end marker)", got)
	}
	if got := turnStateStart(nil); got != 0 {
		t.Errorf("turnStateStart(nil) = %d, want 0", got)
	}
	// No marker anywhere: the whole replay counts as this turn.
	bare := msgs[1:]
	if got := turnStateStart(bare); got != 0 {
		t.Errorf("turnStateStart with no marker = %d, want 0", got)
	}
}

func TestScanTurnStateCountsActivity(t *testing.T) {
	msgs := spiralTranscript()
	scan := scanTurnState(msgs, turnStateStart(msgs))

	if scan.goal != "Add a profile tab to the app" {
		t.Errorf("goal = %q, want the turn opener", scan.goal)
	}
	if scan.readCalls != 4 {
		t.Errorf("readCalls = %d, want 4 (2 read_file + grep + missing read)", scan.readCalls)
	}
	if scan.reads["app/tabs.tsx"] != 2 {
		t.Errorf("tabs.tsx read count = %d, want ×2", scan.reads["app/tabs.tsx"])
	}
	if scan.writeCalls != 1 || scan.writes["app/profile.tsx"] != 1 {
		t.Errorf("writes = %d (%v), want 1 of app/profile.tsx", scan.writeCalls, scan.writes)
	}
	if len(scan.checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(scan.checks))
	}
	if scan.checks[0].tool != "run_typecheck" || scan.checks[0].verdict != "FAIL" {
		t.Errorf("check = %+v, want run_typecheck FAIL", scan.checks[0])
	}
	if !strings.Contains(scan.checks[0].head, "TS2304") {
		t.Errorf("check head = %q, want the summary excerpt", scan.checks[0].head)
	}
	if scan.errCount != 1 || !strings.Contains(scan.lastErr, "ENOENT") {
		t.Errorf("errors = %d (%q), want 1 naming ENOENT", scan.errCount, scan.lastErr)
	}
}

func TestScanTurnStateIgnoresEarlierTurnsAndNudges(t *testing.T) {
	msgs := []ai.Message{
		tsTurnEnd(),
		ai.UserText("Old turn prompt"),
		tsUse("old", "read_file", `{"path":"old.tsx"}`),
		tsResult("old", "body", false),
		tsTurnEnd(),
		ai.UserText("New turn prompt"),
		// A verify nudge and a skill block injected mid-turn must not count.
		ai.UserText("You have made 8 file writes since the last passing verification."),
	}
	scan := scanTurnState(msgs, turnStateStart(msgs))
	if scan.goal != "New turn prompt" {
		t.Errorf("goal = %q, want the new turn's opener", scan.goal)
	}
	if scan.readCalls != 0 || scan.writeCalls != 0 {
		t.Errorf("activity from before the boundary leaked in: reads=%d writes=%d",
			scan.readCalls, scan.writeCalls)
	}
}

func TestTurnStateNextDirectives(t *testing.T) {
	idle := &turnStateScan{readCalls: 9}
	if got := turnStateNext("build", idle, 4, 60, 0); !strings.Contains(got, "No file writes after 4 rounds") {
		t.Errorf("idle round 4 directive = %q, want the stop-gathering command", got)
	}
	if got := turnStateNext("build", idle, 3, 60, 0); !strings.Contains(got, "Orient") {
		t.Errorf("round 3 directive = %q, want the soft orientation line", got)
	}

	// Plan mode never commands a write, and past its read budget commands
	// the plan instead.
	if got := turnStateNext("plan", &turnStateScan{readCalls: 7}, 5, 60, 0); !strings.Contains(got, "write the plan now") {
		t.Errorf("plan directive = %q, want 'write the plan now'", got)
	}
	if got := turnStateNext("plan", &turnStateScan{readCalls: 2}, 5, 60, 0); strings.Contains(got, "No file writes") {
		t.Errorf("plan directive = %q, must not command writes", got)
	}

	// Budget pressure outranks scoping guidance.
	if got := turnStateNext("build", idle, 46, 60, 0); !strings.Contains(got, "budget nearly spent") {
		t.Errorf("round 46 directive = %q, want the budget warning", got)
	}

	// Unverified writes escalate to the check command.
	wrote := &turnStateScan{writeCalls: 9, writes: map[string]int{"a.ts": 9}}
	if got := turnStateNext("build", wrote, 10, 60, 8); !strings.Contains(got, "run run_checks now") {
		t.Errorf("unverified directive = %q, want the verify command", got)
	}
}

func TestRenderTurnStateCard(t *testing.T) {
	msgs := spiralTranscript()
	scan := scanTurnState(msgs, turnStateStart(msgs))
	card := renderTurnState(scan, 7, 60, "build", 1)

	for _, want := range []string{
		"## Turn state (auto",
		"Goal: Add a profile tab to the app",
		"Round 7/60",
		"reads: 4 (app/tabs.tsx ×2",
		"writes: 1 (app/profile.tsx) · unverified: 1",
		"run_typecheck FAIL (TS2304",
		"Last error: read_file: ENOENT",
		"NEXT:",
	} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q:\n%s", want, card)
		}
	}
	if strings.Count(card, "\n") > 9 {
		t.Errorf("card grew past its line budget (%d newlines):\n%s", strings.Count(card, "\n"), card)
	}
}

func TestAppendTurnStateDoesNotMutateStoredTranscript(t *testing.T) {
	// send and full are the SAME slice here — the exact hazard, since
	// TrimSupersededToolOutput can return the caller's slice unchanged.
	// The append must copy before it writes, or the card leaks into the
	// persisted transcript and accumulates round over round.
	full := spiralTranscript()
	blocksBefore := make([]int, len(full))
	for i, m := range full {
		blocksBefore[i] = len(m.Blocks)
	}

	out := appendTurnState(full, full, 1, 4, 60, "build", NewSession(1, 1, "", nil))
	if len(out) != len(full) {
		t.Fatalf("append changed message count: %d → %d", len(full), len(out))
	}
	last := out[len(out)-1]
	if last.Role != "user" || last.Blocks[len(last.Blocks)-1].Type != "text" {
		t.Fatalf("card not appended to trailing user message: %+v", last)
	}
	if card := last.Blocks[len(last.Blocks)-1].Text; !strings.Contains(card, "## Turn state") {
		t.Errorf("appended block is not the card: %q", card)
	}

	// The stored transcript stays clean and unaliased.
	for i, m := range full {
		if len(m.Blocks) != blocksBefore[i] {
			t.Fatalf("message %d block count changed: %d → %d", i, blocksBefore[i], len(m.Blocks))
		}
		for _, b := range m.Blocks {
			if strings.Contains(b.Text, "## Turn state") || b.Text == "mutated" {
				t.Errorf("card leaked into stored transcript at message %d", i)
			}
		}
	}
	// Mutating the returned card must not reach the source.
	out[len(out)-1].Blocks[len(out[len(out)-1].Blocks)-1].Text = "mutated"
	for i, m := range full {
		for _, b := range m.Blocks {
			if b.Text == "mutated" {
				t.Errorf("returned blocks alias the stored transcript at message %d", i)
			}
		}
	}
}

func TestAppendTurnStateFlagOff(t *testing.T) {
	t.Setenv("TALONSTUDIO_TURN_STATE", "off")
	full := spiralTranscript()
	out := appendTurnState(full, full, 1, 4, 60, "build", NewSession(1, 1, "", nil))
	if len(out) != len(full) {
		t.Errorf("flag-off still changed the wire copy: %d → %d", len(full), len(out))
	}
	for _, b := range out[len(out)-1].Blocks {
		if strings.Contains(b.Text, "## Turn state") {
			t.Error("flag-off still rendered the card")
		}
	}
}

func TestAppendTurnStateFallsBackToNewMessage(t *testing.T) {
	// A wire copy whose tail is an assistant message (defensive: the loop
	// normally ends rounds on tool results) gets the card as its own user
	// turn rather than being rewritten.
	full := []ai.Message{
		ai.UserText("prompt"),
		ai.AssistantText("thinking out loud"),
	}
	out := appendTurnState(full, full, 0, 1, 60, "build", NewSession(1, 1, "", nil))
	if len(out) != 3 || out[2].Role != "user" {
		t.Fatalf("want a new trailing user message, got %d messages, tail %+v", len(out), out[len(out)-1])
	}
	if !strings.Contains(out[2].Text(), "## Turn state") {
		t.Errorf("trailing message is not the card: %q", out[2].Text())
	}
}
