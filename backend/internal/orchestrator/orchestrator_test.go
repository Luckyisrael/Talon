package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/store"
)

func newTestSession(t *testing.T) *SessionState {
	t.Helper()
	return NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
}

func loadKnowledge(t *testing.T) *knowledge.Registry {
	t.Helper()
	r, err := knowledge.Load("../../template/package.json")
	if err != nil {
		t.Fatalf("knowledge.Load: %v", err)
	}
	return r
}

func toolCall(name, input string) []ai.ToolUseBlock {
	return []ai.ToolUseBlock{{ID: "1", Name: name, Input: []byte(input)}}
}

// A triggered skill is injected once, then never again this session — the whole
// point of the per-session set is that guidance given holds without re-paying for
// it every turn.
func TestAutoAttachSkillsDedup(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	call := toolCall("write_file",
		`{"path":"components/Card.tsx","content":"import Animated from 'react-native-reanimated';"}`)

	first := o.autoAttachSkills(sess, call)
	if len(first) == 0 || !strings.Contains(first[0].Body, "Reanimated") {
		t.Fatalf("expected the reanimated skill injected the first time, got: %+v", first)
	}
	if first[0].Type != "context" || first[0].Kind != "skill" {
		t.Fatalf("expected a skill context block, got: %+v", first[0])
	}
	if second := o.autoAttachSkills(sess, call); len(second) != 0 {
		t.Errorf("expected dedup on the second trigger, got: %+v", second)
	}
}

// A skill the model read for itself must not then be auto-injected on a later
// trigger — MarkSkillSeen is the shared gate for both paths. The new
// talon-mobile skill bundle fires on the same install trigger; this test
// marks every skill that would fire as seen, then asserts the report is empty.
// That is the contract: read skills do not re-inject, regardless of which
// skill the trigger fan-out reaches.
func TestAutoAttachSkipsExplicitlyReadSkill(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	sess.MarkSkillSeen("wallet-mwa")
	sess.MarkSkillSeen("talon-mobile")

	call := toolCall("install_package", `{"name":"@solana-mobile/mobile-wallet-adapter-protocol-web3js"}`)
	if got := o.autoAttachSkills(sess, call); len(got) != 0 {
		t.Errorf("expected no injection for an already-seen skill, got: %+v", got)
	}
}

func TestAutoAttachNoTrigger(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	call := toolCall("write_file", `{"path":"lib/util.ts","content":"export const x = 1;"}`)
	if got := o.autoAttachSkills(sess, call); len(got) != 0 {
		t.Errorf("expected no injection without a trigger, got: %+v", got)
	}
}

func TestAutoAttachNilKnowledge(t *testing.T) {
	o := New(nil, nil, nil)
	sess := newTestSession(t)
	call := toolCall("write_file", `{"path":"app/index.tsx","content":"import 'react-native-reanimated';"}`)
	if got := o.autoAttachSkills(sess, call); len(got) != 0 {
		t.Errorf("nil knowledge must inject nothing, got: %+v", got)
	}
}

// MarkSkillSeen reports first-sight so auto-attach injects exactly once.
func TestMarkSkillSeen(t *testing.T) {
	sess := newTestSession(t)
	if !sess.MarkSkillSeen("x") {
		t.Error("first MarkSkillSeen should report true (newly seen)")
	}
	if sess.MarkSkillSeen("x") {
		t.Error("second MarkSkillSeen should report false (already seen)")
	}
}

// The integration check for Phase A: the base prompt leads, then the always-on
// rules, the preinstalled list read from the real template, and the skill
// manifest all land in the enriched system prompt.
func TestEnrichPromptInjectsKnowledge(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	out := o.enrichPromptWithContext(sess, "BASE PROMPT")

	if !strings.HasPrefix(out, "BASE PROMPT") {
		t.Error("the base system prompt should lead the enriched prompt")
	}
	for _, want := range []string{
		"Preinstalled packages", // from the rules block
		"expo-router",           // a real preinstalled package from the template
		"Skills available",      // manifest header
		"wallet-mwa",            // a skill listed in the manifest
	} {
		if !strings.Contains(out, want) {
			t.Errorf("enriched prompt missing %q", want)
		}
	}
	// The manifest carries names and descriptions only — never a full skill body.
	if body, _ := o.knowledge.SkillBody("wallet-mwa"); strings.Contains(out, body) {
		t.Error("the manifest leaked a full skill body into the cached prefix")
	}
}

