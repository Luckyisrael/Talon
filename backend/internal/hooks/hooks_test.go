package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".talon", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// Load obeys the flag first: off means no config read at all.
func TestLoadFlagGatesEverything(t *testing.T) {
	ws := writeConfig(t, `{"hooks":{"PreToolUse":[{"command":"cat >/dev/null"}]}}`)

	t.Setenv("TALONSTUDIO_HOOKS", "")
	if r := Load(ws); r != nil {
		t.Error("flag off must not load hooks")
	}
	t.Setenv("TALONSTUDIO_HOOKS", "0")
	if r := Load(ws); r != nil {
		t.Error(`flag "0" must not load hooks`)
	}
	t.Setenv("TALONSTUDIO_HOOKS", "v1")
	if r := Load(ws); r == nil {
		t.Error("flag v1 must load the configured hooks")
	}
}

// An invalid hook config disables hooks entirely — partial enforcement
// would be a silent lie.
func TestLoadInvalidConfigDisablesAll(t *testing.T) {
	cases := map[string]string{
		"unknown event":     `{"hooks":{"PreToolUse":[{"command":"x"}],"PreToolUsee":[{"command":"x"}]}}`,
		"neither source":    `{"hooks":{"PreToolUse":[{"name":"x"}]}}`,
		"both sources":      `{"hooks":{"PreToolUse":[{"command":"x","builtin":"block-env-edits"}]}}`,
		"unknown builtin":   `{"hooks":{"PreToolUse":[{"builtin":"block-the-world"}]}}`,
		"missing webhook":   `{"hooks":{"TurnStop":[{"builtin":"slack-on-turn-fail"}]}}`,
		"missing tests cmd": `{"hooks":{"PostToolUse":[{"builtin":"run-tests-after-write"}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TALONSTUDIO_HOOKS", "1")
			if r := Load(writeConfig(t, body)); r != nil {
				t.Errorf("invalid config %s must disable hooks, got %v", body, r)
			}
		})
	}
	t.Run("absent config is quiet-nil", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_HOOKS", "1")
		if r := Load(t.TempDir()); r != nil {
			t.Error("no config file = nil registry, byte-identical")
		}
	})
}

// The decision combination: first deny wins, ask is the fallback, context
// concatenates, and a broken handler fails closed with ExecutionError.
func TestCheckDecisionContract(t *testing.T) {
	t.Setenv("TALONSTUDIO_HOOKS", "1")

	ws := writeConfig(t, `{"hooks":{"PreToolUse":[
		{"command":"true","name":"ok"},
		{"command":"printf '%s' '{\"decision\":\"ask\",\"additionalContext\":\"check the style guide\"}'","name":"styler"},
		{"command":"printf '%s' '{\"decision\":\"allow\",\"additionalContext\":\"extra eyes\"}'","name":"eyes"}
	]}}`)
	r := Load(ws)
	d, err := r.Check(context.Background(), Payload{Event: PreToolUse, Tool: "write_file"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Decision != Ask {
		t.Errorf("combined decision = %q, want ask", d.Decision)
	}
	if !strings.Contains(d.AdditionalContext, "style guide") || !strings.Contains(d.AdditionalContext, "extra eyes") {
		t.Errorf("additionalContext must concatenate every consulted hook, got %q", d.AdditionalContext)
	}

	// A later deny beats an earlier ask.
	ws2 := writeConfig(t, `{"hooks":{"PreToolUse":[
		{"command":"printf '%s' '{\"decision\":\"ask\"}'","name":"asker"},
		{"command":"printf '%s' '{\"decision\":\"deny\",\"reason\":\"not on my watch\"}'","name":"veto"}
	]}}`)
	d2, err := Load(ws2).Check(context.Background(), Payload{Event: PreToolUse})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d2.Decision != Deny || d2.Reason != "not on my watch" {
		t.Errorf("deny must win outright, got %+v", d2)
	}

	// Exit 2 = block with stderr as the reason.
	ws3 := writeConfig(t, `{"hooks":{"PreToolUse":[{"command":"echo 'nope, secrets' >&2; exit 2","name":"seal"}]}}`)
	d3, err := Load(ws3).Check(context.Background(), Payload{Event: PreToolUse})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d3.Decision != Deny || !strings.Contains(d3.Reason, "nope, secrets") {
		t.Errorf("exit 2 must deny with stderr as the reason, got %+v", d3)
	}

	// Extension failure: a hook that crashes (exit 1) is an error the
	// caller fails closed with — never an implicit allow.
	ws4 := writeConfig(t, `{"hooks":{"PreToolUse":[{"command":"echo boom >&2; exit 1","name":"crashy"}]}}`)
	_, err = Load(ws4).Check(context.Background(), Payload{Event: PreToolUse})
	var ee *ExecutionError
	if !errors.As(err, &ee) {
		t.Fatalf("crashing hook must surface ExecutionError, got %v", err)
	}
	if !strings.Contains(err.Error(), "crashy") {
		t.Errorf("error must name the hook, got %q", err.Error())
	}

	// Contract violations are errors too: invalid JSON on exit 0, and an
	// unknown decision word. The config body is marshalled, not spliced, so
	// quotes inside the command survive.
	for _, cmd := range []string{
		`echo 'not json'`,
		`printf '%s' '{"decision":"maybe"}'`,
	} {
		body, err := json.Marshal(map[string]any{
			"hooks": map[string]any{"PreToolUse": []any{map[string]string{"command": cmd, "name": "bad"}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ws5 := writeConfig(t, string(body))
		if _, err := Load(ws5).Check(context.Background(), Payload{Event: PreToolUse}); err == nil {
			t.Errorf("contract-violating hook %q must be an extension failure", cmd)
		}
	}

	// Tool-scoped entries only fire for their tools.
	ws6 := writeConfig(t, `{"hooks":{"PreToolUse":[{"command":"exit 2","tools":["try_edit"],"name":"scoped"}]}}`)
	r6 := Load(ws6)
	d6, err := r6.Check(context.Background(), Payload{Event: PreToolUse, Tool: "read_file"})
	if err != nil || d6.Decision != Allow {
		t.Errorf("scoped hook must skip other tools: %+v, %v", d6, err)
	}
	d6, err = r6.Check(context.Background(), Payload{Event: PreToolUse, Tool: "try_edit"})
	if err != nil || d6.Decision != Deny {
		t.Errorf("scoped hook must gate its tool: %+v, %v", d6, err)
	}
}

// A nil registry is the pre-W10 path: allow, no I/O, no error.
func TestNilRegistryIsByteIdentical(t *testing.T) {
	var r *Registry
	d, err := r.CheckAudited(context.Background(), Payload{Event: PreToolUse, Tool: "write_file"}, func(ExecRecord) {
		t.Error("nil registry must not dispatch anything, sync or async")
	})
	if err != nil || d.Decision != Allow {
		t.Errorf("nil registry: (%+v, %v), want allow,nil", d, err)
	}
}

// Async entries spawn from Check itself: they never gate the caller (their
// deny does not reach the returned decision) and they still report their
// execution for the audit trail.
func TestAsyncEntriesSpawnAndNeverGate(t *testing.T) {
	t.Setenv("TALONSTUDIO_HOOKS", "1")
	ws := writeConfig(t, `{"hooks":{"TurnStop":[{"command":"printf '%s' '{\"decision\":\"deny\",\"reason\":\"nope\"}'","name":"notifier","async":true}]}}`)
	reg := Load(ws)
	recCh := make(chan ExecRecord, 1)
	d, err := reg.CheckAudited(context.Background(), Payload{Event: TurnStop, Outcome: "failure"},
		func(rec ExecRecord) { recCh <- rec })
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if d.Decision != Allow {
		t.Errorf("async deny must never gate the caller, got %+v", d)
	}
	select {
	case rec := <-recCh:
		if rec.Decision.Decision != Deny || rec.Hook != "notifier" {
			t.Errorf("async execution record = %+v, want the notifier's deny", rec)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async hook never reported its execution")
	}
}

// block-env-edits: real .env files denied, templates allowed, non-mutators
// and non-env paths untouched.
func TestBuiltinBlockEnvEdits(t *testing.T) {
	h := &envBlockHandler{}
	cases := []struct {
		tool, path string
		want       string
	}{
		{"write_file", ".env", Deny},
		{"edit_file", "config/.env.local", Deny},
		{"try_edit", "app/.env.production", Deny},
		{"delete_file", ".env", Deny},
		{"write_file", ".env.example", Allow},
		{"write_file", ".env.sample", Allow},
		{"write_file", "src/env.ts", Allow},
		{"read_file", ".env", Allow}, // reads are the model's problem, not this gate's
	}
	for _, c := range cases {
		in, _ := json.Marshal(map[string]string{"path": c.path})
		d, err := h.Handle(context.Background(), Payload{Event: PreToolUse, Tool: c.tool, Input: in})
		if err != nil {
			t.Fatalf("%s %s: %v", c.tool, c.path, err)
		}
		if d.Decision != c.want {
			t.Errorf("%s %s → %q, want %q", c.tool, c.path, d.Decision, c.want)
		}
		if c.want == Deny && !strings.Contains(d.Reason, ".env.example") {
			t.Errorf("deny reason must point at the template escape hatch, got %q", d.Reason)
		}
	}
}

// Built-in attributes: gating is sync, work/notification are async by
// construction.
func TestBuiltinAsyncConstruction(t *testing.T) {
	t.Setenv("TALONSTUDIO_HOOKS", "1")
	ws := writeConfig(t, `{"hooks":{
		"PreToolUse":[{"builtin":"block-env-edits"}],
		"PostToolUse":[{"builtin":"run-tests-after-write","command":"true","tools":["write_file"]}],
		"TurnStop":[{"builtin":"slack-on-turn-fail","webhook":"`+"`"+`"}]
	}}`)
	r := Load(ws)
	if r == nil {
		t.Fatal("valid config must load")
	}
	if r.byEvent[PreToolUse][0].async {
		t.Error("block-env-edits must be sync (its decision gates dispatch)")
	}
	if !r.byEvent[PostToolUse][0].async {
		t.Error("run-tests-after-write must be async (never delays a round)")
	}
	if !r.byEvent[TurnStop][0].async {
		t.Error("slack-on-turn-fail must be async (notification)")
	}
}
