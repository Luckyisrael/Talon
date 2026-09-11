package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	harnessagent "github.com/luckyisrael/talon/backend/internal/harness/agent"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// The H2 steer contract, end to end: a message delivered to a running turn
// lands in the very next model request, marked as a steer — and a turn nobody
// steers carries no steer turns. This is the eval gate for the phase: steer
// semantics are only correct if the redirected round actually sees the text.
func TestSteerLandsInNextRequest(t *testing.T) {
	var mu sync.Mutex
	modelCalls := 0
	// round2Body captures what the model was sent on its second request —
	// where a steer delivered during round 1 must appear.
	var round2Body []byte
	// steerReady closes when the steer has been delivered into the session
	// lane. Round 1's response is held until then: the rendezvous guarantees
	// the steer arrives while the loop is still inside round 1, so it must
	// drain at the round-2 boundary — the exact ordering the test asserts.
	steerReady := make(chan struct{})

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		modelCalls++
		n := modelCalls
		mu.Unlock()

		var reqBody []byte
		if r.Body != nil {
			// Best-effort read; a read error fails the assertion below, not
			// the server.
			buf := make([]byte, 1<<20)
			read, _ := r.Body.Read(buf)
			reqBody = buf[:read]
		}
		if n == 2 {
			mu.Lock()
			round2Body = reqBody
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			// Hold round 1 open until the steer has landed in the session
			// lane — the loop is blocked on this response, which is exactly
			// when a user types. The response then completes the round and the
			// next boundary drains the steer into round 2's request.
			<-steerReady
			// Round 1: a tool call, which keeps the turn running — the window
			// in which the steer is delivered below.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "call-1",
					"name":  "list_files",
					"input": map[string]any{"path": ""},
				}},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
			})
			return
		}
		// Round 2+: finish the turn.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "done"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 5, "output_tokens": 5},
		})
	}))
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)
	defer sess.Emitter.Close()

	// Publish the agent handle exactly as CreateSession does, so the steer
	// path under test is the production one.
	o.agents.Publish(harnessagent.Handle(sess.ID, sess.ID, sess))
	defer o.agents.Unpublish(sess.ID)

	// Deliver the steer while round 1's model request is in flight, then
	// release the held response. The poll on modelCalls finds the moment the
	// first request has arrived (the loop is blocked on the model — exactly
	// when a user types); the delivery itself happens from this goroutine,
	// and only once it is in the lane does round 1's response go out, so the
	// drain at the round-2 boundary is guaranteed to see it.
	go func() {
		for {
			mu.Lock()
			n := modelCalls
			mu.Unlock()
			if n >= 1 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if got := o.Agent(sess.ID).Steer(harnessagent.Steer, "actually use reanimated-v4"); got == 0 {
			t.Error("steer returned sequence 0")
		}
		close(steerReady)
	}()

	outcome, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		Mode:    "build",
		History: []ai.Message{ai.UserText("build the wallet screen")},
		Tools:   testTools(),
		Execute: testExecutor(),
	})
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if outcome == nil || outcome.Rounds < 2 {
		t.Fatalf("expected at least 2 rounds, got %+v", outcome)
	}

	mu.Lock()
	body := string(round2Body)
	mu.Unlock()
	if body == "" {
		t.Fatal("round 2 request body was not captured")
	}
	if !contains(body, "actually use reanimated-v4") {
		t.Errorf("steer text missing from round 2 request:\n%.500s", body)
	}
	if !contains(body, "user steered the running build") {
		t.Errorf("steer marker missing from round 2 request:\n%.500s", body)
	}
}

// The unsteered polarity: the same scripted turn with no steer carries no
// steer marker anywhere in its second request — the marker is evidence of a
// delivery, not decoration.
func TestUnsteeredTurnCarriesNoSteerMarker(t *testing.T) {
	var mu sync.Mutex
	modelCalls := 0
	var round2Body []byte

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		modelCalls++
		n := modelCalls
		mu.Unlock()
		if n == 2 {
			buf := make([]byte, 1<<20)
			read, _ := r.Body.Read(buf)
			mu.Lock()
			round2Body = buf[:read]
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{
					"type":  "tool_use",
					"id":    "call-1",
					"name":  "list_files",
					"input": map[string]any{"path": ""},
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
	defer model.Close()

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := New(aiClient, nil, loadKnowledge(t))
	sess := newTestSession(t)
	defer sess.Emitter.Close()

	if _, err := o.RunAgentLoop(context.Background(), sess, RunOptions{
		Mode:    "build",
		History: []ai.Message{ai.UserText("build the wallet screen")},
		Tools:   testTools(),
		Execute: testExecutor(),
	}); err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}

	mu.Lock()
	body := string(round2Body)
	mu.Unlock()
	if contains(body, "user steered the running build") || contains(body, "user added context mid-build") {
		t.Errorf("unsteered turn carries a steer marker:\n%.500s", body)
	}
}

// Deliver is concurrency-safe with the loop: a steer delivered while the
// session is mid-round never blocks, never panics, and is drained exactly
// once — double delivery would duplicate the redirect in the model request.
func TestSteerDrainedExactlyOnce(t *testing.T) {
	sess := newTestSession(t)
	defer sess.Emitter.Close()

	sess.Deliver(harnessagent.Steer, "first")
	sess.Deliver(harnessagent.Inject, "context note")
	got := sess.DrainSteers()
	if len(got) != 2 {
		t.Fatalf("expected 2 drained, got %d", len(got))
	}
	if got[0].Text != "first" || got[0].Class != harnessagent.Steer {
		t.Errorf("drain order broken: %+v", got[0])
	}
	if got[1].Class != harnessagent.Inject {
		t.Errorf("inject mislabeled: %+v", got[1])
	}
	if again := sess.DrainSteers(); len(again) != 0 {
		t.Errorf("second drain returned %d — the lane must clear", len(again))
	}
}

// The registry publishes and unpublishes honestly: a steer to a dead agent
// fails with nil (the route turns that into a 409), never into a void.
func TestAgentRegistryLifecycle(t *testing.T) {
	r := harnessagent.NewRegistry()
	sess := newTestSession(t)
	a := harnessagent.Handle(7, 7, sess)
	if r.Get(7) != nil {
		t.Fatal("empty registry returned an agent")
	}
	r.Publish(a)
	if r.Get(7) != a {
		t.Fatal("publish did not register")
	}
	if got := r.Get(7).Steer(harnessagent.Steer, "x"); got != 1 {
		t.Errorf("first steer sequence = %d, want 1", got)
	}
	if n := sess.DrainSteers(); len(n) != 1 {
		t.Errorf("handle steer did not reach the session lane: %d", len(n))
	}
	r.Unpublish(7)
	r.Unpublish(7) // idempotent
	if r.Get(7) != nil {
		t.Error("unpublish left the agent registered")
	}
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var _ = events.PhaseIntake // keep the events import for the emitter cleanup path

var _ = knowledge.DefaultFailureIndex // keep the knowledge import for future gate tests

// testTools returns a minimal toolset the scripted model can call.
func testTools() []ai.ToolDefinition {
	return []ai.ToolDefinition{{Name: "list_files", Description: "list files"}}
}

// testExecutor answers every call benignly.
func testExecutor() ToolExecutor {
	return func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		return "ok", false
	}
}