// drainFor non-blockingly reads whatever the emitter has buffered and reports
// whether an event of the given type is among it.
func drainFor(em *events.SessionEmitter, typ events.EventType) bool {
	found := false
	for {
		select {
		case e := <-em.Events():
			if e.Type == typ {
				found = true
			}
		default:
			return found
		}
	}
}

// recordingExec is a ToolExecutor that notes whether it ran and with what.
func recordingExec(ran *bool) ToolExecutor {
	return func(_ context.Context, name string, input json.RawMessage) (string, bool) {
		*ran = true
		return "ok", false
	}
}

// A guardrail-violating call must be refused before it executes, come back as a
// failed tool_result the model can read, and surface a guardrail_reject event.
func TestExecuteToolsBlocksGuardrailViolation(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	ran := false
	opts := RunOptions{Execute: recordingExec(&ran), Exists: func(string) bool { return false }}

	calls := toolCall("install_package", `{"name":"@solana/wallet-adapter-react"}`)
	results := o.executeTools(context.Background(), sess, calls, opts)

	if ran {
		t.Fatal("a guardrail-violating tool call must not reach the executor")
	}
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("want a single is_error tool_result, got %+v", results)
	}
	if !strings.Contains(results[0].Content, "browser-wallet-package") {
		t.Errorf("the tool_result should name the rule so the model can correct, got %q", results[0].Content)
	}
	if !drainFor(sess.Emitter, events.GuardrailEvent) {
		t.Error("a guardrail_reject event should have been emitted")
	}
}

// A clean call runs normally — the guardrail is not in the way of ordinary work.
func TestExecuteToolsAllowsCleanCall(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	ran := false
	opts := RunOptions{Execute: recordingExec(&ran), Exists: func(string) bool { return false }}

	calls := toolCall("write_file", `{"path":"app/new.tsx","content":"export const x = 1"}`)
	results := o.executeTools(context.Background(), sess, calls, opts)

	if !ran {
		t.Fatal("a clean tool call must execute")
	}
	if results[0].IsError {
		t.Errorf("a clean call should not be an error: %q", results[0].Content)
	}
	// Mark-on-write: the file just written counts as read, so a follow-up edit
	// this turn is not blocked by read-before-write.
	if !sess.HasReadFile("app/new.tsx") {
		t.Error("a written file should be marked read so it can be edited afterwards")
	}
}

// Editing a file the model has not read this turn is refused through the loop,
// with existence supplied by the Exists option.
func TestExecuteToolsReadBeforeWrite(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	ran := false
	opts := RunOptions{Execute: recordingExec(&ran), Exists: func(string) bool { return true }}

	calls := toolCall("edit_file", `{"path":"app/(tabs)/index.tsx","old_string":"a","new_string":"b"}`)
	results := o.executeTools(context.Background(), sess, calls, opts)

	if ran {
		t.Fatal("editing an unread file must be refused before it runs")
	}
	if !results[0].IsError || !strings.Contains(results[0].Content, "read-before-write") {
		t.Errorf("want a read-before-write refusal, got %q", results[0].Content)
	}
}

