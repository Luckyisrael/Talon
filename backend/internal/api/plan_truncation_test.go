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

// FM-PLANa — written first (TEST_RULES §3). The failure, measured in
// Run-7: the plan stream ended mid-array with 17,081 chars stored, and
// the turn still reported `done {status: success}` with the prompt row
// marked `completed`. Unmarshal failed, the handler's prose fallback
// (written for a model that answers in words) swallowed the broken
// envelope, planAsksQuestion said no, and the success arm ran. Project
// 24 was left with plan_json = null while drive.py, the scorecard and
// the UI all read a green turn — the build then ran planless, so
// `plan screens: none found`, S3 fell back to counting route files,
// S5 was unmeasurable and S7 scored 0/0.
//
// The law: a stream the gateway cut at max_tokens ends as an error;
// plan output that opens as JSON and does not parse is a FAILED plan
// turn (terminal `failed` + error frame), never `completed`; prose
// that does not open with `{` keeps the message path unchanged.

// truncatedPlanJSON is Run-7's shape: an envelope that opens cleanly
// and stops mid-array. Deliberately free of "?" and the question
// markers planAsksQuestion keys on, so the red state is unambiguously
// the success arm and not the waiting one.
const truncatedPlanJSON = `{"message":"**What I'm planning:** Momentum as six screens, four tabs, everything in AsyncStorage with no network.","plan":{"screens":[{"name":"Home","route":"app/(tabs)/index","description":"Today's habits"},{"name":"Habits","route":"app/(tabs)/habits",`

// anthropicPlanStream serves one anthropic SSE stream carrying `text`
// as a single text block, then a message_delta with stopReason.
func anthropicPlanStream(text, stopReason string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emit := func(event string, data any) {
			b, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
			flusher.Flush()
		}
		emit("message_start", map[string]any{
			"type":    "message_start",
			"message": map[string]any{"id": "m1", "role": "assistant", "content": []any{}},
		})
		emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": text},
		})
		emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		emit("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stopReason},
		})
		emit("message_stop", map[string]any{"type": "message_stop"})
	}))
}

// openaiPlanTextStream serves one openai SSE plan turn whose content is
// the given text, finishing with end_turn.
func openaiPlanTextStream(t *testing.T, text string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emit := func(data any) {
			b, _ := json.Marshal(data)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		emit(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": text}}}})
		emit(map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "end_turn"}}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// runPlanStream drives one plan turn against `model` and returns the
// wire output plus the prompt row it left behind.
func runPlanStream(t *testing.T, model *httptest.Server, protocol string) (string, *store.Prompt) {
	t.Helper()

	t.Setenv("AI_PROTOCOL", protocol)
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "0")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "plan-trunc.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("plantrunc@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Momentum", "v1", "momentum", "com.local.momentum", "com.local.momentum", "momentum")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"a habit tracker","mode":"plan"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}

	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil || prompt == nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	return rec.Body.String(), prompt
}

// TestPlanStreamTruncatedByMaxTokensIsAFailure pins the wire and the
// row: the gateway's own word that it cut the response must surface as
// an error event, and the turn must end failed — not success with a
// silently missing plan (Run-7, project 24).
func TestPlanStreamTruncatedByMaxTokensIsAFailure(t *testing.T) {
	model := anthropicPlanStream(truncatedPlanJSON, "max_tokens")
	defer model.Close()

	out, prompt := runPlanStream(t, model, "anthropic")

	if strings.Contains(out, `"status":"success"`) {
		t.Errorf("a plan stream cut at max_tokens reported success — Run-7's exact bug:\n%s", out)
	}
	if !strings.Contains(out, "event: error") {
		t.Errorf("the truncation never reached the client as an error frame:\n%s", out)
	}
	if !strings.Contains(out, `"status":"error"`) {
		t.Errorf("the turn did not end with done error:\n%s", out)
	}
	if prompt.Status != "failed" {
		t.Errorf("prompt status = %q, want failed (a lost plan is not a completed turn)", prompt.Status)
	}
}

// TestBrokenPlanEnvelopeIsNotReportedAsSuccess pins the handler-side
// half independently of any stop_reason: output that opens with `{` is
// an envelope attempt, and an envelope attempt that does not parse is a
// lost plan — a clean end_turn must not launder it into success.
func TestBrokenPlanEnvelopeIsNotReportedAsSuccess(t *testing.T) {
	broken := `{"message":"Six screens with four tabs and AsyncStorage","plan":{"screens":[{"name":"Home","route":"app/(tabs)/index"`
	model := openaiPlanTextStream(t, broken)
	defer model.Close()

	out, prompt := runPlanStream(t, model, "openai")

	if strings.Contains(out, `"status":"success"`) {
		t.Errorf("a broken plan envelope was reported as success:\n%s", out)
	}
	if !strings.Contains(out, `"status":"error"`) {
		t.Errorf("the turn did not end with done error:\n%s", out)
	}
	if prompt.Status != "failed" {
		t.Errorf("prompt status = %q, want failed", prompt.Status)
	}
}

// TestPlanStreamProseAnswerStillSucceeds is the keep-test: the new rule
// must eat broken envelopes, not the message path a plan turn legitimately
// takes when it settles something in words instead of JSON.
func TestPlanStreamProseAnswerStillSucceeds(t *testing.T) {
	prose := "Three screens for a first release, all local, nothing else to settle."
	model := openaiPlanTextStream(t, prose)
	defer model.Close()

	out, prompt := runPlanStream(t, model, "openai")

	if strings.Contains(out, `"status":"error"`) {
		t.Errorf("a prose answer was failed by the envelope rule:\n%s", out)
	}
	if !strings.Contains(out, `"status":"success"`) {
		t.Errorf("a prose answer no longer completes the turn:\n%s", out)
	}
	if prompt.Status != "completed" {
		t.Errorf("prompt status = %q, want completed", prompt.Status)
	}
}
