package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// L8 build-stream liveness (ENGINEERING_STANDARDS §6 table, L8; S12 row).
//
// Failure modes, written first (TEST_RULES §3):
//
//  1. FM-L8a — the build drain loop had no heartbeat while plan mode had a
//     15 s one: Run 2's stream went up to 191 s between frames while the
//     model generated a large tool input (text and thinking are forwarded,
//     tool arguments were not). The heartbeat must cover that silence as
//     visibility ONLY — the scorecard excludes `heartbeat` frames from S12
//     and stall detection stays with stallbody, so a ping can never make a
//     stuck turn look alive.
//  2. FM-L8b — tool-argument fragments were consumed inside the client and
//     never reached the wire: minutes of a write_file input streaming with
//     zero frames on the SSE. The fragments are real progress and must be
//     forwarded (throttled — the frame is a heartbeat-grade notice, not the
//     payload itself).
func TestBuildStreamHeartbeatCoversModelSilence(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The silence a real build turn makes before its first byte: long
		// enough for several heartbeat ticks at the shortened interval.
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "Done."},
				"finish_reason": "end_turn",
			}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	// Shorten the heartbeat so the 300 ms model silence contains several ticks.
	oldInterval := buildHeartbeatInterval
	buildHeartbeatInterval = 40 * time.Millisecond
	t.Cleanup(func() { buildHeartbeatInterval = oldInterval })

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "build-live.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("buildlive@local", "hash")
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
		fmt.Sprintf(`{"project_id":%d,"prompt":"a streak app","mode":"build"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if n := strings.Count(out, "event: heartbeat"); n < 2 {
		t.Errorf("the build heartbeat never covered the model's %d-frame silence (%d heartbeat frames):\n%s",
			3, n, out)
	}
	prompt, err := st.GetLatestPrompt(project.ID)
	if err != nil {
		t.Fatalf("GetLatestPrompt: %v", err)
	}
	if want := fmt.Sprintf(`"prompt_id":%d`, prompt.ID); !strings.Contains(out, want) {
		t.Errorf("the heartbeat frame never named its turn — no %q in:\n%s", want, out)
	}
	if !strings.Contains(out, "event: done") || !strings.Contains(out, `"status":"success"`) {
		t.Errorf("the turn did not finish successfully:\n%s", out)
	}
}

// TestBuildStreamForwardsToolArgProgress pins FM-L8b: a large tool input
// streaming from the provider must produce frames while it streams — Run 2's
// top gap (191 s) was exactly this: text/thinking done, arguments still
// generating, nothing on the wire.
func TestBuildStreamForwardsToolArgProgress(t *testing.T) {
	var reqCount atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		emit := func(frame string) {
			_, _ = fmt.Fprint(w, frame)
			flusher.Flush()
		}
		mk := func(v map[string]any) string {
			b, _ := json.Marshal(v)
			return "data: " + string(b) + "\n\n"
		}
		if reqCount.Add(1) == 1 {
			// Round 1: name first, then argument fragments over a stretched
			// window — the silence the sink cannot represent.
			emit(mk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{
				"tool_calls": []map[string]any{{"index": 0, "id": "call_1",
					"function": map[string]any{"name": "write_file", "arguments": ""}}},
			}}}}))
			for _, frag := range []string{`{"path":"app/`, `index.tsx","content":`, `"streaks"} `} {
				time.Sleep(60 * time.Millisecond)
				emit(mk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{
					"tool_calls": []map[string]any{{"index": 0,
						"function": map[string]any{"arguments": frag}}},
				}}}}))
			}
			emit(mk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{},
				"finish_reason": "tool_calls"}}}))
			emit("[DONE]\n\n")
			return
		}
		// Round 2: finish.
		emit(mk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": "Done."}}}}))
		emit(mk(map[string]any{"choices": []map[string]any{{"delta": map[string]any{},
			"finish_reason": "end_turn"}}}))
		emit("[DONE]\n\n")
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "build-args.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("buildargs@local", "hash")
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
		fmt.Sprintf(`{"project_id":%d,"prompt":"a streak app","mode":"build"}`, project.ID)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()

	if !strings.Contains(out, "event: tool_input.delta") {
		t.Errorf("tool-argument fragments never reached the wire — the 191 s gap is back:\n%s", out)
	}
	if !strings.Contains(out, `"tool":"write_file"`) {
		t.Errorf("the tool_input.delta frame did not name the tool:\n%s", out)
	}
	// The frame is progress, not a call: its data must carry no status, or
	// the scorecard's running/terminal pairing would count it (S4/S1).
	var lastEvent string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "event: ") {
			lastEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if lastEvent == "tool_input.delta" && strings.HasPrefix(line, "data: ") &&
			strings.Contains(line, `"status"`) {
			t.Errorf("tool_input.delta data carries a status — it would corrupt tool pairing: %s", line)
		}
	}
}