// A tool that panics mid-execution must not take the build down with it. The
// crash is contained at the run() body both the parallel and sequential paths
// share: it comes back as an is_error tool_result carrying the crashing call's
// ID, every other call in the same turn still completes, and executeTools returns
// normally rather than letting the panic escape and kill the process.
func TestExecuteToolsRecoversFromPanic(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	// Panic on one call, succeed on the other, so the test proves both that the
	// crash is contained and that it does not poison its healthy neighbour.
	opts := RunOptions{
		Exists: func(string) bool { return false },
		Execute: func(_ context.Context, _ string, input json.RawMessage) (string, bool) {
			if strings.Contains(string(input), "boom") {
				panic("simulated tool crash")
			}
			return "ok", false
		},
	}

	// Distinct IDs so the assertions can prove each result carries its own call's.
	// No ReadOnlyTool → sequential, so results keep the calls' order.
	calls := []ai.ToolUseBlock{
		{ID: "call-boom", Name: "write_file", Input: []byte(`{"path":"app/boom.tsx","content":"x"}`)},
		{ID: "call-ok", Name: "write_file", Input: []byte(`{"path":"app/fine.tsx","content":"y"}`)},
	}

	// Reaching the next line at all is the first assertion: the panic did not escape.
	results := o.executeTools(context.Background(), sess, calls, opts)

	if len(results) != len(calls) {
		t.Fatalf("want %d results, got %d", len(calls), len(results))
	}

	// The crashing call: a tool_result, flagged as an error, carrying its own ID,
	// with language that tells the model the tool faulted (not its request) so it
	// tries a smaller step rather than retrying the same call.
	boom := results[0]
	if boom.Type != "tool_result" || !boom.IsError {
		t.Errorf("a panicking tool must yield an is_error tool_result, got %+v", boom)
	}
	if boom.ToolUseID != "call-boom" {
		t.Errorf("the error result must carry the crashing call's ID, got %q", boom.ToolUseID)
	}
	if !strings.Contains(boom.Content, "crashed") {
		t.Errorf("the result should tell the model the tool crashed, got %q", boom.Content)
	}

	// The neighbour ran to completion, untouched by the crash beside it.
	if ok := results[1]; ok.IsError || ok.ToolUseID != "call-ok" {
		t.Errorf("a healthy call next to a crashing one must still succeed, got %+v", ok)
	}
}

// The last line of defence for the "every tool_use needs a matching tool_result"
// rule: if some future path ever leaves a gap in the pre-sized results slice, the
// backfill turns that zero-value Block (empty Type — silently dropped by both
// serializers, orphaning its tool_use into a 400) into an error result carrying
// the call's ID, and never clobbers a result a run already set.
func TestBackfillMissingResults(t *testing.T) {
	calls := []ai.ToolUseBlock{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	results := make([]ai.Block, len(calls))
	// Simulate a run that set only the middle result and left the two ends as gaps.
	results[1] = ai.Block{Type: "tool_result", ToolUseID: "b", Content: "ok"}

	backfillMissingResults(results, calls)

	for i, r := range results {
		if r.Type != "tool_result" {
			t.Errorf("result %d has type %q; an empty Type is dropped by both serializers and orphans the tool_use", i, r.Type)
		}
		if r.ToolUseID != calls[i].ID {
			t.Errorf("result %d must carry its call's ID %q, got %q", i, calls[i].ID, r.ToolUseID)
		}
	}
	if !results[0].IsError || !results[2].IsError {
		t.Error("a backfilled gap must be flagged is_error so the model treats it as a failed call")
	}
	// The result the run already produced is left exactly as it was.
	if results[1].Content != "ok" || results[1].IsError {
		t.Errorf("backfill must not overwrite a result a run already set, got %+v", results[1])
	}
}

// ── the verification ladder ───────────────────────────

// gateResponse is one scripted answer from a verification tool.
type gateResponse struct {
	out     string
	isError bool
}

// ladderExec answers each verification tool from a script and records the order
// tools were called, so a test can prove the ladder stops at the first failure.
// Any unscripted tool passes cleanly.
func ladderExec(script map[string]gateResponse, calls *[]string) ToolExecutor {
	return func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
		if calls != nil {
			*calls = append(*calls, name)
		}
		if r, ok := script[name]; ok {
			return r.out, r.isError
		}
		return `{"passed":true}`, false
	}
}

// verifyJSON renders a tool's verdict the way the verify tools do.
func verifyJSON(t *testing.T, passed bool, errs, summary string) string {
	t.Helper()
	b, err := json.Marshal(verifyResult{Passed: passed, Errors: errs, Summary: summary})
	if err != nil {
		t.Fatalf("marshal verdict: %v", err)
	}
	return string(b)
}

