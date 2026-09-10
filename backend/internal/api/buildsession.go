package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/harness/sandbox"
	"github.com/seekersstudio/backend/internal/harness/subagent"
	"github.com/seekersstudio/backend/internal/harness/workflow"
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
	// sandbox is the H4 confinement capability over the workspace. Nil means
	// the composition did not provide one — the executor then runs without
	// the extra pass, exactly today's behavior (the per-tool checks inside
	// the registry still apply; the capability is the uniform outer ring).
	sandbox *sandbox.Capability
	// children is the H9 delegation service bound to this session: delegate
	// spawns scoped child loops; list_children reports them. Nil (default)
	// keeps the tools unregistered — the flag SEEKERSTUDIO_SUBAGENTS=1 opts
	// a deployment in.
	children *subagent.Service
}

// newBuildSession wires the agent's tools to one project's workspace.
// output, when non-nil, is attached to the command-running tools so their
// subprocess output streams back to the frontend while the agent works.
func (h *PromptHandler) newBuildSession(workspacePath string, projectID int64, adbTarget string, output io.Writer) *buildSession {
	fs := tools.NewFS(workspacePath)
	reg := tools.NewRegistry()

	// H4: the confinement capability for this session — workspace-write by
	// default (writes inside the project dir; the platform temp roots are
	// scratch). The enforcement hook is trivial-allow because the outer
	// ring's job is path-shape confinement; the per-tool registry checks
	// (tier classification, secret patterns, timeouts) remain the inner ring.
	// SEEKERSTUDIO_SANDBOX=read-only selects a read-only session (plan-style
	// turns that must not mutate), danger-full-access is not selectable from
	// the environment — only a composition change can grant it.
	bs := &buildSession{fs: fs, reg: reg}
	bs.sandbox = sandbox.New(sandboxModeFromEnv(), workspacePath,
		[]string{os.TempDir()}, func(op sandbox.Operation) error { return nil })

	// H9: opt-in delegation. The child runner is a second orchestrator loop
	// over this same workspace with a read-scoped registry: children inspect
	// and report; they never mutate (a review child must not be able to
	// edit). Pool 2 is the rate-limit-aware default.
	if subagentsEnabled() {
		bs.children = subagent.NewService(childRunnerFor(h, workspacePath, projectID, adbTarget, output), 2)
	}

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

	// Sandboxed edits. Like mark_task_done, try_edit/keep_edit are
	// orchestrator-owned (the loop owns the patch buffer and the verify
	// path); registered so the model sees their schemas.
	reg.Register(&tools.TryEdit{})
	reg.Register(&tools.KeepEdit{})
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
	// H6: when the multi-source catalog is attached, read_skill resolves
	// through it — workspace skills load on demand exactly like embedded ones.
	var skillLib tools.SkillLibrary
	if h.skillCatalog != nil {
		skillLib = catalogLibrary{c: h.skillCatalog}
	} else if h.knowledge != nil && len(h.knowledge.SkillNames()) > 0 {
		skillLib = h.knowledge
	}
	if skillLib != nil {
		reg.Register(&tools.ReadSkill{Library: skillLib})
	}

	// H9 delegation: scoped children on the same loop. Registered only
	// when the deployment opted in — delegation multiplies context and cost,
	// so it ships dark until a composition turns it on. Depth 1 is enforced
	// twice: the service seam refuses grandchildren, and delegate is never
	// registered in a child's own registry.
	if bs.children != nil {
		reg.Register(&tools.Delegate{Service: bs.children, LabelPrefix: fmt.Sprintf("project-%d", projectID)})
		reg.Register(&tools.ListChildren{Service: bs.children})
		// H10: the declarative fan-out rides the same service — the engine
		// narrates each phase into the trace so the developer sees progress
		// live, DSH-style.
		engine := workflow.NewEngine(bs.children, func(text string) {
			if output != nil {
				if w, ok := output.(interface{ WriteString(string) (int, error) }); ok {
					_, _ = w.WriteString(text + "\n")
				}
			}
		})
		reg.Register(&tools.RunWorkflow{Engine: engine})
	}

	// Device feedback. Registered only when a device is actually attached —
	// offering a tool that can only fail wastes rounds.
	if adbTarget != "" {
		// DV4: an agent turn against the device is activity. The reaper
		// must not pause a container the agent is actively working on.
		if live, err := h.store.LatestLiveSession(projectID); err == nil && live != nil {
			h.store.TouchSession(live.ID)
		}
		// capture_screenshot verifies through the model; give it one only when we
		// have a client, otherwise it degrades to a plain capture.
		var vision tools.VisionChecker
		if h.ai != nil {
			vision = visionAdapter{c: h.ai}
		}
		reg.Register(&tools.CaptureScreenshot{ADBTarget: adbTarget, Vision: vision})
		reg.Register(&tools.TailLogcat{ADBTarget: adbTarget})
		reg.Register(&tools.TestWalletRoundtrip{ADBTarget: adbTarget})
		// DV1: the bundler's voice. The buffer is pumped from the session's
		// metro tap for the session's lifetime; the agent reads the tail.
		if buf := h.metroBufferFor(projectID); buf != nil {
			reg.Register(&tools.MetroStatus{Source: buf})
		}
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
		// H4 uniform outer ring: when the sandbox capability is present, the
		// mutating tools' target paths are checked against the confinement
		// mode before the tool runs. The per-tool checks inside the registry
		// stay — defense in depth, not replacement; this pass is what makes
		// confinement uniform rather than per-tool bespoke.
		if b.sandbox != nil {
			if refused := sandboxRefusalFor(b.sandbox, name, input); refused != "" {
				return refused, true
			}
		}
		result, err := b.reg.Execute(ctx, name, input)
		if err != nil && result.Content == "" {
			return fmt.Sprintf("%s failed: %v", name, err), true
		}
		return result.Content, result.IsError
	}
}

