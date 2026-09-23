package composer

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
	"github.com/luckyisrael/talon/backend/internal/harness/composition"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
)

// testKnowledge mirrors the orchestrator suite's loader (composer sits at
// the same depth, so the same relative template path resolves).
func testKnowledge(t *testing.T) *knowledge.Registry {
	t.Helper()
	r, err := knowledge.Load("../../template/package.json")
	if err != nil {
		t.Fatalf("knowledge.Load: %v", err)
	}
	return r
}

func writeComposition(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "talon.composition.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write composition: %v", err)
	}
	return path
}

// The harness/loop row is composition-as-data for the one knob that used
// to be env-only at boot: applied after New read AI_MAX_ROUNDS, so data
// beats seed (the settings-document precedent) — and the bounds are the
// env reader's own, enforced at apply time, not clamped silently.
func TestApplyLoopRow(t *testing.T) {
	t.Setenv("AI_MAX_ROUNDS", "")
	o := orchestrator.New(nil, nil, testKnowledge(t))
	if got := o.MaxRounds(); got != 60 {
		t.Fatalf("seed MaxRounds = %d, want 60 (default)", got)
	}

	path := writeComposition(t, `
base:
  - id: loop
    component: harness/loop
    config:
      maxRounds: 42
`)
	comp, err := composition.Load(path, "server", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	applied, err := Apply(comp, o)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(applied) != 1 || applied[0] != "loop" {
		t.Errorf("applied = %v, want [loop]", applied)
	}
	if got := o.MaxRounds(); got != 42 {
		t.Errorf("MaxRounds = %d after row, want 42", got)
	}
}

// A loop row that says nothing about the budget leaves the env seed alone —
// composition overrides only where it actually speaks.
func TestApplyLoopRowWithoutKeyKeepsSeed(t *testing.T) {
	t.Setenv("AI_MAX_ROUNDS", "17")
	o := orchestrator.New(nil, nil, testKnowledge(t))

	comp, err := composition.Load(writeComposition(t, "base:\n  - id: loop\n    component: harness/loop\n"), "server", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Apply(comp, o); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := o.MaxRounds(); got != 17 {
		t.Errorf("MaxRounds = %d, want the env seed 17 untouched", got)
	}
}

// A bad row fails the boot with the row named — a wiring typo must never
// clamp silently or skip.
func TestApplyRejectsBadRows(t *testing.T) {
	o := orchestrator.New(nil, nil, testKnowledge(t))

	cases := []struct {
		name string
		doc  string
		want []string
	}{
		{"out of range", "base:\n  - id: loop\n    component: harness/loop\n    config:\n      maxRounds: 9999\n",
			[]string{`row "loop"`, "out of range"}},
		{"not an integer", "base:\n  - id: loop\n    component: harness/loop\n    config:\n      maxRounds: lots\n",
			[]string{`row "loop"`, "must be an integer"}},
		{"unknown component", "base:\n  - id: spaigot\n    component: harness/spaigot\n",
			[]string{`row "spaigot"`, "unknown component", "policy/<name>"}},
		{"policy that is not registered", "base:\n  - id: p\n    component: policy/nope\n",
			[]string{`row "p"`, "unknown component", "registered: deny-tools"}},
		{"policy with no name", "base:\n  - id: p\n    component: policy/\n",
			[]string{`row "p"`, "names no policy"}},
		{"deny-tools without tools", "base:\n  - id: d\n    component: policy/deny-tools\n    config:\n      posture: strict\n",
			[]string{`row "d"`, "config.tools is required"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := orchestrator.New(nil, nil, testKnowledge(t))
			comp, err := composition.Load(writeComposition(t, c.doc), "server", "")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			_, err = Apply(comp, o)
			if err == nil {
				t.Fatal("Apply = nil error, want failure")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
	_ = o
}

// A nil document is a dark boot; nil wiring with rows to place is an
// error — rows may never vanish quietly.
func TestApplyNilEdges(t *testing.T) {
	o := orchestrator.New(nil, nil, testKnowledge(t))
	if applied, err := Apply(nil, o); err != nil || applied != nil {
		t.Errorf("Apply(nil) = %v, %v; want nil, nil", applied, err)
	}
	comp, err := composition.Load(writeComposition(t, "base:\n  - id: loop\n    component: harness/loop\n"), "server", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Apply(comp, nil); err == nil || !strings.Contains(err.Error(), "no wiring") {
		t.Errorf("Apply(comp, nil) = %v, want the no-wiring error", err)
	}
}

// composer.Load's resolution rules: an explicit TALONSTUDIO_COMPOSITION
// fails loud when it does not exist (a typo must not become a dark boot),
// profile/preset come from the env over the binary's default, and the
// default search walks up from the working directory so launching from
// repo root or backend/ resolves the same file.
func TestLoadResolution(t *testing.T) {
	t.Run("explicit path", func(t *testing.T) {
		path := writeComposition(t, "base:\n  - id: loop\n    component: harness/loop\n    config:\n      maxRounds: 3\n")
		t.Setenv("TALONSTUDIO_COMPOSITION", path)
		comp, err := Load("server")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if comp == nil || comp.Path != path {
			t.Fatalf("Load = %+v, want the explicit file", comp)
		}
		if comp.Profile != "server" {
			t.Errorf("profile = %q, want server (binary default)", comp.Profile)
		}
	})

	t.Run("explicit path missing fails loud", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_COMPOSITION", filepath.Join(t.TempDir(), "nope.yaml"))
		if _, err := Load("server"); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("Load = %v, want the does-not-exist error", err)
		}
	})

	t.Run("env profile overrides the binary default", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_COMPOSITION", writeComposition(t, "base:\n  - id: loop\n    component: harness/loop\n"))
		t.Setenv("TALONSTUDIO_PROFILE", "canary")
		comp, err := Load("server")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if comp.Profile != "canary" {
			t.Errorf("profile = %q, want canary", comp.Profile)
		}
	})

	t.Run("cwd walk finds the tree's file", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_COMPOSITION", "")
		root := t.TempDir()
		child := filepath.Join(root, "backend", "cmd")
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		want := filepath.Join(root, "talon.composition.yaml")
		if err := os.WriteFile(want, []byte("base:\n  - id: loop\n    component: harness/loop\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Chdir(child)
		comp, err := Load("headless")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if comp == nil || comp.Path != want {
			t.Fatalf("Load = %+v, want the parent tree's file %s", comp, want)
		}
		if comp.Profile != "headless" {
			t.Errorf("profile = %q, want the binary default headless", comp.Profile)
		}
	})
}

// compScriptServer answers round 1 with the given tool call and every later
// round with prose — the scripted-provider idiom the loop suites use.
func compScriptServer(t *testing.T, first []map[string]any) *httptest.Server {
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

// DSH extensibility, composition-row half (W14.2): a guardrail that arrives
// as ONE row in a NEW YAML file vetoes with zero edits to any Go file —
// deny-tools registers itself in init(), the row wires it, the bus runs it.
// Under TALONSTUDIO_LOOP=legacy the same row is inert, like every policy
// subscription (the flag is what arms the bus, W14.1).
func TestPolicyRowVetoesEndToEnd(t *testing.T) {
	const doc = `
base:
  - id: no-shell
    component: policy/deny-tools
    config:
      tools: [run_shell]
`
	run := func(flag string) ([]ai.Message, int) {
		path := writeComposition(t, doc)
		srv := compScriptServer(t, []map[string]any{
			{"type": "tool_use", "id": "a", "name": "run_shell",
				"input": map[string]any{"command": "rm -rf /"}},
		})
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
		o := orchestrator.New(client, nil, testKnowledge(t))
		comp, err := composition.Load(path, "server", "")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		applied, err := Apply(comp, o)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if len(applied) != 1 || applied[0] != "no-shell" {
			t.Fatalf("applied = %v, want [no-shell]", applied)
		}

		// Direct construction, the orchestrator suite's idiom: CreateSession
		// loads project context through the store, which a bare harness does
		// not have — the loop itself never needs it.
		sess := orchestrator.NewSession(1, 1, t.TempDir(), events.NewSessionEmitter(1, 128))
		defer sess.Emitter.Close()

		shells := 0
		var mu sync.Mutex
		opts := orchestrator.RunOptions{
			Mode:    "build",
			History: []ai.Message{ai.UserText("clean everything up")},
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
			t.Fatalf("RunAgentLoop(%s): %v", flag, err)
		}
		return outcome.Messages, shells
	}

	// Driver: the row's listener vetoes before dispatch — the refusal
	// reads like a native guardrail, one vocabulary.
	msgs, shells := run("driver")
	if shells != 0 {
		t.Errorf("composition-vetoed run_shell reached the executor %d time(s)", shells)
	}
	b, _ := json.Marshal(msgs)
	if !strings.Contains(string(b), "Blocked by a project guardrail (deny-tools): run_shell is denied by the workspace composition") {
		t.Errorf("veto refusal not in transcript: %s", b)
	}

	// Legacy: same row, same Apply — inert, today's path.
	msgs, shells = run("legacy")
	if shells != 1 {
		t.Errorf("legacy must ignore policy rows: run_shell executed %d time(s), want 1", shells)
	}
	b, _ = json.Marshal(msgs)
	if strings.Contains(string(b), "deny-tools") {
		t.Errorf("legacy transcript must not carry the driver-only veto: %s", b)
	}

	// The registry surface dump-config and error messages read.
	found := false
	for _, name := range orchestrator.PolicyComponentNames() {
		if name == "deny-tools" {
			found = true
		}
	}
	if !found {
		t.Errorf("PolicyComponentNames() = %v, want deny-tools registered", orchestrator.PolicyComponentNames())
	}
}
