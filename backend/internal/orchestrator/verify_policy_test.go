package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// writeVerifyConfig writes a .talon/config.json into a workspace so the
// policy matrix sees a project's own choices.
func writeVerifyConfig(t *testing.T, workspace, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".talon", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ── policy resolution ──────────────────────────────────

// Project config beats the deployment env default, which beats strict; an
// unrecognized value means strict — a typo in a safety flag must not mean
// permissive, and a project's invalid value must not fall through to a
// looser env.
func TestVerifyPolicyResolution(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("TALONSTUDIO_VERIFY_POLICY", "")

	if got := resolveVerifyPolicy(ws); got != VerifyPolicyStrict {
		t.Errorf("default policy = %q, want strict", got)
	}

	t.Setenv("TALONSTUDIO_VERIFY_POLICY", "besteffort")
	if got := resolveVerifyPolicy(ws); got != VerifyPolicyBestEffort {
		t.Errorf("env policy = %q, want besteffort", got)
	}

	writeVerifyConfig(t, ws, `{"verify_policy":"strict"}`)
	if got := resolveVerifyPolicy(ws); got != VerifyPolicyStrict {
		t.Errorf("project config must beat the env default, got %q", got)
	}

	writeVerifyConfig(t, ws, `{"verify_policy":"relaxed"}`)
	if got := resolveVerifyPolicy(ws); got != VerifyPolicyStrict {
		t.Errorf("invalid config policy = %q, want strict (never permissive)", got)
	}
}

// ── the gate class matrix ──────────────────────────────

func TestGateClassMatrix(t *testing.T) {
	cases := []struct {
		stage  string
		device bool
		want   GateClass
	}{
		{"typecheck", false, GateRequired},
		{"lint", false, GateRequired},
		{"bundle", false, GateRequired},
		{"gradle", false, GateRequired},
		{"lsp", false, GateBestEffort}, // W5's law: an absent server never blocks
		{"boot", true, GateRequired},
		{"vision", true, GateRequired},
		{"interact", true, GateRequired},
		{"boot", false, GateBestEffort}, // device-less runs cannot satisfy these
		{"vision", false, GateBestEffort},
		{"interact", false, GateBestEffort},
		{"frobnicate", false, GateRequired}, // anything unknown fails closed
	}
	for _, c := range cases {
		if got := defaultGateClass(c.stage, c.device); got != c.want {
			t.Errorf("defaultGateClass(%q, device=%v) = %q, want %q", c.stage, c.device, got, c.want)
		}
	}
}

// verify_gates can turn a stage off, promote the lsp rung to required, and
// an invalid entry degrades to the default — never to off.
func TestGatePolicyClassConfig(t *testing.T) {
	ws := t.TempDir()
	if got := gatePolicyClass(ws, "vision", false); got != GateBestEffort {
		t.Errorf("no config: vision = %q, want the default best-effort", got)
	}

	writeVerifyConfig(t, ws, `{"verify_gates":{"vision":"off","lsp":"required","lint":"nonsense"}}`)
	if got := gatePolicyClass(ws, "vision", false); got != GateOff {
		t.Errorf("vision = %q, want off from config", got)
	}
	if got := gatePolicyClass(ws, "lsp", false); got != GateRequired {
		t.Errorf("lsp = %q, want the promoted required class", got)
	}
	if got := gatePolicyClass(ws, "lint", false); got != GateRequired {
		t.Errorf("invalid entry must degrade to the default, got %q", got)
	}
	if got := gatePolicyClass(ws, "typecheck", false); got != GateRequired {
		t.Errorf("unconfigured stage keeps its default, got %q", got)
	}
}

// ── classification ─────────────────────────────────────

func TestClassifyVerification(t *testing.T) {
	ws := t.TempDir() // no config → the default matrix
	cases := []struct {
		name      string
		policy    string
		skips     []string
		device    bool
		want      string
		reasonHas string
	}{
		{"nothing skipped", VerifyPolicyStrict, nil, false, VerifyStatusVerified, ""},
		{"device-less skips stay best-effort", VerifyPolicyStrict, []string{"boot", "vision"}, false, VerifyStatusBestEffort, ""},
		{"required static skip refuses", VerifyPolicyStrict, []string{"typecheck"}, false, VerifyStatusRefused, "typecheck"},
		{"device gates required when attached", VerifyPolicyStrict, []string{"interact"}, true, VerifyStatusRefused, "interact"},
		{"besteffort policy downgrades", VerifyPolicyBestEffort, []string{"typecheck"}, false, VerifyStatusBestEffort, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, reason := classifyVerification(c.policy, ws, c.skips, c.device)
			if status != c.want {
				t.Errorf("status = %q, want %q (reason %q)", status, c.want, reason)
			}
			if c.reasonHas != "" && !strings.Contains(reason, c.reasonHas) {
				t.Errorf("reason %q should name %q", reason, c.reasonHas)
			}
			if c.reasonHas == "" && reason != "" {
				t.Errorf("non-refusals carry no reason, got %q", reason)
			}
		})
	}

	// An "off" stage can never refuse — it is not part of this ladder.
	offWS := t.TempDir()
	writeVerifyConfig(t, offWS, `{"verify_gates":{"vision":"off"}}`)
	if status, _ := classifyVerification(VerifyPolicyStrict, offWS, []string{"vision"}, true); status != VerifyStatusBestEffort {
		t.Errorf("an off stage must not refuse, got %q", status)
	}
}

