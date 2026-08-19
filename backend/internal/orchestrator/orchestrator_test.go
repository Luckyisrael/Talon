package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/store"
)

func newTestSession(t *testing.T) *SessionState {
	t.Helper()
	return NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
}

func loadKnowledge(t *testing.T) *agent.Registry {
	t.Helper()
	r, err := agent.Load("../../template/package.json")
	if err != nil {
		t.Fatalf("agent.Load: %v", err)
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
	if first == "" || !strings.Contains(first, "Reanimated") {
		t.Fatalf("expected the reanimated skill injected the first time, got: %q", first)
	}
	if second := o.autoAttachSkills(sess, call); second != "" {
		t.Errorf("expected dedup on the second trigger, got: %q", second)
	}
}

// A skill the model read for itself must not then be auto-injected on a later
// trigger — MarkSkillSeen is the shared gate for both paths.
func TestAutoAttachSkipsExplicitlyReadSkill(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	sess.MarkSkillSeen("wallet-mwa")

	call := toolCall("install_package", `{"name":"@solana-mobile/mobile-wallet-adapter-protocol-web3js"}`)
	if got := o.autoAttachSkills(sess, call); got != "" {
		t.Errorf("expected no injection for an already-seen skill, got: %q", got)
	}
}

func TestAutoAttachNoTrigger(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	call := toolCall("write_file", `{"path":"lib/util.ts","content":"export const x = 1;"}`)
	if got := o.autoAttachSkills(sess, call); got != "" {
		t.Errorf("expected no injection without a trigger, got: %q", got)
	}
}

func TestAutoAttachNilKnowledge(t *testing.T) {
	o := New(nil, nil, nil)
	sess := newTestSession(t)
	call := toolCall("write_file", `{"path":"app/index.tsx","content":"import 'react-native-reanimated';"}`)
	if got := o.autoAttachSkills(sess, call); got != "" {
		t.Errorf("nil knowledge must inject nothing, got: %q", got)
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
		if want := []string{"run_typecheck", "run_lint"}; !equalStrings(calls, want) {
			t.Errorf("gates ran %v, want %v (typecheck before lint)", calls, want)
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

	// A checker that cannot run is a platform problem, not the agent's code: skip
	// the gate and keep going rather than trapping clean code in a repair loop.
	t.Run("a broken checker is skipped, not failed", func(t *testing.T) {
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
		if !hasStage(stages, "typecheck", "skip") {
			t.Errorf("the broken gate should be reported as skipped, got %v", stages)
		}
		if !equalStrings(calls, []string{"run_typecheck", "run_lint"}) {
			t.Errorf("a skipped gate must not stop the ladder, got %v", calls)
		}
	})

	// Output that does not parse means the tool's contract drifted; fail open so a
	// format change never blocks every build.
	t.Run("unparseable output is skipped", func(t *testing.T) {
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
		if stages := verifyStages(sess.Emitter); !hasStage(stages, "typecheck", "skip") {
			t.Errorf("unparseable output should be reported as skipped, got %v", stages)
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
