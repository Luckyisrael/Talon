package api

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/sandbox"
	"github.com/luckyisrael/talon/backend/internal/mcp"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// registerMCP dials every workspace-declared MCP server (W11 — gated by
// TALONSTUDIO_MCP plus the workspace's .talon/config.json) and registers
// its tools into the ONE registry under the mcp__ namespace: schema
// validation, guardrails, W10 hooks, permission mode, the sandbox ring,
// and the output cap all apply because there is no second execution path.
//
// Failure posture: a server that will not dial or will not list logs with
// its stderr tail and is skipped — MCP is additive capability, one dead
// server never fails a session and never shrinks the native catalog (H3).
// Dialled clients land on the build session so closeMCP can reap them
// (process-group kill for stdio — wrappers' children included).
func registerMCP(reg *tools.Registry, bs *buildSession, workspacePath string) {
	for _, sc := range mcp.Load(workspacePath) {
		client, err := mcp.Dial(context.Background(), sc)
		if err != nil {
			log.Printf("mcp: server %q dial failed, skipped: %v", sc.Name, err)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		remote, err := client.ListTools(ctx)
		cancel()
		if err != nil {
			log.Printf("mcp: server %q tools/list failed, skipped: %v", sc.Name, err)
			_ = client.Close()
			continue
		}
		bs.mcp = append(bs.mcp, client)

		allow := setOf(sc.Allow)
		deny := setOf(sc.Deny)
		mutate := setOf(sc.Mutate)
		for _, rt := range remote {
			if !mcp.ValidName(rt.Name) {
				log.Printf("mcp: server %q tool name %q invalid, skipped", sc.Name, rt.Name)
				continue
			}
			// Policy computes the call-time refusal; the tool stays
			// advertised either way (H3: refusal, not hiding).
			policyReason := ""
			switch {
			case deny[rt.Name]:
				policyReason = "denied by this server's deny list (.talon/config.json)"
			case len(allow) > 0 && !allow[rt.Name]:
				policyReason = "not on this server's allow list (.talon/config.json)"
			}
			reg.Register(&tools.MCPTool{
				Server:     sc.Name,
				Remote:     rt.Name,
				RemoteDesc: rt.Description,
				Schema:     rt.InputSchema,
				Mutating:   mutate[rt.Name],
				Deny:       policyReason,
				Client:     client,
			})
		}
	}
}

// mcpPostureRefusal is W11 item 3's sandbox mapping: a tool the workspace
// explicitly allowed to mutate still cannot mutate under a read-only
// posture of either kind — the session's permission_mode, or the
// read-only sandbox. Read-only-classified MCP tools pass through to the
// normal rings untouched. Runs before the path-targeted sandbox check
// (an MCP act has no workspace path to resolve).
//
// Posture read: permission_mode is per-session (already on bs); the
// sandbox mode is re-read from the same env the session's capability was
// built from — same request, same value.
func (b *buildSession) mcpPostureRefusal(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return ""
	}
	if b.reg.IsReadOnly(name) {
		return "" // read-only default: fine under every posture
	}
	if b.permMode == approval.ModeReadOnly {
		return "MCP: " + name + " is workspace-allowed to mutate, but this session's permission_mode is read-only — remote mutation refused."
	}
	if sandboxModeFromEnv() == sandbox.ModeReadOnly {
		return "MCP: " + name + " is workspace-allowed to mutate, but the sandbox (TALONSTUDIO_SANDBOX) is read-only — remote mutation refused."
	}
	return ""
}

// closeMCP reaps every server dialled for this session: stdio servers die
// by process-group SIGKILL (the wrapper's children included), http servers
// drop idle connections. Idempotent; every newBuildSession owner defers it,
// because the per-request registry philosophy means per-request servers.
func (b *buildSession) closeMCP() {
	for _, c := range b.mcp {
		_ = c.Close()
	}
	b.mcp = nil
}

func setOf(list []string) map[string]bool {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}