// ── the shared release rule ────────────────────────────

func TestVerifyReleaseRefusalRule(t *testing.T) {
	if msg := VerifyReleaseRefusal(false, store.ProjectVerifyState{}); msg == "" || !strings.Contains(msg, "no verification record") {
		t.Errorf("absence of a record must refuse, got %q", msg)
	}
	if msg := VerifyReleaseRefusal(true, store.ProjectVerifyState{Status: VerifyStatusVerified}); msg != "" {
		t.Errorf("a verified record ships clean, got %q", msg)
	}

	msg := VerifyReleaseRefusal(true, store.ProjectVerifyState{
		Status:    VerifyStatusBestEffort,
		Skipped:   []string{"boot"},
		Remaining: []string{"Pair a device"},
	})
	if !strings.Contains(msg, "best-effort") || !strings.Contains(msg, "boot") || !strings.Contains(msg, "Pair a device") {
		t.Errorf("best-effort refusal must name the skip and the pending task, got %q", msg)
	}

	msg = VerifyReleaseRefusal(true, store.ProjectVerifyState{
		Status: VerifyStatusRefused,
		Reason: "required gate(s) never ran: lint",
	})
	if !strings.Contains(msg, "refused") || !strings.Contains(msg, "lint") {
		t.Errorf("refusal must name the reason, got %q", msg)
	}

	if r := verifyStateReason(6); !strings.Contains(r, "6 repair attempts") {
		t.Errorf("ladder refusal reason = %q, want the attempt count", r)
	}
}

// ── off stages leave the ladder ────────────────────────

// A stage the project turned off is never run and never recorded as
// skipped; the rest of the ladder is untouched. Without the config the
// same stage runs, proving the filter is what changed.
func TestVerifySkipsOffGates(t *testing.T) {
	ctx := context.Background()
	o := &Orchestrator{}

	ws := t.TempDir()
	writeVerifyConfig(t, ws, `{"verify_gates":{"vision":"off"}}`)
	sess := NewSession(1, 1, ws, events.NewSessionEmitter(1, 128))
	var calls []string
	verdict := o.verify(ctx, sess, RunOptions{Execute: ladderExec(nil, &calls)})
	if !verdict.ok {
		t.Fatalf("ladder with an off stage should still pass, got %+v", verdict)
	}
	for _, c := range calls {
		if c == "run_vision" {
			t.Fatal("an off gate must never run")
		}
	}
	if !containsTool(calls, "run_typecheck") {
		t.Errorf("the rest of the ladder must still run, calls: %v", calls)
	}
	if hasSkip(sess.SkippedGates(), "vision") {
		t.Errorf("an off gate is a project choice, not an infra gap: skips = %v", sess.SkippedGates())
	}

	// Control: no config, the gate runs as before.
	sess2 := NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
	var calls2 []string
	if v := o.verify(ctx, sess2, RunOptions{Execute: ladderExec(nil, &calls2)}); !v.ok {
		t.Fatalf("default ladder should pass, got %+v", v)
	}
	if !containsTool(calls2, "run_vision") {
		t.Errorf("without config vision must run, calls: %v", calls2)
	}
}

func containsTool(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// ── the full loop, against a scripted provider ─────────

// policyHarness is a fake provider plus a store-backed project: one turn
// can run end-to-end and leave a durable verify record behind.
func policyHarness(t *testing.T) (*Orchestrator, *SessionState, *store.Store, int64) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if first {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "call-1",
					"name":  "write_file",
					"input": map[string]any{"path": "app/x.tsx", "content": "x"},
				}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "done"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	t.Cleanup(model.Close)
	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_VERIFY_POLICY", "") // the deployment default: strict

	st, err := store.New(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("policy@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "policy", "1.0.0", "policy-slug", "com.test.policy", "", "")
	if err != nil {
		t.Fatal(err)
	}
	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, st, loadKnowledge(t))
	sess := NewSession(1, p.ID, t.TempDir(), events.NewSessionEmitter(1, 128))
	return o, sess, st, p.ID
}

func policyTurn(o *Orchestrator, sess *SessionState, exec ToolExecutor) (*BuildOutcome, error) {
	return o.RunAgentLoop(context.Background(), sess, RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("make a screen")},
		Execute:      exec,
		FilesTouched: func() []string { return []string{"app/x.tsx"} },
	})
}

