package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/aitest"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// Regression tests for the conversation/history UX bugs found in manual
// testing:
//
//   - Bug 1 (dead air): a plan turn used to write zero bytes until the parsed
//     plan landed — minutes with no phase frame, no prompt id, and no sign the
//     turn was alive. The contract now: the FIRST frame is phase, carrying
//     PLAN and the turn's id; reasoning deltas stream as thinking events; a
//     silent stretch is covered by a heartbeat re-asserting the phase. The
//     tail (plan frame, done success) is unchanged.
//
//   - Bug 4, backend half (double injection): the plan checklist seed — every
//     screen and feature as a task_update — belongs to a FRESH build turn. An
//     answer continuation replays into a client that already holds the
//     checklist; re-emitting it injected the whole screens list a second time.
func TestPlanStreamIsAliveFromTheFirstByte(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The silence a real planner makes: long thinking before its first
		// byte. Nothing else is on the wire during it — the heartbeat must be.
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emitFrame := func(frame string) {
			_, _ = fmt.Fprint(w, frame)
			flusher.Flush()
		}
		reasoning, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"reasoning": "choosing the screens"}}},
		})
		planJSON := `{"message":"Two screens.","plan":{"screens":[{"name":"Home","description":"Balance"},{"name":"Activity","description":"History"}],"features":["dark mode"]}}`
		text, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": planJSON}}},
		})
		finish, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": "end_turn"}},
		})
		emitFrame("data: " + string(reasoning) + "\n\n")
		emitFrame("data: " + string(text) + "\n\n")
		emitFrame("data: " + string(finish) + "\n\n")
		emitFrame("data: [DONE]\n\n")
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "0")

	// Shorten the heartbeat so the 300ms model silence contains several ticks.
	oldInterval := planPhaseInterval
	planPhaseInterval = 40 * time.Millisecond
	t.Cleanup(func() { planPhaseInterval = oldInterval })

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "plan-live.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("planlive@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Wallet", "v1", "wallet", "com.local.wallet", "com.local.wallet", "wallet")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"a wallet app","mode":"plan"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	// 1. The first frame on the wire is the phase frame — before any model byte.
	phaseIdx := strings.Index(out, "event: phase")
	if phaseIdx < 0 {
		t.Fatalf("no phase frame opened the plan stream:\n%s", out)
	}
	if firstIdx := strings.Index(out, "event: "); firstIdx != phaseIdx {
		t.Errorf("the first frame is not the phase frame (first frame at %d, phase at %d):\n%s",
			firstIdx, phaseIdx, out)
	}
	if !strings.Contains(out, `"phase":"PLAN"`) {
		t.Errorf("the phase frame does not say PLAN:\n%s", out)
	}
	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if want := fmt.Sprintf(`"prompt_id":%d,`, prompt.ID); !strings.Contains(out, want) {
		t.Errorf("the plan stream never named its turn — no %q frame:\n%s", want, out)
	}

	// 2. The model's reasoning reaches the client as a thinking event.
	thinkingIdx := strings.Index(out, "event: thinking")
	if thinkingIdx < 0 {
		t.Errorf("thinking events were not forwarded from the plan stream:\n%s", out)
	}
	if !strings.Contains(out, `"text":"choosing the screens"`) {
		t.Errorf("the reasoning delta did not reach the client:\n%s", out)
	}

	// 3. The silent stretch was covered by heartbeat re-asserts of the phase.
	if n := strings.Count(out, "event: phase"); n < 2 {
		t.Errorf("the heartbeat never re-asserted the phase during the model's silence (%d phase frames):\n%s", n, out)
	}

	// 4. The tail is unchanged: parsed plan, then done success — in order.
	planIdx := strings.Index(out, "event: plan")
	doneIdx := strings.Index(out, "event: done")
	if planIdx < 0 || !strings.Contains(out, `"name":"Home"`) {
		t.Errorf("the plan frame did not carry the parsed plan:\n%s", out)
	}
	if doneIdx < 0 || !strings.Contains(out, `"status":"success"`) {
		t.Errorf("the turn did not finish successfully:\n%s", out)
	}
	if planIdx >= 0 && doneIdx >= 0 && !(phaseIdx < thinkingIdx && thinkingIdx < planIdx && planIdx < doneIdx) {
		t.Errorf("frames out of order: phase=%d thinking=%d plan=%d done=%d\n%s",
			phaseIdx, thinkingIdx, planIdx, doneIdx, out)
	}
}

// TestPlanChecklistSeedIsFreshTurnsOnly pins the other half of the
// double-injection fix: the checklist seed rides a fresh build turn, and an
// answer continuation must not blast it again — the client still holds it.
func TestPlanChecklistSeedIsFreshTurnsOnly(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First turn: the agent asks a clarifying question and pauses.
			aitest.Serve(w, r, map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "One thing before I build."},
					{"type": "tool_use", "id": "ask_1", "name": "ask_user",
						"input": map[string]any{
							"question": "Which auth flow?",
							"choices":  []string{"MWA", "email link"},
						}},
				},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
			})
			return
		}
		// After the answer: the agent has what it needs and finishes.
		aitest.Serve(w, r, map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "Wired up MWA auth."}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 12, "output_tokens": 8},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "0")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "plan-seed.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("seedgate@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Wallet", "v1", "wallet", "com.local.wallet", "com.local.wallet", "wallet")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	// A settled plan on the project: the seed reads plan_json.
	planJSON := `{"screens":[{"name":"Home","description":"Balance"},{"name":"Activity","description":"History"}],"features":["dark mode"]}`
	if err := st.UpdateProjectPlan(project.ID, planJSON); err != nil {
		t.Fatalf("UpdateProjectPlan: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, nil, nil)

	// ── Fresh build turn: the checklist seeds ────────────────────────────
	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"Build the wallet","mode":"build"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	fresh := rec.Body.String()
	if !strings.Contains(fresh, "event: task_update") || !strings.Contains(fresh, `"id":"plan:screen:`) {
		t.Errorf("the fresh build turn did not seed the plan checklist:\n%s", fresh)
	}
	if !strings.Contains(fresh, `"id":"plan:feature:0"`) {
		t.Errorf("the seed is missing the feature tasks:\n%s", fresh)
	}

	// The turn paused on ask_user; answer it below. The continuation must not
	// re-seed: the client still holds the checklist from the fresh turn.
	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if prompt.Status != "waiting" {
		t.Fatalf("after the pause, prompt status = %q, want waiting", prompt.Status)
	}

	ansReq := authedRequest(user.ID, `{"answer":"Use MWA transact()"}`)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", prompt.ID))
	ansReq = ansReq.WithContext(context.WithValue(ansReq.Context(), chi.RouteCtxKey, rctx))
	ansRec := httptest.NewRecorder()
	h.Answer(ansRec, ansReq)
	if ansRec.Code != http.StatusOK {
		t.Fatalf("answer: status %d, body %s", ansRec.Code, ansRec.Body.String())
	}
	answer := ansRec.Body.String()
	if !strings.Contains(answer, `"status":"success"`) {
		t.Errorf("the answer continuation did not finish:\n%s", answer)
	}
	if strings.Contains(answer, `"id":"plan:feature:0"`) {
		t.Errorf("the answer continuation re-seeded the plan checklist — the double injection is back:\n%s", answer)
	}
}
