package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/tools"
)

// commandOutputWriter adapts a session emitter into an io.Writer so tools can
// stream their subprocess output to the frontend through the existing event
// channel. Live build output is exactly the signal the developer was blind to
// before: the trace showed build_apk as a row that flipped running→done while
// gradle sat silent for minutes.
type commandOutputWriter struct{ emitter *events.SessionEmitter }

func (w commandOutputWriter) Write(p []byte) (int, error) {
	if w.emitter != nil {
		w.emitter.EmitCommandOutput(string(p))
	}
	return len(p), nil
}

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
// output, when non-nil, is attached to the command-running tools so their
// subprocess output streams back to the frontend while the agent works.
func (h *PromptHandler) newBuildSession(workspacePath string, projectID int64, adbTarget string, output io.Writer) *buildSession {
	fs := tools.NewFS(workspacePath)
	reg := tools.NewRegistry()

	// Reading and searching.
	reg.Register(&tools.ListFiles{FS: fs})
	reg.Register(&tools.ReadFile{FS: fs})
	reg.Register(&tools.GrepCodebase{FS: fs})
	// search_files is a name-alias for grep_codebase (see tools/read.go) so
	// models that reach for the more familiar name get the same implementation
	// without busting the AI provider's tool-schema cache.
	reg.Register(&tools.SearchFiles{Inner: &tools.GrepCodebase{FS: fs}})
	reg.Register(&tools.FindFiles{FS: fs})
	reg.Register(&tools.Explore{FS: fs})
	// Grounding: survey the whole project at once, and read an installed package's
	// real type surface — so the model orients and checks APIs instead of guessing
	// on a stack newer than its training data. Both are read-only (run concurrently).
	reg.Register(&tools.ProjectMap{FS: fs})
	reg.Register(&tools.InspectPackage{WorkspacePath: workspacePath})

	// Writing. edit_file and multi_edit are what make incremental work possible;
	// without them the only way to change one line was to rewrite the file.
	reg.Register(&tools.WriteFile{FS: fs})
	reg.Register(&tools.EditFile{FS: fs})
	reg.Register(&tools.MultiEdit{FS: fs})
	reg.Register(&tools.DeleteFile{FS: fs})

	// Running and verifying. The output sink makes each one's subprocess output
	// visible in the terminal pane instead of arriving only at tool result time.
	reg.Register(&tools.RunCommand{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunShell{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunTypecheck{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunLint{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunChecks{WorkspacePath: workspacePath, Output: output})

	var allowlist *devclient.Allowlist
	if h.devClient != nil {
		allowlist = h.devClient.Allowlist()
	}
	reg.Register(&tools.InstallPackage{WorkspacePath: workspacePath, Allowlist: allowlist, Output: output})
	reg.Register(&tools.BuildAPK{WorkspacePath: workspacePath, Allowlist: allowlist, Output: output})

	// Asking the user. ask_user pauses the turn rather than executing; the
	// orchestrator intercepts it. Registered so the model sees its schema and
	// knows it can ask instead of guessing.
	reg.Register(&tools.AskUser{})

	// Marking plan progress. Like ask_user, mark_task_done is orchestrator-owned:
	// it flips a seeded plan task's status rather than executing. Registered so a
	// capable model can check off screens and features as it finishes them.
	reg.Register(&tools.MarkTaskDone{})

	// Project knowledge.
	adapter := h.storeAdapter()
	reg.Register(tools.NewWriteADR(adapter, projectID))
	reg.Register(tools.NewWriteMemory(adapter, projectID))
	reg.Register(tools.NewRecordFailurePattern(adapter, projectID))
	// read_memory is the symmetric read counterpart — see tools/memory.go.
	// Registered only when the adapter actually has the read hooks wired, so a
	// future "write-only" project variant can register write_memory alone.
	if adapter.GetAllMemoryFn != nil {
		reg.Register(tools.NewReadMemory(adapter, projectID))
	}

	// Skills: registered only when the knowledge layer actually loaded skills.
	// Offering read_skill with an empty library would only ever answer "none".
	if h.knowledge != nil && len(h.knowledge.SkillNames()) > 0 {
		reg.Register(&tools.ReadSkill{Library: h.knowledge})
	}

	// Device feedback. Registered only when a device is actually attached —
	// offering a tool that can only fail wastes rounds.
	if adbTarget != "" {
		// capture_screenshot verifies through the model; give it one only when we
		// have a client, otherwise it degrades to a plain capture.
		var vision tools.VisionChecker
		if h.ai != nil {
			vision = visionAdapter{c: h.ai}
		}
		reg.Register(&tools.CaptureScreenshot{ADBTarget: adbTarget, Vision: vision})
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
	// The adapter's read-side hooks convert the store's `ProjectMemory` shape
	// to the tools-package `MemoryEntry` so the tools layer never imports the
	// store. Doing it here, at the wiring boundary, keeps the contract clean.
	return &tools.StoreAdapter{
		CreateADRFn: func(projectID int64, decision, context, options, outcome string) error {
			_, err := h.store.CreateADR(projectID, decision, context, options, outcome)
			return err
		},
		AddMemoryFn: h.store.AddMemory,
		GetAllMemoryFn: func(projectID int64) ([]tools.MemoryEntry, error) {
			rows, err := h.store.GetAllMemory(projectID)
			if err != nil {
				return nil, err
			}
			return projectMemoryToEntries(rows), nil
		},
		GetMemoryByTypeFn: func(projectID int64, entryType string) ([]tools.MemoryEntry, error) {
			rows, err := h.store.GetMemoryByType(projectID, entryType)
			if err != nil {
				return nil, err
			}
			return projectMemoryToEntries(rows), nil
		},
	}
}

func projectMemoryToEntries(rows []store.ProjectMemory) []tools.MemoryEntry {
	out := make([]tools.MemoryEntry, len(rows))
	for i, r := range rows {
		out[i] = tools.MemoryEntry{
			ID:        r.ID,
			EntryType: r.EntryType,
			Content:   r.Content,
			Metadata:  r.Metadata,
			CreatedAt: r.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		}
	}
	return out
}

// visionAdapter lets *ai.Client satisfy tools.VisionChecker. It exists so the
// tools package can consume vision analysis without importing the ai client and
// the provider machinery behind it — the tool depends on the small interface,
// this maps it onto the concrete result.
type visionAdapter struct{ c *ai.Client }

func (v visionAdapter) VisionCheck(ctx context.Context, screenshot []byte, intendedScreen string) (*tools.VisionVerdict, error) {
	r, err := v.c.VisionCheck(ctx, screenshot, intendedScreen)
	if err != nil {
		return nil, err
	}
	return &tools.VisionVerdict{
		Matches:  r.Matches,
		Reason:   r.Reason,
		HasError: r.HasError,
		ErrorMsg: r.ErrorMsg,
	}, nil
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