// Device gates that cannot run (no device attached) skip as best-effort:
// the turn stays verified — annotated — and the record says best-effort.
func TestFullLoopBestEffortKeepsVerifiedAndRecords(t *testing.T) {
	o, sess, st, pid := policyHarness(t)
	exec := ladderExec(map[string]gateResponse{
		"run_boot":     {out: "", isError: true},
		"run_vision":   {out: "", isError: true},
		"run_interact": {out: "", isError: true},
	}, nil)

	outcome, err := policyTurn(o, sess, exec)
	if err != nil {
		t.Fatalf("best-effort sweep must not fail the turn: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}
	if !outcome.Verified {
		t.Error("device-less skips must not void verification under strict")
	}
	if !outcome.VerifiedBestEffort {
		t.Error("the outcome must be annotated best-effort")
	}
	if outcome.VerifyPolicy != VerifyPolicyStrict {
		t.Errorf("VerifyPolicy = %q, want strict", outcome.VerifyPolicy)
	}
	if !equalStrings(outcome.SkippedGates, []string{"boot", "vision", "interact"}) {
		t.Errorf("SkippedGates = %v", outcome.SkippedGates)
	}

	found, got, err := st.GetProjectVerifyState(pid)
	if err != nil || !found {
		t.Fatalf("the sweep must be recorded: found=%v err=%v", found, err)
	}
	if got.Status != VerifyStatusBestEffort || got.Policy != VerifyPolicyStrict {
		t.Errorf("recorded state = %+v, want best-effort under strict", got)
	}
	if !equalStrings(got.Skipped, []string{"boot", "vision", "interact"}) {
		t.Errorf("recorded skips = %v", got.Skipped)
	}
}

// A required gate that never ran: the ladder is fail-open, but the outcome
// is not "verified" and the record refuses — this is the planted
// required-gate failure the W6 eval gate demands.
func TestFullLoopRequiredSkipRefusesUnderStrict(t *testing.T) {
	o, sess, st, pid := policyHarness(t)
	exec := ladderExec(map[string]gateResponse{
		"run_typecheck": {out: "", isError: true}, // the tool broke: the stage never ran
	}, nil)

	outcome, err := policyTurn(o, sess, exec)
	if err != nil {
		t.Fatalf("a skipped gate must not fail the turn itself: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected an outcome")
	}
	if outcome.Verified {
		t.Error("a skipped required gate must not read as verified under strict")
	}
	if outcome.VerifiedBestEffort {
		t.Error("refused is not best-effort")
	}
	if outcome.VerifyPolicy != VerifyPolicyStrict {
		t.Errorf("VerifyPolicy = %q, want strict", outcome.VerifyPolicy)
	}
	if !strings.Contains(outcome.FinalText, "Verification policy:") {
		t.Error("the refusal must be visible in the turn's final text")
	}

	found, got, err := st.GetProjectVerifyState(pid)
	if err != nil || !found {
		t.Fatalf("the refusal must be recorded: found=%v err=%v", found, err)
	}
	if got.Status != VerifyStatusRefused {
		t.Errorf("recorded status = %q, want refused", got.Status)
	}
	if !strings.Contains(got.Reason, "typecheck") {
		t.Errorf("refusal reason should name the gate: %q", got.Reason)
	}
}

// The ladder exhausting its repair budget records a refusal, so a build
// that never compiled cannot be exported behind a stale pass.
func TestFullLoopLadderFailureRecordsRefused(t *testing.T) {
	o, sess, st, pid := policyHarness(t)
	exec := ladderExec(map[string]gateResponse{
		"run_typecheck": {out: verifyJSON(t, false, "app/x.tsx(1,1): error TS2307: cannot find module", "1 type error")},
	}, nil)

	_, err := policyTurn(o, sess, exec)
	if err == nil {
		t.Fatal("a persistently failing required gate must fail the turn")
	}
	if !strings.Contains(err.Error(), "after 6 repair attempts") {
		t.Errorf("err = %v, want the repair-budget message", err)
	}

	found, got, err2 := st.GetProjectVerifyState(pid)
	if err2 != nil || !found {
		t.Fatalf("the ladder failure must be recorded: found=%v err=%v", found, err2)
	}
	if got.Status != VerifyStatusRefused {
		t.Errorf("recorded status = %q, want refused", got.Status)
	}
	if !strings.Contains(got.Reason, "6 repair attempts") {
		t.Errorf("recorded reason = %q, want the ladder's own message", got.Reason)
	}
}
