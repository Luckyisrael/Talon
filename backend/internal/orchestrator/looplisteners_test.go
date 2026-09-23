package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// W14.1 eval gate, clause one: legacy vs driver produce identical
// transcripts on the fake provider (H2's gate). The script exercises every
// §2.4 row that has an observable — a write (FSWrite + verify cadence), a
// read_skill (dispatch/skill), an erroring shell (ToolResult error-shaped),
// and the guard stage around them — and both polarities run it with the
// same listeners registered: the flag, not registration, is what arms the
// bus.
func TestLoopDriverTranscriptParity(t *testing.T) {
	first := []map[string]any{
		{"type": "tool_use", "id": "a", "name": "write_file",
			"input": map[string]any{"path": "lib/util.ts", "content": "export const x = 1;"}},
		{"type": "tool_use", "id": "b", "name": "read_skill",
			"input": map[string]any{"name": "wallet-mwa"}},
		{"type": "tool_use", "id": "c", "name": "run_shell",
			"input": map[string]any{"command": "echo hi"}},
	}

	run := func(flag string) ([]ai.Message, map[string]int, *loopEvents) {
		srv := loopScriptServer(t, first)
		defer srv.Close()
		t.Setenv("AI_PROTOCOL", "anthropic")
		t.Setenv("AI_BASE_URL", srv.URL)
		t.Setenv("AI_API_KEY", "test-key")
		t.Setenv("AI_MODEL", "test-model")
		t.Setenv("TALONSTUDIO_LOOP", flag)

		client, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		o := New(client, nil, loadKnowledge(t))
		ev := registerLoopProbe(o)
		sess := newTestSession(t)
		defer sess.Emitter.Close()

		execs := map[string]int{}
		var emu sync.Mutex
		opts := RunOptions{
			Mode:    "build",
			History: []ai.Message{ai.UserText("scaffold the app")},
			Tools: []ai.ToolDefinition{
				{Name: "write_file"}, {Name: "read_skill"}, {Name: "run_shell"},
			},
			Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
				emu.Lock()
				execs[name]++
				emu.Unlock()
				switch name {
				case "run_shell":
					return "permission denied", true
				default:
					return `{"ok":true}`, false
				}
			},
			FilesTouched: func() []string { return []string{"lib/util.ts"} },
		}
		outcome, err := o.RunAgentLoop(context.Background(), sess, opts)
		if err != nil {
			t.Fatalf("RunAgentLoop(%s): %v", flag, err)
		}
		return outcome.Messages, execs, ev
	}

	// Legacy first: explicit opt-out (unset would also mean legacy, but the
	// gate states both polarities by name).
	legacyMsgs, legacyExecs, legacyEv := run("legacy")
	// Driver: same registration, flag armed.
	driverMsgs, driverExecs, driverEv := run("driver")

	// The transcript is the gate: identical messages, byte for byte.
	lj, _ := json.Marshal(legacyMsgs)
	dj, _ := json.Marshal(driverMsgs)
	if string(lj) != string(dj) {
		t.Errorf("transcripts differ between loop modes:\nlegacy %s\ndriver %s", lj, dj)
	}
	if len(legacyMsgs) == 0 {
		t.Fatal("script produced no transcript")
	}

	// Flag off: subscriptions are registered but silent — today's path.
	if n := legacyEv.total(); n != 0 {
		t.Errorf("legacy flag must not publish to listeners, got %d event(s)", n)
	}
	// Flag on: every §2.4 row that has an observable fired.
	if got := countStage(driverEv.dispatch, "start"); got < 3 {
		t.Errorf("driver ToolDispatch(start) events: %d, want >= 3", got)
	}
	if got := countStage(driverEv.dispatch, "skill"); got != 1 {
		t.Errorf("driver ToolDispatch(skill) events: %d, want 1", got)
	}
	errResults := 0
	for _, r := range driverEv.result {
		if r.IsError {
			errResults++
		}
	}
	if len(driverEv.result) < 3 || errResults == 0 {
		t.Errorf("driver ToolResult events: %d total, %d error-shaped; want >= 3 and >= 1",
			len(driverEv.result), errResults)
	}
	if len(driverEv.writes) != 1 || driverEv.writes[0].Path != "lib/util.ts" {
		t.Errorf("driver FSWrite events: %+v, want exactly one for lib/util.ts", driverEv.writes)
	}

	// Both modes executed the same tools the same way (the refusal-shaped
	// shell error did not reroute dispatch through the bus).
	for _, name := range []string{"write_file", "read_skill", "run_shell"} {
		if legacyExecs[name] != driverExecs[name] {
			t.Errorf("executor calls for %s differ: legacy=%d driver=%d",
				name, legacyExecs[name], driverExecs[name])
		}
	}
}

