package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/aitest"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// The ask_user pause/resume flow, end to end on the backend. This is the
// contract between the two halves of the feature: a build turn that calls
// ask_user must pause (stream a question event, record 'waiting', not finish),
// and POST /answer must resume that same turn to completion by feeding the reply
// back as the tool_result for the pending call — all charged as a single prompt.
//
// It drives real handlers against a stand-in model that asks on its first turn
// and finishes on its second. The load-bearing assertion is on the SECOND model
// request: it must carry the answer as a tool_result for the paused ask_user id.
// That is exactly what AnsweredHistory preserves and what TrimToValidHistory
// would have thrown away — if someone reintroduces the trim on the answer path,
// the reply never reaches the model and this test fails.
func TestAskUserPausesThenAnswerResumes(t *testing.T) {
	var mu sync.Mutex
	var calls int
	var bodies []map[string]any

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		mu.Lock()
		calls++
		n := calls
		bodies = append(bodies, body)
		mu.Unlock()

		if n == 1 {
			// First turn: the agent asks a clarifying question and pauses. The loop
			// intercepts ask_user before running any tool, so stop_reason tool_use is
			// enough to trigger the pause.
			aitest.ServeRaw(w, raw, map[string]any{
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
		aitest.ServeRaw(w, raw, map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "Wired up MWA auth."}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 12, "output_tokens": 8},
		})
	}))
	defer model.Close()

	// Point a real ai.Client at the stand-in — ai.New reads these four.
	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}

	st, err := store.New(filepath.Join(t.TempDir(), "answer.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("asker@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Wallet", "v1", "wallet", "com.local.wallet", "com.local.wallet", "wallet")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, nil, nil)

	// ── The build turn pauses on ask_user ─────────────────
	streamBody := fmt.Sprintf(`{"project_id":%d,"prompt":"Build me a wallet screen","mode":"build"}`, project.ID)
	rec := httptest.NewRecorder()
	h.Stream(rec, authedRequest(user.ID, streamBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	streamOut := rec.Body.String()
	if !strings.Contains(streamOut, "event: question") {
		t.Fatalf("no question event reached the client:\n%s", streamOut)
	}
	if !strings.Contains(streamOut, "Which auth flow?") {
		t.Errorf("the question text did not reach the client:\n%s", streamOut)
	}
	// The terminal done frame must say 'waiting', not 'success' — the turn is
	// mid-flight, and the client keeps it open on the strength of this.
	if !strings.Contains(streamOut, `"status":"waiting"`) {
		t.Errorf("the done frame did not report waiting:\n%s", streamOut)
	}

	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if prompt.Status != "waiting" {
		t.Fatalf("after the pause, prompt status = %q, want waiting", prompt.Status)
	}

	// The question turn is charged one prompt, up front.
	usage, err := st.GetUsage(user.ID)
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	if usage.PromptsUsed != 1 {
		t.Fatalf("after the question, PromptsUsed = %d, want 1", usage.PromptsUsed)
	}

	// ── The answer resumes the same turn to completion ────
	answerBody := `{"answer":"Use MWA transact()"}`
	ansReq := authedRequest(user.ID, answerBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", prompt.ID))
	ansReq = ansReq.WithContext(context.WithValue(ansReq.Context(), chi.RouteCtxKey, rctx))

	ansRec := httptest.NewRecorder()
	h.Answer(ansRec, ansReq)

	if ansRec.Code != http.StatusOK {
		t.Fatalf("answer: status %d, body %s", ansRec.Code, ansRec.Body.String())
	}
	answerOut := ansRec.Body.String()
	if !strings.Contains(answerOut, `"status":"success"`) {
		t.Errorf("the answer turn did not finish successfully:\n%s", answerOut)
	}

	// The turn is now genuinely done — resume drove it to a clean finish.
	prompt, err = st.GetPrompt(prompt.ID)
	if err != nil {
		t.Fatalf("GetPrompt after answer: %v", err)
	}
	if prompt.Status != "completed" {
		t.Errorf("after the answer, prompt status = %q, want completed", prompt.Status)
	}

	// The question and its answer are one exchange — resuming must not charge a
	// second prompt against the monthly quota.
	usage, err = st.GetUsage(user.ID)
	if err != nil {
		t.Fatalf("GetUsage after answer: %v", err)
	}
	if usage.PromptsUsed != 1 {
		t.Errorf("after the answer, PromptsUsed = %d, want 1 (question+answer is one prompt)", usage.PromptsUsed)
	}

	// The load-bearing assertion: the second model request must carry the answer
	// as the tool_result for the pending ask_user. That is what makes the model
	// read it as the reply to its question rather than a fresh instruction.
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("the model was called %d times; the answer never resumed the loop", calls)
	}
	raw, _ := json.Marshal(bodies[1])
	second := string(raw)
	for _, want := range []string{"ask_1", "tool_result", "Use MWA transact()"} {
		if !strings.Contains(second, want) {
			t.Errorf("second model request missing %q — the answer did not reach the model as a tool_result:\n%s", want, second)
		}
	}
}
