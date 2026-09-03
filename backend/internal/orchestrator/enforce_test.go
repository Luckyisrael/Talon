package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/ai"
)

func writeResult(id string) ai.Block {
	return ai.Block{Type: "tool_result", ToolUseID: id, Content: "ok"}
}

func verifyResultBlock(id string, passed bool) ai.Block {
	data, _ := json.Marshal(map[string]any{"passed": passed})
	return ai.Block{Type: "tool_result", ToolUseID: id, Content: string(data)}
}

func TestEnforcePeriodicVerifyNudgesAtThreshold(t *testing.T) {
	o := &Orchestrator{}
	sess := newTestSession(t)

	var conversation []ai.Message
	outcome := &BuildOutcome{}
	// 7 writes: below threshold, no nudge.
	for i := 0; i < 7; i++ {
		id := string(rune('a' + i))
		o.enforcePeriodicVerify(sess,
			[]ai.ToolUseBlock{{ID: id, Name: "write_file", Input: []byte(`{"path":"a.tsx"}`)}},
			[]ai.Block{writeResult(id)}, RunOptions{}, &conversation, outcome)
	}
	if len(conversation) != 0 {
		t.Fatalf("expected no nudge below threshold, got %d messages", len(conversation))
	}
	// 8th write: nudge fires.
	o.enforcePeriodicVerify(sess,
		[]ai.ToolUseBlock{{ID: "h", Name: "edit_file", Input: []byte(`{"path":"a.tsx"}`)}},
		[]ai.Block{writeResult("h")}, RunOptions{}, &conversation, outcome)
	if len(conversation) != 1 {
		t.Fatalf("expected 1 nudge at threshold, got %d", len(conversation))
	}
	text := string(conversation[0].Blocks[0].Text)
	if !strings.Contains(text, "run_checks") {
		t.Errorf("nudge should name run_checks, got %q", text)
	}
}

func TestEnforcePeriodicVerifyResetsOnPass(t *testing.T) {
	o := &Orchestrator{}
	sess := newTestSession(t)

	var conversation []ai.Message
	outcome := &BuildOutcome{}
	for i := 0; i < 8; i++ {
		id := string(rune('a' + i))
		o.enforcePeriodicVerify(sess,
			[]ai.ToolUseBlock{{ID: id, Name: "write_file", Input: []byte(`{"path":"a.tsx"}`)}},
			[]ai.Block{writeResult(id)}, RunOptions{}, &conversation, outcome)
	}
	if n := sess.WritesUnverified(); n != 8 {
		t.Fatalf("expected 8 unverified writes, got %d", n)
	}
	// A passing check resets; a failing check does not.
	o.enforcePeriodicVerify(sess,
		[]ai.ToolUseBlock{{ID: "v", Name: "run_checks", Input: []byte(`{}`)}},
		[]ai.Block{verifyResultBlock("v", false)}, RunOptions{}, &conversation, outcome)
	if n := sess.WritesUnverified(); n != 8 {
		t.Errorf("failed check must not reset, got %d", n)
	}
	o.enforcePeriodicVerify(sess,
		[]ai.ToolUseBlock{{ID: "w", Name: "run_checks", Input: []byte(`{}`)}},
		[]ai.Block{verifyResultBlock("w", true)}, RunOptions{}, &conversation, outcome)
	if n := sess.WritesUnverified(); n != 0 {
		t.Errorf("passing check must reset, got %d", n)
	}
}

func TestPendingPlanTasksOnExhausted(t *testing.T) {
	sess := newTestSession(t)
	sess.AddTask("plan:screen:Home", "Home screen")
	sess.AddTask("plan:screen:About", "About screen")
	sess.MarkPlanTask("plan:screen:Home", "Home screen", "completed", "done")
	remaining := pendingPlanTasks(sess)
	if len(remaining) != 1 || remaining[0] != "About screen" {
		t.Errorf("expected [About screen], got %v", remaining)
	}
}

func TestScreenBaseNameStripsQualifier(t *testing.T) {
	if got := screenBaseName("Portfolio (tab)"); got != "Portfolio" {
		t.Errorf("got %q", got)
	}
	if got := screenBaseName("Send"); got != "Send" {
		t.Errorf("got %q", got)
	}
	if got := planSlug(screenBaseName("NFTs (tab)")); got != "nfts" {
		t.Errorf("got %q", got)
	}
}

func TestMarkScreenMatchesQualifiedNames(t *testing.T) {	o := &Orchestrator{}
	sess := newTestSession(t)
	// Seeded the way seedPlanTasks seeds them: qualifier stripped for the ID.
	sess.AddTask("plan:screen:"+planSlug(screenBaseName("Portfolio (tab)")), "Screen: Portfolio (tab)")
	o.markScreenTasksFromFiles(sess, []string{"app/(tabs)/portfolio.tsx"})
	for _, task := range sess.GetTasks() {
		if task.Status != "completed" {
			t.Errorf("expected Portfolio screen auto-marked, got %+v", task)
		}
	}
}

func TestRemainingNoteSplitsScreensAndFeatures(t *testing.T) {	sess := newTestSession(t)
	sess.AddTask("plan:screen:portfolio", "Screen: Portfolio")
	sess.AddTask("plan:feature:3", "Feature: pull-to-refresh")
	note := remainingNote(sess, "Verified, but stopping")
	if !strings.Contains(note, "1 of 1 plan screens still unbuilt") {
		t.Errorf("should report the screen as unbuilt: %q", note)
	}
	if !strings.Contains(note, "Unconfirmed features (1)") {
		t.Errorf("should report the feature as unconfirmed: %q", note)
	}
	sess.MarkPlanTask("plan:screen:portfolio", "Screen: Portfolio", "completed", "route written")
	if note := remainingNote(sess, "Verified, but stopping"); strings.Contains(note, "unbuilt") {
		t.Errorf("no screens should be reported unbuilt: %q", note)
	}
}

func TestProbeTargetExtraction(t *testing.T) {
	cases := []struct {
		tool  string
		input string
		want  string
	}{
		{"run_shell", `{"command":"grep -n foo node_modules/@solana/web3.js/lib/index.d.ts | head"}`, "@solana/web3.js"},
		{"run_shell", `{"command":"ls app/"}`, ""},
		{"read_file", `{"path":"node_modules/@solana-mobile/mobile-wallet-adapter-protocol/lib/types/index.d.ts"}`, "@solana-mobile/mobile-wallet-adapter-protocol"},
		{"read_file", `{"path":"app/index.tsx"}`, ""},
		{"grep_codebase", `{"pattern":"authorize","glob":"*.ts"}`, ""},
		{"write_file", `{"path":"node_modules/x"}`, ""},
	}
	for _, c := range cases {
		if got := probeTarget(c.tool, []byte(c.input)); got != c.want {
			t.Errorf("probeTarget(%s, %s) = %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

func TestNoteProbeCountsPerTurn(t *testing.T) {
	sess := newTestSession(t)
	if n := sess.NoteProbe("@solana/web3.js"); n != 1 {
		t.Errorf("got %d", n)
	}
	if n := sess.NoteProbe("@solana/web3.js"); n != 2 {
		t.Errorf("got %d", n)
	}
	sess.ResetWriteCounts()
	if n := sess.NoteProbe("@solana/web3.js"); n != 1 {
		t.Errorf("expected reset, got %d", n)
	}
}
