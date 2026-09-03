package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The validator exists to end the "malformed edit silently does nothing" death
// spiral: it must reject a call whose arguments do not fit the tool's schema, with
// a message that names the right fields, and it must never reject a call the tool
// would have accepted.

func TestValidateArgs(t *testing.T) {
	// A representative flat schema: two known fields, both required — write_file's shape.
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":    {"type": "string"},
			"content": {"type": "string"}
		},
		"required": ["path", "content"]
	}`)

	t.Run("valid args pass", func(t *testing.T) {
		msg, ok := validateArgs("write_file", schema, json.RawMessage(`{"path":"a.ts","content":"x"}`))
		if !ok || msg != "" {
			t.Errorf("valid args rejected: ok=%v msg=%q", ok, msg)
		}
	})

	t.Run("unknown field is rejected and named", func(t *testing.T) {
		// old_string belongs to edit_file — exactly the cross-tool confusion observed.
		msg, ok := validateArgs("write_file", schema, json.RawMessage(`{"path":"a.ts","content":"x","old_string":"y"}`))
		if ok {
			t.Fatal("unknown field accepted")
		}
		// The message must say nothing ran and name both the stray field and the
		// fields this tool actually takes, so the model corrects in one round.
		for _, want := range []string{"did not run", "old_string", "path", "content"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message missing %q: %s", want, msg)
			}
		}
	})

	t.Run("missing required field is rejected and named", func(t *testing.T) {
		msg, ok := validateArgs("write_file", schema, json.RawMessage(`{"path":"a.ts"}`))
		if ok {
			t.Fatal("missing required field accepted")
		}
		if !strings.Contains(msg, "content") || !strings.Contains(msg, "Missing required") {
			t.Errorf("message should name the missing required field: %s", msg)
		}
	})

	t.Run("both problems reported together", func(t *testing.T) {
		msg, ok := validateArgs("write_file", schema, json.RawMessage(`{"bogus":1}`))
		if ok {
			t.Fatal("call with an unknown field and missing required fields was accepted")
		}
		if !strings.Contains(msg, "Unexpected") || !strings.Contains(msg, "Missing required") {
			t.Errorf("want both an Unexpected and a Missing required clause: %s", msg)
		}
	})

	// Permissive-by-design: the validator guards against a known failure, not a
	// second type system, so anything it cannot confidently judge passes through.
	// This is the invariant that guarantees it never rejects a valid call.
	t.Run("permissive when it cannot judge", func(t *testing.T) {
		cases := []struct {
			name         string
			schema, args json.RawMessage
		}{
			{"no properties (run_typecheck)", json.RawMessage(`{"type":"object"}`), json.RawMessage(`{"anything":1}`)},
			{"empty properties object", json.RawMessage(`{"type":"object","properties":{}}`), json.RawMessage(`{"x":1}`)},
			{"unparseable schema", json.RawMessage(`not json`), json.RawMessage(`{"x":1}`)},
			{"args not an object", schema, json.RawMessage(`"a string"`)},
			{"args are an array", schema, json.RawMessage(`[1,2]`)},
		}
		for _, tc := range cases {
			if msg, ok := validateArgs("t", tc.schema, tc.args); !ok || msg != "" {
				t.Errorf("%s: should pass through, got ok=%v msg=%q", tc.name, ok, msg)
			}
		}
	})

	t.Run("optional-only schema: unknown still caught, nothing ever missing", func(t *testing.T) {
		// build_apk's shape — properties but no required array.
		noReq := json.RawMessage(`{"type":"object","properties":{"install":{"type":"boolean"}}}`)
		if _, ok := validateArgs("build_apk", noReq, json.RawMessage(`{"install":true}`)); !ok {
			t.Error("valid optional-only call rejected")
		}
		if _, ok := validateArgs("build_apk", noReq, json.RawMessage(`{}`)); !ok {
			t.Error("empty args rejected for an optional-only schema")
		}
		if msg, ok := validateArgs("build_apk", noReq, json.RawMessage(`{"nope":true}`)); ok || !strings.Contains(msg, "nope") {
			t.Errorf("unknown field not caught for optional-only schema: ok=%v msg=%q", ok, msg)
		}
	})
}

// A schema'd tool that records whether Execute ran, to prove validation happens at
// the dispatch point — before the tool — so a malformed call can never reach it.
type schemaTool struct{ ran *bool }

func (schemaTool) Name() string        { return "schema_tool" }
func (schemaTool) Description() string { return "stub" }
func (schemaTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}
func (s schemaTool) Execute(_ context.Context, _ json.RawMessage) (ToolResult, error) {
	*s.ran = true
	return ToolResult{Content: "ran"}, nil
}

func TestRegistryExecuteValidates(t *testing.T) {
	t.Run("bad args are rejected before the tool runs", func(t *testing.T) {
		ran := false
		r := NewRegistry()
		r.Register(schemaTool{ran: &ran})
		res, err := r.Execute(context.Background(), "schema_tool", json.RawMessage(`{"wrong":1}`))
		if err != nil {
			t.Fatalf("Execute should not return a Go error for bad args: %v", err)
		}
		if !res.IsError {
			t.Errorf("malformed call not flagged as an error: %q", res.Content)
		}
		if ran {
			t.Error("tool executed despite invalid arguments — validation must gate it")
		}
		if !strings.Contains(res.Content, "did not run") {
			t.Errorf("error must tell the model nothing happened: %q", res.Content)
		}
	})

	t.Run("valid args reach the tool", func(t *testing.T) {
		ran := false
		r := NewRegistry()
		r.Register(schemaTool{ran: &ran})
		res, _ := r.Execute(context.Background(), "schema_tool", json.RawMessage(`{"path":"a.ts"}`))
		if res.IsError || !ran {
			t.Errorf("valid call did not reach the tool: is_error=%v ran=%v content=%q", res.IsError, ran, res.Content)
		}
	})
}
