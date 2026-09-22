package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/devclient"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/sandbox"
	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/harness/workflow"
	"github.com/luckyisrael/talon/backend/internal/metro"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
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
	// seam is the H4 approval seam for this turn: ask policy, the
	// turn-approvals answerer, the session-log audit. Tier-2 shell
	// escalations ask through it (RunShell's Sandbox + Approval + PromptID);
	// the device-build gate reads the same table. A nil log still yields a
	// working seam — record() degrades to a no-op, so audit loss never fails
	// a build.
	seam *approval.Seam
	// children is the H9 delegation service bound to this session: delegate
	// spawns scoped child loops, delegate_fork seeds one from this session's
	// history, list_children reports them. Nil (default) keeps the tools
	// unregistered — the flag TALONSTUDIO_SUBAGENTS=1 opts a deployment in.
	children *subagent.Service
	// projectID + store let the executor keep the device session alive:
	// device-scoped tool calls touch the live session clock so the reaper
	// does not pause the container mid-build (a 15-minute gradle run is
	// one tool call — without touches the session looks idle and dies).
	// promptID attributes build receipts to the owning turn.
	projectID int64
	promptID  int64
	store     *store.Store
}

// deviceTools need a live container; invoking one is activity that must
// move the reaper clock. Everything else (reads, edits, shell) is
// host-side work the reaper correctly ignores.
var deviceTools = map[string]bool{
	"build_apk": true, "run_gradle": true, "run_boot": true,
	"run_vision": true, "run_interact": true, "capture_screenshot": true,
	"tail_logcat": true, "test_wallet_roundtrip": true, "run_e2e": true,
}

