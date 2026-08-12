package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/tools"
)

// buildSession is everything one build turn needs: the tools it can call, the
// file layer those tools write through, and the definitions the model sees.
//
// A session is constructed per request. The previous design mutated a registry
// hanging off the shared handler, which meant two concurrent builds pointed each
// other's tools at the wrong workspace.
type buildSession struct {
	reg  *tools.Registry
	fs   *tools.FS
	defs []ai.ToolDefinition
}

// newBuildSession wires the agent's tools to one project's workspace.
func (h *PromptHandler) newBuildSession(workspacePath string, projectID int64, adbTarget string) *buildSession {
	fs := tools.NewFS(workspacePath)
	reg := tools.NewRegistry()

	// Reading and searching.
	reg.Register(&tools.ListFiles{FS: fs})
	reg.Register(&tools.ReadFile{FS: fs})
	reg.Register(&tools.GrepCodebase{FS: fs})

	// Writing. edit_file and multi_edit are what make incremental work possible;
	// without them the only way to change one line was to rewrite the file.
	reg.Register(&tools.WriteFile{FS: fs})
	reg.Register(&tools.EditFile{FS: fs})
	reg.Register(&tools.MultiEdit{FS: fs})
	reg.Register(&tools.DeleteFile{FS: fs})

	// Running and verifying.
	reg.Register(&tools.RunCommand{WorkspacePath: workspacePath})
	reg.Register(&tools.RunTypecheck{WorkspacePath: workspacePath})
	reg.Register(&tools.RunLint{WorkspacePath: workspacePath})

	var allowlist *devclient.Allowlist
	if h.devClient != nil {
		allowlist = h.devClient.Allowlist()
	}
	reg.Register(&tools.InstallPackage{WorkspacePath: workspacePath, Allowlist: allowlist})
	reg.Register(&tools.BuildAPK{WorkspacePath: workspacePath, Allowlist: allowlist})

	// Project knowledge.
	adapter := h.storeAdapter()
	reg.Register(tools.NewWriteADR(adapter, projectID))
	reg.Register(tools.NewWriteMemory(adapter, projectID))

	// Device feedback. Registered only when a device is actually attached —
	// offering a tool that can only fail wastes rounds.
	if adbTarget != "" {
		reg.Register(&tools.CaptureScreenshot{ADBTarget: adbTarget})
		reg.Register(&tools.TailLogcat{ADBTarget: adbTarget})
		reg.Register(&tools.TestWalletRoundtrip{ADBTarget: adbTarget})
	}

	// Research. Both back onto public endpoints, so there is nothing to
	// configure; they degrade to "no results" rather than failing the turn.
	reg.Register(&tools.WebSearch{HTTPClient: &http.Client{Timeout: 20 * time.Second}})
	reg.Register(&tools.GitHubSearch{HTTPClient: &http.Client{Timeout: 20 * time.Second}})

	defs := make([]ai.ToolDefinition, 0, len(reg.List()))
	for _, d := range reg.List() {
		defs = append(defs, ai.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}

	return &buildSession{reg: reg, fs: fs, defs: defs}
}

func (h *PromptHandler) storeAdapter() *tools.StoreAdapter {
	return &tools.StoreAdapter{
		CreateADRFn: func(projectID int64, decision, context, options, outcome string) error {
			_, err := h.store.CreateADR(projectID, decision, context, options, outcome)
			return err
		},
		AddMemoryFn: h.store.AddMemory,
	}
}

// executor adapts the registry to the orchestrator's ToolExecutor. A tool
// failure is reported as is_error on the tool result, never as a Go error: the
// agent is supposed to read the failure and react to it, not have the loop
// abort underneath it.
func (b *buildSession) executor() orchestrator.ToolExecutor {
	return func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		result, err := b.reg.Execute(ctx, name, input)
		if err != nil && result.Content == "" {
			return fmt.Sprintf("%s failed: %v", name, err), true
		}
		return result.Content, result.IsError
	}
}

// adbTargetFor finds the device attached to a project, if any.
func (h *PromptHandler) adbTargetFor(projectID int64) string {
	sessions, err := h.store.GetActiveSessions()
	if err != nil {
		return ""
	}
	for _, s := range sessions {
		if s.ProjectID == projectID && s.ADBPort > 0 {
			return fmt.Sprintf("localhost:%d", s.ADBPort)
		}
	}
	return ""
}
