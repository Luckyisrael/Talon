package evals

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
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	transport "github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/hooks"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
)

// W10 phase gate — TestPhaseW10HookDeny.
//
// The plan's three clauses, each against a real RunAgentLoop over a
// request-aware mock provider:
//
//  1. A blocking hook prevents dispatch and yields the model-visible
//     reason (the spy's Execute never sees the write; the refusal lands
//     on the tool_result the model reads).
//  2. An async hook runs after Write without delaying the round — proved
//     by log timestamps: the turn returns in less time than the hook's
//     own runtime, and the hook/exec row is stamped after the write.
//  3. A hook failure closes the turn (DSH's law): honest FinalText,
//     HookStop, nil error, and the round loop dispatches no further
//     model request — extension failure never corrupts the loop.

// w10AuditRow is one in-memory log row with its arrival time — the
// "log timestamps" clause asserts against these.
type w10AuditRow struct {
	kind    events.Kind
	payload any
	at      time.Time
}

type w10AuditLog struct {
	mu   sync.Mutex
	rows []w10AuditRow
}

func (l *w10AuditLog) Append(kind events.Kind, payload any) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows = append(l.rows, w10AuditRow{kind: kind, payload: payload, at: time.Now()})
	return int64(len(l.rows)), nil
}

func (l *w10AuditLog) Sync() error { return nil }

// findHook returns the first hook/exec row for the named hook.
func (l *w10AuditLog) findHook(name string) (events.HookPayload, time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.rows {
		if r.kind != events.HookExec {
			continue
		}
		if hp, ok := r.payload.(events.HookPayload); ok && hp.Hook == name {
			return hp, r.at, true
		}
	}
	return events.HookPayload{}, time.Time{}, false
}

// w10Workspace writes a .talon/config.json into a fresh workspace.
func w10Workspace(t *testing.T, config string) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".talon", "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// w10Turn is one executed turn plus what the gate needs to measure.
type w10Turn struct {
	outcome  *orchestrator.BuildOutcome
	audit    *w10AuditLog
	requests func() int
	elapsed  time.Duration

	mu      sync.Mutex
	writes  int
	writeAt time.Time
}

func (r *w10Turn) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *w10Turn) lastWrite() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writeAt
}

// runW10Turn drives one full loop against a request-aware mock: the first
// model request issues toolName/toolArgs, later requests end the turn.
// Flags and provider env are set here; a hook-refused or hook-closed turn
// returns a nil error by contract (W10), so any error is a real failure.
func runW10Turn(t *testing.T, ws, toolName, toolArgs string, touched []string) *w10Turn {
	t.Helper()

	t.Setenv("TALONSTUDIO_HOOKS", "1")

	var reqMu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMu.Lock()
		requests++
		n := requests
		reqMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{{
							"id":   "call-1",
							"type": "function",
							"function": map[string]any{
								"name":      toolName,
								"arguments": toolArgs,
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "done"},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "w10-model")

	client, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := orchestrator.New(client, nil, nil)
	audit := &w10AuditLog{}
	sess := orchestrator.NewSession(0, 1, ws, transport.NewSessionEmitter(0, 64))
	go func() {
		for {
			select {
			case <-sess.Emitter.Done():
				return
			case <-sess.Emitter.Events():
			}
		}
	}()
	defer sess.Emitter.Close()

	res := &w10Turn{audit: audit, requests: func() int {
		reqMu.Lock()
		defer reqMu.Unlock()
		return requests
	}}

	start := time.Now()
	outcome, runErr := o.RunAgentLoop(context.Background(), sess, orchestrator.RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("build the thing")},
		EventLog:     audit,
		Hooks:        hooks.Load(ws),
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "write_file" {
				res.mu.Lock()
				res.writes++
				res.writeAt = time.Now()
				res.mu.Unlock()
				return `{"ok":true}`, false
			}
			out, _ := json.Marshal(map[string]any{"passed": true, "summary": "clean"})
			return string(out), false
		},
		FilesTouched: func() []string { return touched },
	})
	res.elapsed = time.Since(start)
	if runErr != nil {
		// Both deliberate stops (deny, hook-close) return nil by contract;
		// anything else is a real failure.
		t.Fatalf("W10 stops are deliberate, not errors: %v", runErr)
	}
	res.outcome = outcome
	return res
}

