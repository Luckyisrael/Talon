package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// L4 plan feasibility gate — fails-first tests (ENGINEERING_STANDARDS §6).
// The failure modes themselves are written in planfeasible.go's header and
// in §6; these tests pin the contract: an infeasible package fails the
// PLAN turn at both doors, unknown probes never do, preinstalled packages
// never reach the probe, and the probe never mutates (FM-L4d) or guesses
// (FM-L4c/e).

// planEnvelopeWithPackages builds the reply contract with a package list.
func planEnvelopeWithPackages(t *testing.T, packages ...string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"message": "Here is the plan.",
		"plan": map[string]any{
			"screens":    []map[string]string{{"name": "Home", "description": "Landing"}},
			"features":   []string{"dark mode"},
			"navigation": "stack",
			"packages":   packages,
		},
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return string(raw)
}

// definitiveProbe reports every package infeasible, definitively — the
// fake behind "the workspace says no".
func definitiveProbe(infeasible ...string) planPackageProbe {
	return func(context.Context, string, []string) ([]string, string, bool) {
		return infeasible, "npm ERR! code E404", true
	}
}

func unknownProbe() planPackageProbe {
	return func(context.Context, string, []string) ([]string, string, bool) {
		return nil, "", false
	}
}

// TestPlanSendDoorRejectsInfeasiblePackage — FM-L4a at the Send door: a
// settled plan carrying a package the probe proves infeasible fails the
// turn (422, package named, prompt failed) and nothing is saved.
func TestPlanSendDoorRejectsInfeasiblePackage(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		return 200, textReply(planEnvelopeWithPackages(t, "bad-pkg"))
	})
	f.h.packageProbe = definitiveProbe("bad-pkg")

	rec := httptest.NewRecorder()
	f.h.Create(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, f.pid)))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("infeasible package must fail the plan turn at the Send door — "+
			"status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "bad-pkg") {
		t.Errorf("the rejection must name the offending package: %s", rec.Body.String())
	}
	prompt, err := f.st.GetLatestPrompt(f.pid)
	if err != nil || prompt == nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if prompt.Status != "failed" {
		t.Errorf("rejected plan turn status = %q, want failed", prompt.Status)
	}
	proj, err := f.st.GetProject(f.pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj.PlanJSON != "" {
		t.Errorf("a rejected plan must not be saved — plan_json = %q", proj.PlanJSON)
	}
}

// TestPlanStreamDoorRejectsInfeasiblePackage — the same gate on the
// streaming door (the E2E path): the stream carries an error naming the
// package, closes with done/error, and saves nothing.
func TestPlanStreamDoorRejectsInfeasiblePackage(t *testing.T) {
	planJSON := `{"message":"Here is the plan.","plan":{"screens":[{"name":"Home","description":"Landing"}],"features":["dark mode"],"packages":["bad-pkg"]}}`
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		text, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": planJSON}}},
		})
		finish, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "end_turn"}},
		})
		_, _ = fmt.Fprint(w, "data: "+string(text)+"\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: "+string(finish)+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "0")

	client, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "l4-stream.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("l4-stream@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "L4", "v1", "l4", "com.local.l4", "com.local.l4", "l4")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	orch := orchestrator.New(client, st, nil)
	h := NewPromptHandler(st, client, t.TempDir(), "", orch, nil, nil, nil)
	h.packageProbe = definitiveProbe("bad-pkg")

	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, "event: error") || !strings.Contains(out, "bad-pkg") {
		t.Errorf("the stream must carry an error naming the package:\n%s", out)
	}
	if !strings.Contains(out, `"status":"error"`) {
		t.Errorf("the stream must close with done/error:\n%s", out)
	}
	if strings.Contains(out, "event: plan") {
		t.Errorf("a rejected plan must not be emitted as a plan frame:\n%s", out)
	}
	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil || prompt == nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if prompt.Status != "failed" {
		t.Errorf("rejected plan turn status = %q, want failed", prompt.Status)
	}
	proj, err := st.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj.PlanJSON != "" {
		t.Errorf("a rejected plan must not be saved — plan_json = %q", proj.PlanJSON)
	}
}

// TestPlanGatePassesUnknownProbeResult — FM-L4c: an unknown answer (the
// probe flaked) passes the turn; failing healthy plans on flakiness would
// trade one bug for another.
func TestPlanGatePassesUnknownProbeResult(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		return 200, textReply(planEnvelopeWithPackages(t, "some-pkg"))
	})
	f.h.packageProbe = unknownProbe()

	rec := httptest.NewRecorder()
	f.h.Create(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, f.pid)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown probe result must pass the turn — status %d, body %s",
			rec.Code, rec.Body.String())
	}
}

// TestPlanGateSkipsPreinstalledPackages — the prompt's promise ("already
// installed — do not list these") must hold at the gate too: a
// preinstalled package never reaches the probe, and a plan of nothing but
// preinstalled packages passes without probing at all.
func TestPlanGateSkipsPreinstalledPackages(t *testing.T) {
	pkgJSON := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(pkgJSON,
		[]byte(`{"dependencies":{"expo-notifications":"*"}}`), 0o644); err != nil {
		t.Fatalf("template package.json: %v", err)
	}
	reg, err := knowledge.Load(pkgJSON)
	if err != nil {
		t.Fatalf("knowledge.Load: %v", err)
	}
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		return 200, textReply(planEnvelopeWithPackages(t, "expo-notifications", "bad-pkg"))
	})
	f.h.knowledge = reg
	var probed [][]string
	f.h.packageProbe = func(_ context.Context, _ string, pkgs []string) ([]string, string, bool) {
		probed = append(probed, pkgs)
		return nil, "", false
	}

	if _, reject := f.h.checkPlanFeasibility(context.Background(), f.pid, &ai.AppPlan{
		Packages: []string{"expo-notifications"},
	}); reject {
		t.Fatal("a plan of only preinstalled packages must pass without probing")
	}
	if len(probed) != 0 {
		t.Errorf("preinstalled-only plan still probed: %v", probed)
	}

	if _, reject := f.h.checkPlanFeasibility(context.Background(), f.pid, &ai.AppPlan{
		Packages: []string{"expo-notifications", "bad-pkg"},
	}); reject {
		t.Fatal("gate with an unknown probe must pass")
	}
	if len(probed) != 1 || len(probed[0]) != 1 || probed[0][0] != "bad-pkg" {
		t.Errorf("probe must receive only the non-preinstalled package, got %v", probed)
	}
}

