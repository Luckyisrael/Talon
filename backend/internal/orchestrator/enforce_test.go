package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/store"
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

func TestMarkScreenMatchesQualifiedNames(t *testing.T) {
	o := &Orchestrator{}
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

func TestRemainingNoteSplitsScreensAndFeatures(t *testing.T) {
	sess := newTestSession(t)
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

func TestAuditUnmarksPhantomScreens(t *testing.T) {
	o := &Orchestrator{}
	sess := newTestSession(t)
	sess.AddTask("plan:screen:wallet", "Screen: Wallet")
	sess.AddTask("plan:screen:settings", "Screen: Settings")
	sess.MarkPlanTask("plan:screen:wallet", "Screen: Wallet", "completed", "claimed")
	sess.MarkPlanTask("plan:screen:settings", "Screen: Settings", "completed", "claimed")

	exists := map[string]bool{"app/(tabs)/wallet.tsx": true}
	o.auditScreenTasks(sess, RunOptions{
		Exists: func(path string) bool { return exists[path] },
	})

	statusOf := func(id string) string {
		for _, task := range sess.GetTasks() {
			if task.ID == id {
				return task.Status
			}
		}
		return "missing"
	}
	if got := statusOf("plan:screen:wallet"); got != "completed" {
		t.Errorf("built screen should stay completed, got %q", got)
	}
	if got := statusOf("plan:screen:settings"); got != "pending" {
		t.Errorf("phantom screen should flip back to pending, got %q", got)
	}
	remaining := pendingPlanTasks(sess)
	if len(remaining) != 1 {
		t.Errorf("expected the phantom in remaining_tasks, got %v", remaining)
	}
}

func TestWantsDeviceBuild(t *testing.T) {
	yes := []string{
		"build the APK and install it",
		"run it on the device for me",
		"show me a preview",
		"deploy a release build",
		"test it on the emulator",
	}
	for _, prompt := range yes {
		if !WantsDeviceBuild(prompt) {
			t.Errorf("expected device build for %q", prompt)
		}
	}
	no := []string{
		"build the wallet screen",
		"add a comment noting the module purpose",
		"rebuild the checklist",
		"fix the type error",
		"",
	}
	for _, prompt := range no {
		if WantsDeviceBuild(prompt) {
			t.Errorf("expected no device build for %q", prompt)
		}
	}
}

func TestWantsApproval(t *testing.T) {
	yes := []string{
		"yes",
		"OK",
		"yep, run it",
		"go ahead",
		"approved",
		"looks good",
		"sure — push it",
		"lgtm",
	}
	for _, prompt := range yes {
		if !WantsApproval(prompt) {
			t.Errorf("expected approval consent for %q", prompt)
		}
	}
	no := []string{
		"",
		"maybe later",
		"pick the other one",
		"red, not blue", // negation-outranks: "not" kills the "ok" inside... (contains no yes-word anyway)
		"yes, but not now",
		"no",
		"don't",
		"wait",
	}
	for _, prompt := range no {
		if WantsApproval(prompt) {
			t.Errorf("expected no approval consent for %q", prompt)
		}
	}
}

func TestAnswerNegated(t *testing.T) {
	neg := []string{
		"no",
		"don't install it",
		"I said no",
		"never do that again",
		"wait for me",
		"cancelled",
	}
	for _, prompt := range neg {
		if !AnswerNegated(prompt) {
			t.Errorf("expected negated for %q", prompt)
		}
	}
	pos := []string{
		"yes",
		"build the apk and install it",
		"run it on the device for me",
		"go ahead and push",
	}
	for _, prompt := range pos {
		if AnswerNegated(prompt) {
			t.Errorf("expected not negated for %q", prompt)
		}
	}
}

func TestBuildApkGuardrail(t *testing.T) {
	o := &Orchestrator{}
	sess := newTestSession(t)
	sess.SetBuildRequested(false)
	d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), RunOptions{})
	if d.Allowed || d.Rule != "scope" {
		t.Errorf("unprompted build_apk must be refused, got %+v", d)
	}
	sess.SetBuildRequested(true)
	d = o.guardrail(sess, "build_apk", json.RawMessage(`{}`), RunOptions{})
	if !d.Allowed {
		t.Errorf("requested build_apk must pass the gate, got %+v", d)
	}
}

// The Fix-1 contract: an approval granted mid-turn (stored via Answer) must
// unlock build_apk even when the session's own prompt text did not ask for
// a device build — answers land as tool_results, invisible to text scans.
func TestBuildApkGuardrailApprovalBinding(t *testing.T) {
	st, err := store.New(t.TempDir() + "/appr.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("appr@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "appr", "1.0.0", "appr-slug", "com.test.appr", "", "")

	o := &Orchestrator{store: st}
	sess := NewSession(77, p.ID, t.TempDir(), events.NewSessionEmitter(77, 128))
	sess.SetBuildRequested(false)

	// No approval: still refused.
	if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), RunOptions{}); d.Allowed {
		t.Fatal("no approval, no prompt ask — must refuse")
	}

	// User answers "yes, build the APK": approval binds to prompt 77.
	if !orchestratorWantsBuild(t, "Yes — build the APK and install it on the device") {
		t.Fatal("answer text should classify as a device-build approval")
	}
	if err := st.GrantApproval(p.ID, 77, store.ScopeDeviceBuild); err != nil {
		t.Fatal(err)
	}
	if !o.approved(77) {
		t.Fatal("approval should be readable for the prompt")
	}
	if d := o.guardrail(sess, "build_apk", json.RawMessage(`{}`), RunOptions{}); !d.Allowed {
		t.Fatalf("granted approval must unlock build_apk, got %+v", d)
	}

	// The approval is prompt-scoped: a different prompt stays locked.
	if o.approved(78) {
		t.Fatal("approval must not leak across prompts")
	}
}

func orchestratorWantsBuild(t *testing.T, s string) bool {
	t.Helper()
	return WantsDeviceBuild(s)
}