// W14.1's DSH extensibility test, half one: a guardrail defined in a NEW
// file (this one) attaches with a single Subscribe call and vetoes a call —
// zero edits to the loop, the guard stage, or any existing listener. The
// composition-row half (the same guardrail arriving as one YAML row) is
// W14.2's composer; the seam it plugs into is this.
func TestLoopPolicyListenerVetoes(t *testing.T) {
	first := []map[string]any{
		{"type": "tool_use", "id": "a", "name": "run_shell",
			"input": map[string]any{"command": "rm -rf /"}},
	}

	run := func(flag string, veto bool) ([]ai.Message, int) {
		srv := loopScriptServer(t, first)
		defer srv.Close()
		t.Setenv("AI_PROTOCOL", "anthropic")
		t.Setenv("AI_BASE_URL", srv.URL)
		t.Setenv("AI_API_KEY", "test-key")
		t.Setenv("AI_MODEL", "test-model")
		t.Setenv("TALONSTUDIO_LOOP", flag)

		client, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		o := New(client, nil, loadKnowledge(t))
		if veto {
			o.SubscribeLoopPolicy(func(PolicyEvent) PolicyDecision {
				return PolicyDecision{Allow: false, Rule: "no-shell", Reason: "shells are gated in this project"}
			})
		}
		sess := newTestSession(t)
		defer sess.Emitter.Close()

		shells := 0
		var mu sync.Mutex
		opts := RunOptions{
			Mode:    "build",
			History: []ai.Message{ai.UserText("clean everything up")},
			Tools:   []ai.ToolDefinition{{Name: "run_shell"}},
			Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
				mu.Lock()
				shells++
				mu.Unlock()
				return "ran", false
			},
		}
		outcome, err := o.RunAgentLoop(context.Background(), sess, opts)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		return outcome.Messages, shells
	}

	// Driver + veto: the call never reaches the executor, and the refusal
	// reads exactly like a native guardrail — one vocabulary.
	msgs, shells := run("driver", true)
	if shells != 0 {
		t.Errorf("vetoed run_shell reached the executor %d time(s)", shells)
	}
	if !messagesContain(msgs, "Blocked by a project guardrail (no-shell): shells are gated in this project") {
		t.Errorf("veto refusal not in transcript: %s", mustJSON(msgs))
	}

	// Flag off: the same registered veto is inert — today's path.
	msgs, shells = run("legacy", true)
	if shells != 1 {
		t.Errorf("legacy flag must ignore subscriptions: run_shell executed %d time(s), want 1", shells)
	}
	if messagesContain(msgs, "no-shell") {
		t.Errorf("legacy transcript must not carry the driver-only veto: %s", mustJSON(msgs))
	}
}