// TestClassifyNPMFailure — the classification table behind FM-L4c: only
// the registry/workspace saying no is proof; network-level and
// unrecognized output never are.
func TestClassifyNPMFailure(t *testing.T) {
	cases := []struct {
		name, out string
		want      npmFailure
	}{
		{"404 code", "npm ERR! code E404\nnpm ERR! 404 Not Found - GET https://registry.npmjs.org/bad-pkg", npmFailureDefinitive},
		{"404 line", "npm ERR! 404 Not Found - GET https://registry.npmjs.org/bad-pkg", npmFailureDefinitive},
		{"eresolve", "npm ERR! code ERESOLVE\nnpm ERR! ERESOLVE Unable to resolve dependency tree", npmFailureDefinitive},
		{"dns", "npm ERR! code ENOTFOUND\nnpm ERR! network getaddrinfo ENOTFOUND registry.npmjs.org", npmFailureTransient},
		{"dns again", "npm ERR! code EAI_AGAIN", npmFailureTransient},
		{"timeout", "npm ERR! code ETIMEDOUT", npmFailureTransient},
		{"reset", "npm ERR! code ECONNRESET", npmFailureTransient},
		{"503", "npm ERR! 503 Service Unavailable - GET https://registry.npmjs.org/x", npmFailureTransient},
		{"429", "npm ERR! 429 Too Many Requests - GET https://registry.npmjs.org/x", npmFailureTransient},
		{"empty", "", npmFailureUnknown},
		{"unrecognized", "npm ERR! something brand new happened", npmFailureUnknown},
	}
	for _, c := range cases {
		if got := classifyNPMFailure(c.out); got != c.want {
			t.Errorf("%s: classify(%q) = %v, want %v", c.name, c.out, got, c.want)
		}
	}
}

// writeFakeNPM installs a fake `npm` at the front of PATH whose behavior
// is the script body — hermetic resolution, no network, no workspace.
func writeFakeNPM(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("fake npm: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestNPMDryRunProbeIsolatesDefinitiveFailures — the probe's flow: the
// batch fails definitively, the baseline (no plan packages) resolves, and
// isolation names exactly the offending package.
func TestNPMDryRunProbeIsolatesDefinitiveFailures(t *testing.T) {
	writeFakeNPM(t, `
case "$*" in
  *bad-pkg*) echo "npm ERR! code E404" >&2; echo "npm ERR! 404 Not Found - GET https://registry.npmjs.org/bad-pkg" >&2; exit 1;;
esac
exit 0`)
	infeasible, reason, definitive := npmDryRunProbe(context.Background(), t.TempDir(),
		[]string{"good-pkg", "bad-pkg"})
	if !definitive {
		t.Fatal("a definitive 404 must be a definitive answer")
	}
	if len(infeasible) != 1 || infeasible[0] != "bad-pkg" {
		t.Errorf("isolation must name only the offending package, got %v", infeasible)
	}
	if !strings.Contains(reason, "404") {
		t.Errorf("reason must carry npm's verdict, got %q", reason)
	}
}

// TestNPMDryRunProbePassesOnTransientNetwork — FM-L4c: a network-level
// failure is unknown, never infeasible.
func TestNPMDryRunProbePassesOnTransientNetwork(t *testing.T) {
	writeFakeNPM(t, `echo "npm ERR! code EAI_AGAIN" >&2
echo "npm ERR! network request failed" >&2
exit 1`)
	infeasible, _, definitive := npmDryRunProbe(context.Background(), t.TempDir(), []string{"any-pkg"})
	if definitive || len(infeasible) != 0 {
		t.Errorf("transient network failure must report unknown — got infeasible=%v definitive=%v",
			infeasible, definitive)
	}
}

// TestNPMDryRunProbePassesWhenBaselineBroken — FM-L4e: the dry-run
// resolves the whole workspace; if the baseline itself cannot resolve,
// the probe cannot judge plans and must report unknown.
func TestNPMDryRunProbePassesWhenBaselineBroken(t *testing.T) {
	writeFakeNPM(t, `echo "npm ERR! code ERESOLVE" >&2
echo "npm ERR! ERESOLVE Unable to resolve dependency tree" >&2
exit 1`)
	infeasible, _, definitive := npmDryRunProbe(context.Background(), t.TempDir(), []string{"any-pkg"})
	if definitive || len(infeasible) != 0 {
		t.Errorf("a broken baseline must not fail plans — got infeasible=%v definitive=%v",
			infeasible, definitive)
	}
}

// TestInfeasibleMessageIsActionable — the rejection is what the user (and
// the next plan attempt) reads: package named, reason given, next step
// spelled out.
func TestInfeasibleMessageIsActionable(t *testing.T) {
	msg := infeasibleMessage([]string{"bad-pkg"}, "npm ERR! 404 Not Found")
	for _, want := range []string{"bad-pkg", "404 Not Found", "plan again"} {
		if !strings.Contains(msg, want) {
			t.Errorf("rejection message missing %q: %s", want, msg)
		}
	}
}
