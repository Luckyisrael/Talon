package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// MCPClient is the transport handle one MCP server exposes to its tools.
// Declared here (like PluginFileFetcher) so the tools package stays free of
// the mcp import; the wiring layer bridges the concrete client.
type MCPClient interface {
	CallTool(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error)
}

// MCPTool adapts one remote MCP tool to the local Tool interface so it
// rides the ONE pipeline: schema validation, guardrails, W10 hooks,
// permission mode, the sandbox ring, output caps — all of it applies with
// no second execution path (W11 plan item 1).
//
// Trust posture: Name() is namespaced mcp__<server>__<remote>, so a remote
// can never shadow a native tool; Deny keeps a policy-refused tool
// advertised (H3's catalog-stability law — refusal happens at call time,
// not by hiding) and refuses honestly when invoked; mutation is opt-in via
// Mutating (the workspace's explicit allow) and is additionally gated by
// the wiring layer's posture check before Execute ever runs.
type MCPTool struct {
	Server     string // configured server name (diagnostics, reasons)
	Remote     string // the tool's name on that server
	RemoteDesc string // description as the server declared it
	Schema     json.RawMessage
	Mutating   bool   // workspace-allowed mutation; false = read-only default
	Deny       string // non-empty: refused by workspace policy, still advertised
	Client     MCPClient
}

func (t *MCPTool) Name() string { return "mcp__" + t.Server + "__" + t.Remote }

func (t *MCPTool) Description() string {
	desc := strings.TrimSpace(t.RemoteDesc)
	if desc == "" {
		desc = "remote tool " + t.Remote + " from MCP server " + t.Server
	}
	return "[MCP server " + t.Server + "] " + desc
}

func (t *MCPTool) InputSchema() json.RawMessage { return SchemaOrObject(t.Schema) }

// ReadOnly implements the ReadOnly marker: only the workspace's explicit
// mutate allow promotes a tool out of the read-only default (W11 item 3).
func (t *MCPTool) ReadOnly() bool { return !t.Mutating }

func (t *MCPTool) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	// Policy refusal names itself: the tool stays in the catalog and says
	// exactly why it will not run — the model can tell policy from breakage.
	if t.Deny != "" {
		return ToolResult{
			Content: fmt.Sprintf("%s is denied by this workspace's MCP policy: %s "+
				"(allow/deny in .talon/config.json — the tool stays listed so you stop trying it)",
				t.Name(), t.Deny),
			IsError: true,
		}, nil
	}
	if t.Client == nil {
		return ToolResult{Content: "MCP server " + t.Server + " is not connected in this session.", IsError: true}, nil
	}
	text, isError, err := t.Client.CallTool(ctx, t.Remote, args)
	if err != nil {
		// Transport/protocol failure below the tool layer: reported as a
		// failed call, never as a loop-ending Go error (registry contract).
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return ToolResult{Content: text, IsError: isError}, nil
}

// PluginTool runs one pinned plugin's declared tool (W11 item 2): argv
// executed directly — no shell — with cwd inside the plugin's
// hash-verified cache, the model's JSON arguments on stdin, stdout as the
// result. That is W10's shell-hook contract applied to a tool, and the
// supply-chain boundary the plan names (pins + hashes) is the cache write
// path itself: bytes reach the cache only after sha256-verifying against
// the signed manifest.
//
// The wiring layer only ever constructs read-only plugin tools: a manifest
// declaring mutation is skipped with a log line (no workspace allow path
// exists for plugin mutation — MCP's is workspace config, plugins have no
// equivalent yet), so ReadOnly() reporting true is never a lie.
type PluginTool struct {
	Plugin string
	Remote string
	Desc   string
	Schema json.RawMessage
	Argv   []string // direct exec argv; Argv[0] is a PATH interpreter/binary
	Dir    string   // verified plugin cache dir (cwd for the exec)
}

func (t *PluginTool) Name() string { return "plugin__" + t.Plugin + "__" + t.Remote }

func (t *PluginTool) Description() string {
	desc := strings.TrimSpace(t.Desc)
	if desc == "" {
		desc = "tool " + t.Remote + " from pinned plugin " + t.Plugin
	}
	return "[plugin " + t.Plugin + "] " + desc
}

func (t *PluginTool) InputSchema() json.RawMessage { return SchemaOrObject(t.Schema) }

func (t *PluginTool) ReadOnly() bool { return true }

// pluginOutputCap bounds what one plugin tool run can buffer: the registry
// truncates what reaches the model at 32KB anyway, but memory is claimed
// before that, so the buffer stops growing first.
const pluginOutputCap = 1 << 20

func (t *PluginTool) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if len(t.Argv) == 0 || strings.TrimSpace(t.Argv[0]) == "" {
		return ToolResult{Content: "plugin tool " + t.Name() + " has no command to run (bad manifest).", IsError: true}, nil
	}
	cmd := exec.CommandContext(ctx, t.Argv[0], t.Argv[1:]...)
	cmd.Dir = t.Dir
	cmd.Stdin = bytes.NewReader(args)
	var stdout limitedBuffer
	stdout.Limit = pluginOutputCap
	var stderr limitedBuffer
	stderr.Limit = 16 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return ToolResult{
			Content: fmt.Sprintf("%s cancelled: %v", t.Name(), ctx.Err()),
			IsError: true,
		}, nil
	}
	if err != nil {
		// Non-zero exit is a tool-level failure the model reads — the W10
		// shell contract (exit 0 = success, output; anything else = honest
		// failure with the reason), never a loop-ending Go error.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = "(no stderr)"
		}
		return ToolResult{
			Content: fmt.Sprintf("%s failed: %v\n%s", t.Name(), err, msg),
			IsError: true,
		}, nil
	}
	out := stdout.String()
	if stdout.Truncated {
		out += fmt.Sprintf("\n\n[output truncated at %d KB]", pluginOutputCap/1024)
	}
	return ToolResult{Content: out}, nil
}

// SchemaOrObject guarantees a parseable object schema: wiring passes what
// the manifest/server declared, and anything missing or malformed degrades
// to an empty-object schema — arguments are then admitted as an object
// rather than every call failing validateArgs against garbage.
func SchemaOrObject(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return json.RawMessage(`{"type":"object"}`)
	}
	return trimmed
}

// limitedBuffer keeps at most Limit bytes, noting the cut.
type limitedBuffer struct {
	buf       bytes.Buffer
	Limit     int
	Truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.Limit <= 0 {
		return len(p), nil
	}
	remaining := l.Limit - l.buf.Len()
	if remaining <= 0 {
		l.Truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = l.buf.Write(p[:remaining])
		l.Truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string { return l.buf.String() }