// Clause 1 — blocking hook prevents dispatch and yields the model-visible
// reason. Uses the block-env-edits built-in: the secrets class moved out
// of prompt text into enforcement.
func TestPhaseW10HookDeny(t *testing.T) {
	ws := w10Workspace(t, `{"hooks":{"PreToolUse":[{"builtin":"block-env-edits"}]}}`)
	r := runW10Turn(t, ws, "write_file", `{"path":".env","content":"SECRET=1"}`, []string{".env"})

	if got := r.writeCount(); got != 0 {
		t.Errorf("a blocking hook must prevent dispatch: write_file ran %d time(s)", got)
	}

	// The model-visible reason: the refusal lands where the model reads it.
	blob, err := json.Marshal(r.outcome.Messages)
	if err != nil {
		t.Fatalf("marshal outcome: %v", err)
	}
	if !strings.Contains(string(blob), "Blocked by a project hook") {
		t.Errorf("model-visible refusal missing from the turn, got %s", blob)
	}
	if !strings.Contains(string(blob), ".env.example") {
		t.Errorf("refusal must name the escape hatch (.env.example), got %s", blob)
	}

	// The model saw the refusal and got to answer — a deny never corrupts
	// the loop, it feeds it.
	if r.requests() != 2 {
		t.Errorf("refusal must reach the model and let it answer: %d model requests", r.requests())
	}

	// The execution audited: one hook/exec row, denied, named.
	hp, _, ok := r.audit.findHook("builtin:block-env-edits")
	if !ok {
		t.Fatal("blocking hook execution must audit a hook/exec row")
	}
	if hp.Decision != hooks.Deny {
		t.Errorf("audited decision = %q, want deny", hp.Decision)
	}
	if hp.Error != "" {
		t.Errorf("deny is not an extension failure, got error %q", hp.Error)
	}
}

// Clause 2 — an async hook runs after Write without delaying the round,
// proved by log timestamps: the turn returns in less wall time than the
// hook itself takes, and the hook/exec row is stamped after the write.
func TestPhaseW10AsyncHookRunsAfterWrite(t *testing.T) {
	ws := w10Workspace(t, `{"hooks":{"PostToolUse":[
		{"command":"sleep 1","name":"slow-tests","async":true,"tools":["write_file"]}
	]}}`)
	r := runW10Turn(t, ws, "write_file", `{"path":"src/app.ts","content":"x"}`, []string{"src/app.ts"})

	if r.writeCount() != 1 {
		t.Fatalf("ordinary write must dispatch: %d run(s)", r.writeCount())
	}
	if r.elapsed >= 900*time.Millisecond {
		t.Errorf("async hook must not delay the round: turn took %s waiting on a 1s hook", r.elapsed)
	}
	if !r.outcome.Verified && r.outcome.FinalText == "" {
		t.Error("turn must complete normally with the async hook in flight")
	}

	// The row lands after the write, with the hook's own runtime in its
	// duration — polled, never blocking the assertions above.
	deadline := time.Now().Add(5 * time.Second)
	for {
		hp, at, ok := r.audit.findHook("slow-tests")
		if ok {
			if !at.After(r.lastWrite()) {
				t.Errorf("async hook must run after Write: row at %s, write at %s", at, r.lastWrite())
			}
			if hp.DurationMS < 900 {
				t.Errorf("row must carry the hook's runtime (log timestamps): %dms", hp.DurationMS)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("async hook never audited its execution")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Clause 3 — a hook failure closes the turn per DSH's law: honest
// FinalText, HookStop, nil error, zero dispatch, and no second model
// request — the extension broke, the loop did not.
func TestPhaseW10HookFailureClosesTurn(t *testing.T) {
	ws := w10Workspace(t, `{"hooks":{"PreToolUse":[{"command":"echo crashed >&2; exit 3","name":"boom"}]}}`)
	r := runW10Turn(t, ws, "write_file", `{"path":"src/x.ts","content":"x"}`, []string{"src/x.ts"})

	if got := r.writeCount(); got != 0 {
		t.Errorf("failed hook must fail closed: write_file ran %d time(s)", got)
	}
	if !r.outcome.HookStop {
		t.Error("hook failure must set HookStop")
	}
	if !strings.Contains(r.outcome.FinalText, "project hook failed") {
		t.Errorf("closing text must name the hook failure, got %q", r.outcome.FinalText)
	}
	if r.requests() != 1 {
		t.Errorf("the turn must close before round two dispatches: %d model requests", r.requests())
	}
	hp, _, ok := r.audit.findHook("boom")
	if !ok {
		t.Fatal("failed hook execution must audit a hook/exec row")
	}
	if hp.Error == "" {
		t.Error("the audited row must carry the extension failure")
	}
}