// newBuildSession wires the agent's tools to one project's workspace.
// output, when non-nil, is attached to the command-running tools so their
// subprocess output streams back to the frontend while the agent works.
func (h *PromptHandler) newBuildSession(workspacePath string, projectID, promptID int64, adbTarget string, output io.Writer) *buildSession {
	fs := tools.NewFS(workspacePath)
	reg := tools.NewRegistry()

	// H4: the confinement capability for this session — workspace-write by
	// default (writes inside the project dir; the platform temp roots are
	// scratch). The enforcement hook is trivial-allow because the outer
	// ring's job is path-shape confinement; the per-tool registry checks
	// (tier classification, secret patterns, timeouts) remain the inner ring.
	// TALONSTUDIO_SANDBOX=read-only selects a read-only session (plan-style
	// turns that must not mutate), danger-full-access is not selectable from
	// the environment — only a composition change can grant it.
	bs := &buildSession{fs: fs, reg: reg, projectID: projectID, promptID: promptID, store: h.store}
	bs.sandbox = sandbox.New(sandboxModeFromEnv(), workspacePath,
		[]string{os.TempDir()}, func(op sandbox.Operation) error { return nil })

	// H4: the approval seam for this turn — ask policy, the turn-approvals
	// answerer (escalation requests read tier2_shell, device build reads
	// device_build), every decision audited to the project's session log.
	// The log resolves through the handler cache when present and opens
	// directly when not (bare handlers); a nil log still builds a seam.
	bs.seam = h.buildApprovalSeam(h.projectLog(projectID), projectID, promptID)

	// H9: opt-in delegation. The child runner is a second orchestrator loop
	// over this same workspace with a read-scoped registry: children inspect
	// and report; they never mutate (a review child must not be able to
	// edit). Pool 2 is the rate-limit-aware default.
	if subagentsEnabled() {
		bs.children = subagent.NewService(childRunnerFor(h, workspacePath, projectID, adbTarget, output), 2)
	}

	// Reading and searching. read_file also resolves spill locators (W1.2):
	// the loop's filed oversized outputs come back through the same tool the
	// model already knows, gated by the spill store's own containment check.
	reg.Register(&tools.ListFiles{FS: fs})
	reg.Register(&tools.ReadFile{FS: fs, Spill: h.spill})
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
	// run_shell: tier-2 commands escalate through this turn's approval seam
	// (H4) — one strictly-wider ask, audited, the turn's grant the binding
	// record. Nil seam fields fall back to the ask_user refusal.
	reg.Register(&tools.RunShell{
		WorkspacePath: workspacePath, Output: output,
		Sandbox: bs.sandbox, Approval: bs.seam, PromptID: promptID,
	})
	reg.Register(&tools.RunTypecheck{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunLint{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunPrettier{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunChecks{WorkspacePath: workspacePath, Output: output})
	// The G4 static gates the orchestrator's verify ladder walks. run_bundle
	// and run_gradle degrade to honest "unavailable" verdicts on hosts
	// without the JS/Android toolchain — registering them unconditionally
	// keeps the ladder's rungs intact instead of the model discovering
	// "No tool named run_bundle" mid-build.
	reg.Register(&tools.RunBundle{WorkspacePath: workspacePath, Output: output})
	reg.Register(&tools.RunGradle{WorkspacePath: workspacePath, Output: output})

	var allowlist *devclient.Allowlist
	if h.devClient != nil {
		allowlist = h.devClient.Allowlist()
	}
	reg.Register(&tools.InstallPackage{WorkspacePath: workspacePath, TemplateDir: h.templateDir, Allowlist: allowlist, Output: output})
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

	// run_gate reports the session's latest verify-chain state.
	// Orchestrator-owned like ask_user/try_edit: the provider reads the
	// loop's accumulator directly, so the model can ask "what's green,
	// what's red" without waiting for the ladder's next cadence. Defined
	// and unit-tested since G4 but never registered — the model's request
	// always resolved to "No tool named run_gate" (W1.4). Nil-safe for
	// handlers constructed without an orchestrator (the testinfo shape).
	reg.Register(&tools.RunGate{ReportProvider: func() *tools.VerifyReport {
		if h.orch == nil {
			return nil
		}
		return h.orch.LastVerifyReport()
	}})

	// Git-native workflow (G1-G2): the workspace is a real repo from provision.
	// These tools let the agent work standard — status/diff/log/commit/branch
	// without shell-escaping or guessing git invocations.
	reg.Register(&tools.GitStatus{WorkspacePath: workspacePath})
	reg.Register(&tools.GitDiff{WorkspacePath: workspacePath})
	reg.Register(&tools.GitLog{WorkspacePath: workspacePath})
	reg.Register(&tools.GitCommit{WorkspacePath: workspacePath})
	reg.Register(&tools.GitBranch{WorkspacePath: workspacePath})
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
	// With a plugin registry attached, the per-workspace snapshot layers
	// pinned plugin skills under the base catalog (base wins clashes), so
	// the model sees exactly this project's plugins and loads one at a time.
	var skillLib tools.SkillLibrary
	if snap := h.pluginSkillLibrary(workspacePath); snap != nil {
		skillLib = snap
	} else if h.skillCatalog != nil {
		skillLib = catalogLibrary{c: h.skillCatalog}
	} else if h.knowledge != nil && len(h.knowledge.SkillNames()) > 0 {
		skillLib = h.knowledge
	}
	if skillLib != nil {
		reg.Register(&tools.ReadSkill{Library: skillLib})
	}
	// Plugin reference files: one verified starting point at a time, only
	// from plugins pinned for this workspace.
	if pf := h.pluginFileTool(workspacePath); pf != nil {
		reg.Register(pf)
	}

	// H9 delegation: scoped children on the same loop. Registered only
	// when the deployment opted in — delegation multiplies context and cost,
	// so it ships dark until a composition turns it on. Depth 1 is enforced
	// twice: the service seam refuses grandchildren, and delegate /
	// delegate_fork are never registered in a child's own registry.
	if bs.children != nil {
		reg.Register(&tools.Delegate{Service: bs.children, LabelPrefix: fmt.Sprintf("project-%d", projectID)})
		reg.Register(&tools.DelegateFork{Service: bs.children, LabelPrefix: fmt.Sprintf("project-%d", projectID)})
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
		// DV5: the E2E gate. Maestro flows under .maestro/flows/, executed
		// against this device; honest skip when maestro is not installed.
		reg.Register(&tools.RunE2E{WorkspacePath: workspacePath, ADBTarget: adbTarget})
		// DV1: the bundler's voice. The buffer is pumped from the session's
		// metro tap for the session's lifetime; the agent reads the tail.
		if buf := h.metroBufferFor(projectID); buf != nil {
			reg.Register(&tools.MetroStatus{Source: buf})
		}

		// G4 device gates. The verify ladder walks run_boot, run_vision,
		// and run_interact; before this block they resolved to "No tool
		// named …" and the ladder limped. Each degrades honestly when its
		// inputs are missing (no built APK, no capture path) — registered
		// whenever a device exists so the rungs are never absent.
		adbClient := &tools.ADBRunner{Target: adbTarget}
		// Metro port for bundle deep links: resolved live (a restarted
		// server kills Metro processes, so a stored port would be stale).
		// Zero when no bundler runs — launches proceed without the URL.
		if live, err := h.store.LatestLiveSession(projectID); err == nil && live != nil {
			if port, ok := metro.PortForSession(live.ID); ok {
				adbClient.MetroPort = port
			}
		}
		// boot installs the project's APK and polls pidof. The package name
		// is the project's identity; the APK path is build_apk's standard
		// output, which exists only after a build — until then the gate
		// reports its honest "no apk/pkg supplied — boot skipped".
		pkgName := ""
		apkPath := ""
		if proj, err := h.store.GetProject(projectID); err == nil {
			pkgName = proj.AndroidPackage
		}
		if st, err := os.Stat(filepath.Join(workspacePath, "android", "app", "build", "outputs", "apk", "debug", "app-debug.apk")); err == nil && !st.IsDir() {
			apkPath = filepath.Join(workspacePath, "android", "app", "build", "outputs", "apk", "debug", "app-debug.apk")
		}
		reg.Register(&tools.RunBoot{WorkspacePath: workspacePath, APKPath: apkPath, PkgName: pkgName, ADBClient: adbClient})
		// vision and interact verify through the model's eyes; wire them
		// only when a client exists (a plain capture otherwise is not a
		// vision verdict, and the gates would claim verification they
		// never performed).
		if h.ai != nil {
			gate := &gateVision{c: h.ai, target: adbTarget}
			reg.Register(&tools.RunVision{WorkspacePath: workspacePath, Capture: gate, Vision: gate, ADBClient: adbClient})
			reg.Register(&tools.RunInteract{WorkspacePath: workspacePath, Capture: gate, Vision: gate, ADBClient: adbClient})
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

	// Return the session the fields were accumulated on, not a fresh
	// literal: rebuilding here silently dropped sandbox, promptID, seam,
	// and children — the H4 outer ring never ran, receipts attributed to
	// prompt 0, and the approval seam never reached the session (W1.6 found
	// this; the tool registrations carried their own copies, which is why
	// tier-2 worked while the session stayed nil).
	bs.defs = defs
	return bs
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
		SearchMemoryFn: func(projectID int64, query, entryType string, limit int) ([]tools.MemoryEntry, error) {
			rows, err := h.store.SearchMemoryFTS(projectID, query, entryType, limit)
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

// gateVision wires the G4 device gates (run_vision, run_interact) to the real
// things: the screenshot comes off the device over adb screencap, the verdict
// from the model. One adapter satisfies both narrow gate interfaces so the
// gates never import the ai package.
type gateVision struct {
	c      *ai.Client
	target string
}

// CaptureScreenshot satisfies tools.ScreenshotCapturer — the same adb
// exec-out screencap the capture_screenshot tool uses.
func (g gateVision) CaptureScreenshot(ctx context.Context, _, _ string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "adb", "-s", g.target, "exec-out", "screencap", "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("screencap failed: %v, output: %s", err, string(out))
	}
	return out, nil
}

// VisionCheck satisfies tools.VisionGateChecker — the model's eyes, mapped
// onto the gate's verdict shape.
func (g gateVision) VisionCheck(ctx context.Context, png []byte, intended string) (tools.VisionGateResult, error) {
	r, err := g.c.VisionCheck(ctx, png, intended)
	if err != nil {
		return tools.VisionGateResult{}, err
	}
	return tools.VisionGateResult{
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
		// Device-scoped calls keep the live session clock moving so a long
		// device flow (gradle, boot, vision) is never mistaken for idleness
		// and reaped mid-turn. Best-effort: a failed touch must not fail
		// the tool call it accompanies.
		if deviceTools[name] && b.store != nil {
			if live, err := b.store.LatestLiveSession(b.projectID); err == nil && live != nil {
				_ = b.store.TouchSession(live.ID)
			}
		}
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
		// Build receipts: every build_apk completion is filed durably with
		// the owning turn attached, whether that turn is still alive or not.
		// The live turn reads the inline result as today; a turn that died
		// mid-build (cancel, restart, timeout) leaves a receipt the next
		// turn picks up instead of rebuilding blind. Best-effort — a failed
		// write must not rewrite the tool's own verdict.
		if name == "build_apk" && b.store != nil && b.promptID != 0 {
			b.recordBuildReceipt(result, err)
		}
		return result.Content, result.IsError
	}
}

// recordBuildReceipt files one finished build_apk run: status, artifact,
// one-line summary. Child sessions (promptID 0, read-only registries)
// never reach here — build_apk is not in their toolset.
func (b *buildSession) recordBuildReceipt(result tools.ToolResult, err error) {
	status := "built"
	if err != nil || result.IsError {
		status = "failed"
	}
	apkPath := ""
	if status == "built" {
		for _, line := range strings.Split(result.Content, "\n") {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "APK built successfully:"); ok {
				apkPath = strings.TrimSpace(rest)
				break
			}
		}
	}
	summary := result.Content
	if len(summary) > 500 {
		summary = summary[:500] + "…"
	}
	if summary == "" && err != nil {
		summary = err.Error()
	}
	if rerr := b.store.RecordBuildReceipt(b.projectID, b.promptID, status, apkPath, summary); rerr != nil {
		log.Printf("build receipt for prompt %d not recorded: %v", b.promptID, rerr)
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

// sandboxModeFromEnv reads TALONSTUDIO_SANDBOX: "read-only" selects the
// non-mutating mode; the default (anything else) is workspace-write. Danger
// mode is deliberately not reachable from the environment — only a
// composition change can grant it, and that change is auditable in code
// review, not in a runtime flag.
func sandboxModeFromEnv() sandbox.Mode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_SANDBOX"))) {
	case "read-only", "ro", "readonly":
		return sandbox.ModeReadOnly
	}
	return sandbox.ModeWorkspace
}

// subagentsEnabled reads TALONSTUDIO_SUBAGENTS: delegation ships dark (off
// by default) — it multiplies context and cost, so a deployment opts in.
func subagentsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_SUBAGENTS"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// childRunnerFor assembles the H9 in-process runner: the child gets a
// read-only build session over the same workspace (inspect and report;
// never mutate), the orchestrator's loop, and the session handler's prompts.
// A fork child additionally seeds from the parent's live conversation and
// forks the project's session log (W1.5).
func childRunnerFor(h *PromptHandler, workspacePath string, projectID int64, adbTarget string, output io.Writer) subagent.Runner {
	eventLog := h.slog.get(h.store, projectID)
	return &childRunner{
		build: func() *buildSession {
			// A child's registry: the read tools only. bs.defs is rebuilt
			// because newBuildSession would register the full set including
			// delegate (depth-1 wall #2: the child cannot see the tool).
			child := h.newBuildSession(workspacePath, projectID, 0, adbTarget, output)
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
		parentTools:  nil,
		// W1.5: a fork child seeds from the parent's conversation — the same
		// history the parent's next turn replays — resolved per spawn so a
		// later fork carries the turns that happened since the last one.
		forkHistory: func() []ai.Message { return h.conversation(projectID) },
		// The session-log fork: child log = parent prefix + lineage + the
		// child's own turn (nil log = no fork, never a failed run).
		log: eventLog,
		st:  h.store,
		runLoop: func(ctx context.Context, sess *orchestrator.SessionState, bs *buildSession,
			defs []ai.ToolDefinition, history []ai.Message, system string) (*orchestrator.BuildOutcome, error) {
			return h.orch.RunAgentLoop(ctx, sess, orchestrator.RunOptions{
				SystemPrompt: system,
				Mode:         "build",
				History:      history,
				Tools:        defs,
				Execute:      bs.executor(),
				ReadOnlyTool: bs.reg.IsReadOnly,
				FilesTouched: bs.fs.Touched,
				Exists:       bs.fs.Exists,
				// A subagent's retries belong to the same project log: the parent
				// turn's replay should see that a delegated call recovered too.
				EventLog: eventLog,
			})
		},
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
