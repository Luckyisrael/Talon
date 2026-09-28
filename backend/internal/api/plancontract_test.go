package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// L15 plan contract gate — FM-C1a, written before the implementation
// (TEST_RULES §3): both doors reject an envelope-valid plan whose declared
// feature files are malformed, with the feature named, and save nothing.
// Red today: the envelope parses, the check passes, and the plan persists.

// planEnvelopeWithMalformedFeature builds the reply contract carrying a
// shaped feature whose declared file escapes the workspace.
func planEnvelopeWithMalformedFeature(t *testing.T, file string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"message": "Here is the plan.",
		"plan": map[string]any{
			"screens": []map[string]string{{"name": "Home", "description": "Landing"}},
			"features": []map[string]any{{
				"text":  "escape the workspace",
				"files": []string{file},
			}},
			"navigation": "stack",
		},
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return string(raw)
}

// TestPlanSendDoorRejectsMalformedFeatureFiles — the Send door: 422, the
// feature named, prompt failed, nothing saved.
func TestPlanSendDoorRejectsMalformedFeatureFiles(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		return 200, textReply(planEnvelopeWithMalformedFeature(t, "../escape.ts"))
	})

	rec := httptest.NewRecorder()
	f.h.Create(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, f.pid)))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed declared file must fail the plan turn at the Send door — "+
			"status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "escape the workspace") {
		t.Errorf("the rejection must name the offending feature: %s", rec.Body.String())
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
		t.Errorf("a contract-invalid plan must not be saved — plan_json = %q", proj.PlanJSON)
	}
}

// TestPlanStreamDoorRejectsMalformedFeatureFiles — the stream door (the
// E2E path): error event naming the feature, done/error, no plan frame,
// nothing saved.
func TestPlanStreamDoorRejectsMalformedFeatureFiles(t *testing.T) {
	planJSON := planEnvelopeWithMalformedFeature(t, "node_modules/react/index.js")
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
	st, err := store.New(filepath.Join(t.TempDir(), "l15-contract.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("l15@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "L15", "v1", "l15", "com.local.l15", "com.local.l15", "l15")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	orch := orchestrator.New(client, st, nil)
	h := NewPromptHandler(st, client, t.TempDir(), "", orch, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, "event: error") || !strings.Contains(out, "escape the workspace") {
		t.Errorf("the stream must carry an error naming the feature:\n%s", out)
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
		t.Errorf("a contract-invalid plan must not be saved — plan_json = %q", proj.PlanJSON)
	}
}

// TestPlanGatePassesShapedContractPlan — the gate must not over-fire: a
// shaped plan with exact workspace-relative files passes and saves (guard
// against the rejection turning into the envelope-only crime in reverse).
func TestPlanGatePassesShapedContractPlan(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(map[string]any{
			"message": "Here is the plan.",
			"plan": map[string]any{
				"screens": []map[string]string{{"name": "Home", "description": "Landing"}},
				"features": []map[string]any{{
					"text":       "dark mode",
					"files":      []string{"app/settings.tsx", "src/theme.ts"},
					"acceptance": "settings toggle flips the theme",
				}},
				"navigation": "stack",
			},
		})
		return 200, textReply(string(raw))
	})

	rec := httptest.NewRecorder()
	f.h.Create(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan it","mode":"plan"}`, f.pid)))

	if rec.Code != http.StatusOK {
		t.Fatalf("a contract-valid plan must pass — status %d, body %s", rec.Code, rec.Body.String())
	}
	proj, err := f.st.GetProject(f.pid)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj.PlanJSON == "" {
		t.Fatal("a passing plan must be saved")
	}
	var saved ai.AppPlan
	if err := json.Unmarshal([]byte(proj.PlanJSON), &saved); err != nil {
		t.Fatalf("saved plan must reload: %v", err)
	}
	if len(saved.Features) != 1 || len(saved.Features[0].Files) != 2 {
		t.Fatalf("saved plan must keep the shaped contract, got %+v", saved.Features)
	}
}