// Extension failure never corrupts the loop (DSH's law): a panicking policy
// listener fails closed — the call refuses, with the failure named where
// the model sees it — and a panicking observer is logged and skipped while
// the rest of the walk continues.
func TestLoopListenerPanicIsolation(t *testing.T) {
	first := []map[string]any{
		{"type": "tool_use", "id": "a", "name": "run_shell",
			"input": map[string]any{"command": "echo hi"}},
	}

	run := func(register func(o *Orchestrator)) ([]ai.Message, int) {
		srv := loopScriptServer(t, first)
		defer srv.Close()
		t.Setenv("AI_PROTOCOL", "anthropic")
		t.Setenv("AI_BASE_URL", srv.URL)
		t.Setenv("AI_API_KEY", "test-key")
		t.Setenv("AI_MODEL", "test-model")
		t.Setenv("TALONSTUDIO_LOOP", "driver")

		client, err := ai.New()
		if err != nil {
			t.Fatalf("ai.New: %v", err)
		}
		o := New(client, nil, loadKnowledge(t))
		register(o)
		sess := newTestSession(t)
		defer sess.Emitter.Close()

		shells := 0
		var mu sync.Mutex
		opts := RunOptions{
			Mode:    "build",
			History: []ai.Message{ai.UserText("say hi")},
			Tools:   []ai.ToolDefinition{{Name: "run_shell"}},
			Execute: func(_ context.Context, _ string, _ json.RawMessage) (string, bool) {
				mu.Lock()
				shells++
				mu.Unlock()
				return "ran", false
			},
		}
		outcome, err := o.RunAgentLoop(context.Background(), sess, opts)
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
		return outcome.Messages, shells
	}

	// A panicking policy listener fails closed.
	msgs, shells := run(func(o *Orchestrator) {
		o.SubscribeLoopPolicy(func(PolicyEvent) PolicyDecision {
			panic("listener blew up")
		})
	})
	if shells != 0 {
		t.Errorf("failed-closed policy must not dispatch: executor ran %d time(s)", shells)
	}
	if !messagesContain(msgs, "failing closed") {
		t.Errorf("fail-closed refusal must name itself: %s", mustJSON(msgs))
	}

	// A panicking observer is skipped; the healthy observer still runs and
	// the turn completes.
	seen := 0
	msgs, shells = run(func(o *Orchestrator) {
		o.SubscribeLoopDispatch(func(DispatchEvent) {
			panic("observer blew up")
		})
		o.SubscribeLoopDispatch(func(DispatchEvent) {
			seen++
		})
	})
	if shells != 1 {
		t.Errorf("observer panic must not block dispatch: executor ran %d time(s), want 1", shells)
	}
	if seen == 0 {
		t.Error("listener after the panicking one never ran — isolation walk is broken")
	}
	if len(msgs) == 0 {
		t.Error("turn produced no transcript")
	}
}

// The flag itself: only an explicit driver arms the bus. A typo, an
// explicit legacy, or an off value must all mean today's path — a migration
// flag failing open would be the wrong direction.
func TestLoopFlagGate(t *testing.T) {
	cases := []struct {
		set  string
		want bool
	}{
		{"", false},
		{"legacy", false},
		{"LEGACY", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"garbage", false},
		{"driver", true},
		{" DRIVER ", true},
		{"1", true},
		{"true", true},
		{"on", true},
		{"v1", true},
	}
	for _, c := range cases {
		t.Run("set="+c.set, func(t *testing.T) {
			t.Setenv("TALONSTUDIO_LOOP", c.set)
			if got := loopDriverEnabled(); got != c.want {
				t.Errorf("loopDriverEnabled(%q) = %v, want %v", c.set, got, c.want)
			}
		})
	}
}

// loopScriptServer answers round 1 with first (tool calls) and every later
// round with a prose finish — the scripted-provider idiom the parity test
// family uses.
func loopScriptServer(t *testing.T, first []map[string]any) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":     first,
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
}

// loopEvents records what the four subscriptions saw.
type loopEvents struct {
	mu       sync.Mutex
	dispatch []DispatchEvent
	result   []ResultEvent
	writes   []FSWriteEvent
	policy   int
}

func (e *loopEvents) total() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.dispatch) + len(e.result) + len(e.writes) + e.policy
}

// registerLoopProbe attaches one observer per channel and returns the
// recorder. The policy probe allows everything — it only counts.
func registerLoopProbe(o *Orchestrator) *loopEvents {
	ev := &loopEvents{}
	o.SubscribeLoopPolicy(func(PolicyEvent) PolicyDecision {
		ev.mu.Lock()
		ev.policy++
		ev.mu.Unlock()
		return PolicyDecision{Allow: true}
	})
	o.SubscribeLoopDispatch(func(e DispatchEvent) {
		ev.mu.Lock()
		ev.dispatch = append(ev.dispatch, e)
		ev.mu.Unlock()
	})
	o.SubscribeLoopResult(func(e ResultEvent) {
		ev.mu.Lock()
		ev.result = append(ev.result, e)
		ev.mu.Unlock()
	})
	o.SubscribeLoopFSWrite(func(e FSWriteEvent) {
		ev.mu.Lock()
		ev.writes = append(ev.writes, e)
		ev.mu.Unlock()
	})
	return ev
}

func countStage(evs []DispatchEvent, stage string) int {
	n := 0
	for _, e := range evs {
		if e.Stage == stage {
			n++
		}
	}
	return n
}

func messagesContain(msgs []ai.Message, want string) bool {
	return strings.Contains(mustJSON(msgs), want)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