// verifyStages drains the emitter and returns the (stage, status) of every verify
// event, in order — the trace the IDE renders as the verification ladder.
func verifyStages(em *events.SessionEmitter) [][2]string {
	var out [][2]string
	for {
		select {
		case e := <-em.Events():
			if e.Type == events.VerifyEvent {
				out = append(out, [2]string{e.Tool, e.Status})
			}
		default:
			return out
		}
	}
}

func hasStage(stages [][2]string, stage, status string) bool {
	for _, s := range stages {
		if s[0] == stage && s[1] == status {
			return true
		}
	}
	return false
}

func TestVerifyLadder(t *testing.T) {
	// Every gate clean: the ladder runs both rungs in order and reports done.
	t.Run("all gates pass", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		var calls []string
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: verifyJSON(t, true, "", "no type errors")},
			"run_lint":      {out: verifyJSON(t, true, "", "no lint errors")},
		}, &calls)}

		v := o.verify(context.Background(), sess, opts)
		if !v.ok {
			t.Fatalf("want ok, got feedback: %q", v.feedback)
		}
		if want := []string{"run_typecheck", "run_lint", "run_bundle", "run_gradle", "run_boot", "run_vision", "run_interact"}; !equalStrings(calls, want) {
			t.Errorf("gates ran %v, want %v (typecheck before lint, then the G4 gates in order)", calls, want)
		}
		stages := verifyStages(sess.Emitter)
		if !hasStage(stages, "typecheck", "pass") || !hasStage(stages, "lint", "pass") {
			t.Errorf("expected pass telemetry for both gates, got %v", stages)
		}
	})

	// The whole point of an ordered ladder: a typecheck failure stops it, and the
	// linter is never run on code that does not compile.
	t.Run("typecheck failure stops the ladder before lint", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		var calls []string
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: verifyJSON(t, false, "app/index.tsx(3,5): error TS2304: Cannot find name 'x'.", "1 type error(s)")},
			"run_lint":      {out: verifyJSON(t, false, "should never be reached", "lint errors")},
		}, &calls)}

		v := o.verify(context.Background(), sess, opts)
		if v.ok {
			t.Fatal("a typecheck failure must not pass the ladder")
		}
		if !strings.Contains(v.feedback, "does not compile") {
			t.Errorf("feedback should be the typecheck failure, got %q", v.feedback)
		}
		if !strings.Contains(v.feedback, "app/index.tsx") {
			t.Errorf("feedback should name the offending file, got %q", v.feedback)
		}
		if equalStrings(calls, []string{"run_typecheck", "run_lint"}) || len(calls) != 1 {
			t.Fatalf("lint must not run after a typecheck failure; calls = %v", calls)
		}
		stages := verifyStages(sess.Emitter)
		if hasStage(stages, "lint", "running") {
			t.Errorf("the lint gate should never have started, got %v", stages)
		}
	})

	// Once the code compiles, lint is the gate that can still send it back.
	t.Run("lint failure after a clean typecheck", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		var calls []string
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: verifyJSON(t, true, "", "no type errors")},
			"run_lint":      {out: verifyJSON(t, false, "app/index.tsx: 'foo' is assigned but never used", "lint errors")},
		}, &calls)}

		v := o.verify(context.Background(), sess, opts)
		if v.ok {
			t.Fatal("a lint failure must not pass the ladder")
		}
		if !strings.Contains(v.feedback, "ESLint") {
			t.Errorf("feedback should be the lint failure, got %q", v.feedback)
		}
		if !equalStrings(calls, []string{"run_typecheck", "run_lint"}) {
			t.Errorf("both gates should have run, got %v", calls)
		}
		if stages := verifyStages(sess.Emitter); !hasStage(stages, "lint", "fail") {
			t.Errorf("expected a lint fail event, got %v", stages)
		}
	})

	// A checker that cannot run is a platform problem, not the agent's code: keep
	// going rather than trapping clean code in a repair loop — but surface it as a
	// distinct "unavailable" event (with a log line) instead of a quiet skip, so a
	// real infra break is visible rather than reading as a pass.
	t.Run("a broken checker is surfaced as unavailable, not failed", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		var calls []string
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: "tsc: command not found", isError: true},
			"run_lint":      {out: verifyJSON(t, true, "", "no lint errors")},
		}, &calls)}

		v := o.verify(context.Background(), sess, opts)
		if !v.ok {
			t.Fatalf("a broken checker must not fail the build, got feedback: %q", v.feedback)
		}
		stages := verifyStages(sess.Emitter)
		if !hasStage(stages, "typecheck", "unavailable") {
			t.Errorf("the broken gate should be reported as unavailable, got %v", stages)
		}
		if !equalStrings(calls, []string{"run_typecheck", "run_lint", "run_bundle", "run_gradle", "run_boot", "run_vision", "run_interact"}) {
			t.Errorf("an unavailable gate must not stop the ladder, got %v", calls)
		}
	})

	// Output that does not parse means the tool's contract drifted; fail open so a
	// format change never blocks every build — and surface the drift, don't swallow
	// it.
	t.Run("unparseable output is surfaced as unavailable", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: "not json at all"},
			"run_lint":      {out: verifyJSON(t, true, "", "no lint errors")},
		}, nil)}

		v := o.verify(context.Background(), sess, opts)
		if !v.ok {
			t.Fatalf("unparseable output must fail open, got feedback: %q", v.feedback)
		}
		if stages := verifyStages(sess.Emitter); !hasStage(stages, "typecheck", "unavailable") {
			t.Errorf("unparseable output should be reported as unavailable, got %v", stages)
		}
	})

	// Valid JSON without an explicit "passed" verdict (e.g. {"ok":true}) parses
	// with passed defaulting to false — which used to fail the whole build on
	// an empty error message. A missing verdict is checker drift, not a code
	// failure: surface as unavailable and keep going.
	t.Run("missing passed verdict is surfaced as unavailable", func(t *testing.T) {
		o := New(nil, nil, nil)
		sess := newTestSession(t)
		opts := RunOptions{Execute: ladderExec(map[string]gateResponse{
			"run_typecheck": {out: `{"ok":true}`},
			"run_lint":      {out: verifyJSON(t, true, "", "no lint errors")},
		}, nil)}

		v := o.verify(context.Background(), sess, opts)
		if !v.ok {
			t.Fatalf("missing verdict must fail open, got feedback: %q", v.feedback)
		}
		if stages := verifyStages(sess.Emitter); !hasStage(stages, "typecheck", "unavailable") {
			t.Errorf("missing verdict should be reported as unavailable, got %v", stages)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ── adaptive prompting by model tier ──────────────────

// The discipline block is model-specific but session-invariant, so it sits in the
// cached prefix: after the base prompt and rules, before the per-session memory
// that varies. A nil-client orchestrator adds nothing.
func TestEnrichPromptAppendsWorkingDiscipline(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	if out := o.enrichPromptWithContext(sess, "BASE"); strings.Contains(out, "Working discipline") {
		t.Error("a nil-client orchestrator should add no working-discipline block")
	}

	// Simulate a weaker model having been detected at construction.
	o.discipline = "## Working discipline\n\nSENTINEL-DISCIPLINE"
	sess.LoadMemory([]store.ProjectMemory{{EntryType: "component", Content: "SENTINEL-MEMORY"}})

	out := o.enrichPromptWithContext(sess, "BASE PROMPT")
	if !strings.HasPrefix(out, "BASE PROMPT") {
		t.Error("the base prompt should still lead the enriched prompt")
	}
	discIdx := strings.Index(out, "SENTINEL-DISCIPLINE")
	memIdx := strings.Index(out, "SENTINEL-MEMORY")
	if discIdx < 0 {
		t.Fatal("the discipline block should be injected when set")
	}
	if memIdx >= 0 && discIdx > memIdx {
		t.Error("the discipline block belongs in the stable prefix, before per-session memory")
	}
}

// Fix 4: a verified outcome must name the gates that never ran. The sweep
// records unavailability; SkippedGates reflects the final sweep only.
func TestSkippedGatesSurfaceOnVerified(t *testing.T) {
	sess := newTestSession(t)
	if got := sess.SkippedGates(); len(got) != 0 {
		t.Fatalf("fresh session should have no skipped gates, got %v", got)
	}
	sess.BeginGateSweep()
	sess.NoteGateUnavailable("boot")
	sess.NoteGateUnavailable("vision")
	sess.NoteGateUnavailable("boot") // dedup
	if got := sess.SkippedGates(); len(got) != 2 || got[0] != "boot" || got[1] != "vision" {
		t.Fatalf("want [boot vision], got %v", got)
	}
	// A later sweep that runs clean erases early unavailability.
	sess.BeginGateSweep()
	if got := sess.SkippedGates(); len(got) != 0 {
		t.Fatalf("final clean sweep should clear skips, got %v", got)
	}
}

// Item 1 (durable backlog): task state written through during a session is
// what a fresh session re-seeds from — restarts and resume see real progress,
// and a plan edit supersedes dropped work instead of deleting history.
func TestPlanTaskPersistenceRoundTrip(t *testing.T) {
	st, err := store.New(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("pt@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "pt", "1.0.0", "pt-slug", "com.test.pt", "", "")
	planJSON := `{"screens":[{"name":"Watchlist"},{"name":"Activity"}],"features":["MWA auth"]}`
	if err := st.UpdateProjectPlan(p.ID, planJSON); err != nil {
		t.Fatal(err)
	}

	o := New(nil, st, nil)
	sess := o.CreateSession(1, p.ID, t.TempDir())
	t.Cleanup(func() { o.RemoveSession(1) })

	// Fresh seed: two screens + one feature, all pending.
	if tasks := sess.GetTasks(); len(tasks) != 3 {
		t.Fatalf("expected 3 seeded tasks, got %d", len(tasks))
	}
	sess.MarkPlanTask("plan:screen:watchlist", "Screen: Watchlist", "completed", "route written")

	rows, err := st.LoadPlanTasks(p.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("seed should persist the full backlog (3 rows): %v %v", rows, err)
	}
	var completedRow *store.PlanTask
	for i := range rows {
		if rows[i].TaskID == "plan:screen:watchlist" {
			completedRow = &rows[i]
		}
	}
	if completedRow == nil || completedRow.Status != "completed" || completedRow.CompletedByPrompt != 1 {
		t.Fatalf("write-through should persist the completion: %+v", completedRow)
	}

	// A new session (a restart, effectively) re-seeds from the table.
	sess2 := o.CreateSession(2, p.ID, t.TempDir())
	t.Cleanup(func() { o.RemoveSession(2) })
	for _, task := range sess2.GetTasks() {
		if task.ID == "plan:screen:watchlist" && task.Status != "completed" {
			t.Fatalf("completed task must survive re-seed, got %+v", task)
		}
		if task.ID == "plan:screen:activity" && task.Status != "pending" {
			t.Fatalf("untouched task must stay pending, got %+v", task)
		}
	}

	// A plan edit dropping Activity supersedes its row, keeps the rest.
	planJSON2 := `{"screens":[{"name":"Watchlist"}],"features":["MWA auth"]}`
	if err := st.UpdateProjectPlan(p.ID, planJSON2); err != nil {
		t.Fatal(err)
	}
	sess3 := o.CreateSession(3, p.ID, t.TempDir())
	t.Cleanup(func() { o.RemoveSession(3) })
	final, _ := st.LoadPlanTasks(p.ID)
	var superseded int
	for _, r := range final {
		if r.TaskID == "plan:screen:activity" && r.Status == "superseded" {
			superseded++
		}
	}
	if superseded != 1 {
		t.Fatalf("dropped screen should be superseded once, table: %+v", final)
	}
	if len(sess3.GetTasks()) != 2 {
		t.Fatalf("superseded work must not re-seed, got %+v", sess3.GetTasks())
	}
}
