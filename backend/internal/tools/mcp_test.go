package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeMCP struct {
	text    string
	isError bool
	err     error
	called  string
	args    string
}

func (f *fakeMCP) CallTool(_ context.Context, name string, args json.RawMessage) (string, bool, error) {
	f.called = name
	f.args = string(args)
	return f.text, f.isError, f.err
}

func TestMCPToolNamespaceAndDefaults(t *testing.T) {
	tool := &MCPTool{Server: "github", Remote: "create_issue", RemoteDesc: "make an issue",
		Schema: json.RawMessage(`{"type":"object"}`), Client: &fakeMCP{}}
	if tool.Name() != "mcp__github__create_issue" {
		t.Errorf("name = %q, want mcp__ namespace", tool.Name())
	}
	if !strings.HasPrefix(tool.Description(), "[MCP server github]") {
		t.Errorf("description must carry the server, got %q", tool.Description())
	}
	// Read-only by default (W11 item 3): no explicit mutate allow, no
	// mutation — the registry classifies it concurrent-safe and the
	// posture check waves it through.
	if !tool.ReadOnly() {
		t.Error("MCP tools must be read-only by default")
	}
	tool.Mutating = true
	if tool.ReadOnly() {
		t.Error("explicit mutate allow must promote the tool")
	}
}

func TestMCPToolDeniedStaysListedButRefuses(t *testing.T) {
	fake := &fakeMCP{text: "should never run"}
	tool := &MCPTool{Server: "s", Remote: "danger", Deny: "denied by this server's deny list (.talon/config.json)",
		Client: fake}
	// H3: still advertised (Name/Description/InputSchema intact)…
	_ = tool.Description()
	_ = tool.InputSchema()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("policy refusal is not a Go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("denied call must be an is_error tool result")
	}
	if fake.called != "" {
		t.Fatal("denied call must never reach the transport")
	}
	if !strings.Contains(res.Content, "MCP policy") || !strings.Contains(res.Content, "deny list") {
		t.Errorf("refusal must name the policy, got %q", res.Content)
	}
}

func TestMCPToolExecutesAndMapsFailures(t *testing.T) {
	fake := &fakeMCP{text: "remote says hi"}
	tool := &MCPTool{Server: "s", Remote: "echo", Client: fake}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"m":1}`))
	if err != nil || res.IsError {
		t.Fatalf("happy path: %+v err=%v", res, err)
	}
	if res.Content != "remote says hi" || fake.called != "echo" || fake.args != `{"m":1}` {
		t.Errorf("args did not ride through: content=%q called=%q args=%q", res.Content, fake.called, fake.args)
	}

	toolErr := &MCPTool{Server: "s", Remote: "echo",
		Client: &fakeMCP{isError: true, text: "server says no"}}
	res, _ = toolErr.Execute(context.Background(), nil)
	if !res.IsError {
		t.Error("server isError must map to is_error")
	}
	if res.Content != "server says no" {
		t.Errorf("server verdict text must ride through, got %q", res.Content)
	}

	fake3 := &fakeMCP{err: context.DeadlineExceeded}
	toolT := &MCPTool{Server: "s", Remote: "echo", Client: fake3}
	res, err = toolT.Execute(context.Background(), nil)
	if err != nil {
		t.Errorf("transport failure must be a failed call, not a Go error: %v", err)
	}
	if !res.IsError {
		t.Error("transport failure must be is_error")
	}

	bare := &MCPTool{Server: "s", Remote: "x"}
	res, _ = bare.Execute(context.Background(), nil)
	if !res.IsError || !strings.Contains(res.Content, "not connected") {
		t.Errorf("nil client must refuse honestly, got %+v", res)
	}
}

func TestSchemaOrObject(t *testing.T) {
	if got := string(SchemaOrObject(nil)); got != `{"type":"object"}` {
		t.Errorf("nil schema = %s", got)
	}
	if got := string(SchemaOrObject(json.RawMessage(`[1,2]`))); got != `{"type":"object"}` {
		t.Errorf("non-object schema = %s", got)
	}
	if got := string(SchemaOrObject(json.RawMessage(`{"type":"object"}`))); got != `{"type":"object"}` {
		t.Errorf("valid schema mangled: %s", got)
	}
}

func TestPluginToolRunsFromDirWithStdinStdout(t *testing.T) {
	// Direct argv (no shell added by the tool): cat maps stdin→stdout, so
	// this proves the W10-style contract — JSON args in, text out.
	tool := &PluginTool{
		Plugin: "demo", Remote: "whoami", Dir: t.TempDir(),
		Argv: []string{"/bin/cat"},
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"a":1}`))
	if err != nil || res.IsError {
		t.Fatalf("cat must succeed: %+v err=%v", res, err)
	}
	if res.Content != `{"a":1}` {
		t.Errorf("stdin/stdout contract broken: %q", res.Content)
	}
}

func TestPluginToolFailureCarriesStderr(t *testing.T) {
	tool := &PluginTool{
		Plugin: "demo", Remote: "bad", Dir: t.TempDir(),
		Argv: []string{"/bin/sh", "-c", "echo exploded >&2; exit 3"},
	}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("non-zero exit is not a Go error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "exploded") {
		t.Errorf("failure must carry stderr: %+v", res)
	}
}

func TestPluginToolEmptyArgvRefuses(t *testing.T) {
	tool := &PluginTool{Plugin: "demo", Remote: "x", Dir: t.TempDir()}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil || !res.IsError || !strings.Contains(res.Content, "no command") {
		t.Errorf("empty argv must refuse honestly: %+v err=%v", res, err)
	}
}

func TestPluginToolNamespacedAndReadOnly(t *testing.T) {
	tool := &PluginTool{Plugin: "p-x", Remote: "do", Argv: []string{"/bin/cat"}, Dir: t.TempDir()}
	if tool.Name() != "plugin__p-x__do" {
		t.Errorf("name = %q", tool.Name())
	}
	if !tool.ReadOnly() {
		t.Error("plugin tools are read-only (the wiring never constructs mutating ones)")
	}
	if !strings.HasPrefix(tool.Description(), "[plugin p-x]") {
		t.Errorf("description = %q", tool.Description())
	}
}