// metroBuffers holds one bundler-state buffer per project (DV1). The
// bundler outlives turns; its tap must too. Guarded by metroMu because
// concurrent turns (and the child-runner path) resolve the same project.
var (
	metroMu     sync.Mutex
	metroBufs   = map[int64]*tools.Buffer{}
	metroPumped = map[int64]bool{}
)

// metroBufferFor returns the project's bundler-state buffer, creating and
// starting its pump when a live device session exists. Nil when no device
// is attached — the tool is then not registered (offering a tool that can
// only say "nothing" wastes the agent's round).
func (h *PromptHandler) metroBufferFor(projectID int64) *tools.Buffer {
	session, err := h.store.LatestLiveSession(projectID)
	if err != nil || session == nil {
		return nil
	}
	tap, unsubscribe := metro.SubscribeSession(session.ID)
	if tap == nil {
		return nil
	}

	metroMu.Lock()
	defer metroMu.Unlock()
	buf, ok := metroBufs[projectID]
	if !ok {
		buf = tools.NewMetroBuffer(32)
		metroBufs[projectID] = buf
	}
	buf.SetLive(true)
	if !metroPumped[projectID] {
		metroPumped[projectID] = true
		go func(ch chan metro.Event, stop func()) {
			// The pump runs until the channel closes (bundler stopped) or
			// the events channel is replaced — drop-oldest on the buffer
			// keeps a slow agent from ever stalling the bundler's stdout.
			for ev := range ch {
				buf.Push(ev.Kind, ev.Text, ev.Progress)
			}
			buf.SetLive(false)
			stop()
		}(tap, unsubscribe)
	} else {
		unsubscribe() // an existing pump owns this project's tap
	}
	return buf
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

// sandboxRefusalFor extracts the mutating tools' target path and checks it
// against the capability. Returns the refusal text, or "" when the call is
// allowed (or the tool is not a path-targeted mutation).
func sandboxRefusalFor(c *sandbox.Capability, name string, input json.RawMessage) string {
	path := mutatingToolPath(name, input)
	if path == "" {
		return ""
	}
	abs, err := c.ResolveWorkspace(path)
	if err != nil {
		return "SANDBOX: " + err.Error() + " — use a workspace-relative path."
	}
	if d := c.AllowsPath("write", abs); !d.Allowed {
		return "SANDBOX: " + d.Reason
	}
	return ""
}

// mutatingToolPath pulls the target path from the mutating tools' inputs.
// Read-only and command tools return "" — they are not path-targeted writes.
func mutatingToolPath(name string, input json.RawMessage) string {
	switch name {
	case "write_file", "edit_file", "multi_edit", "delete_file":
	default:
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(input, &a) != nil || a.Path == "" {
		// Malformed input: let the tool itself produce the honest error.
		return ""
	}
	return a.Path
}

// sandboxModeFromEnv reads SEEKERSTUDIO_SANDBOX: "read-only" selects the
// non-mutating mode; the default (anything else) is workspace-write. Danger
// mode is deliberately not reachable from the environment — only a
// composition change can grant it, and that change is auditable in code
// review, not in a runtime flag.
func sandboxModeFromEnv() sandbox.Mode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SEEKERSTUDIO_SANDBOX"))) {
	case "read-only", "ro", "readonly":
		return sandbox.ModeReadOnly
	}
	return sandbox.ModeWorkspace
}

// subagentsEnabled reads SEEKERSTUDIO_SUBAGENTS: delegation ships dark (off
// by default) — it multiplies context and cost, so a deployment opts in.
func subagentsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SEEKERSTUDIO_SUBAGENTS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// childRunnerFor assembles the H9 in-process runner: the child gets a
// read-only build session over the same workspace (inspect and report;
// never mutate), the orchestrator's loop, and the session handler's prompts.
func childRunnerFor(h *PromptHandler, workspacePath string, projectID int64, adbTarget string, output io.Writer) subagent.Runner {
	return &childRunner{
		build: func() *buildSession {
			// A child's registry: the read tools only. bs.defs is rebuilt
			// because newBuildSession would register the full set including
			// delegate (depth-1 wall #2: the child cannot see the tool).
			child := h.newBuildSession(workspacePath, projectID, adbTarget, output)
			readOnly := child.reg
			readDefs := make([]ai.ToolDefinition, 0, len(child.defs))
			for _, d := range child.defs {
				if readOnly.IsReadOnly(d.Name) {
					readDefs = append(readDefs, d)
				}
			}
			child.defs = readDefs
			child.children = nil
			return child
		},
		newSession: func() *orchestrator.SessionState {
			// A real emitter: the loop emits phase marks and tool calls from
			// round one; a nil emitter panics on the first Emit. The channel
			// is drained so the child loop never blocks on a full buffer.
			sess := orchestrator.NewSession(0, projectID, workspacePath, events.NewSessionEmitter(0, 64))
			go drain(sess)
			return sess
		},
		systemPrompt: h.systemPrompt(),
		orchestrator: h.orch,
		parentTools:  nil,
	}
}

// drain consumes a session's event stream so emitters never block their
// loops on a full channel. The events channel is never closed by design —
// the drain selects on the emitter's Done so the goroutine exits at Close,
// rather than a bare for-range that would block forever (the documented
// contract in events.go).
func drain(sess *orchestrator.SessionState) {
	if sess == nil || sess.Emitter == nil {
		return
	}
	ch := sess.Emitter.Events()
	done := sess.Emitter.Done()
	for {
		select {
		case <-ch:
		case <-done:
			// Drain anything still buffered, then exit.
			for {
				select {
				case <-ch:
				default:
					return
				}
			}
		}
	}
}
