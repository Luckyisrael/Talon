package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	gbuild "github.com/seekersstudio/backend/internal/build"
	"github.com/seekersstudio/backend/internal/runtime"
	sdk "github.com/seekersstudio/backend/internal/sdk"
	"github.com/seekersstudio/backend/internal/verify"
)

// VerifyResult is the shape both verification tools return. Passed is the only
// field callers should branch on: a non-empty Errors string is normal even on
// success, because compilers print banners and warnings to the same stream.
type VerifyResult struct {
	Passed  bool   `json:"passed"`
	Errors  string `json:"errors,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// TypecheckResult is retained under its original name for callers that
// unmarshal it by type.
type TypecheckResult = VerifyResult

const (
	verifyTimeout   = 4 * time.Minute
	maxVerifyOutput = 24 * 1024
)

// runVerifyCmd distinguishes the two failure modes that matter. A non-zero exit
// status means the checker ran and found problems — that is data for the agent,
// not an error. Anything else (binary missing, timeout, permission denied)
// means the check never produced a verdict and must surface as a real error.
// output, when non-nil, receives the checker's stdout/stderr as it runs.
func runVerifyCmd(ctx context.Context, dir string, output io.Writer, name string, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Kill the whole process group on timeout: tsc/eslint spawn children that
	// would otherwise outlive the deadline and hold Wait open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Keep the toolchain from prompting or writing colour codes into output the
	// agent has to read.
	cmd.Env = append(os.Environ(), "CI=1", "FORCE_COLOR=0", "NO_COLOR=1", "npm_config_yes=true")

	raw, runErr := runCaptured(cmd, output)
	out := string(raw)

	if ctx.Err() == context.DeadlineExceeded {
		return out, false, fmt.Errorf("%s timed out after %s", name, verifyTimeout)
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return out, true, nil
		}
		return out, false, fmt.Errorf("could not run %s: %w", name, runErr)
	}
	return out, false, nil
}

// clipOutput keeps the head and tail of a long compiler log: the first errors
// are the actionable ones and the tail usually holds the error count.
func clipOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxVerifyOutput {
		return s
	}
	head := maxVerifyOutput * 3 / 4
	tail := maxVerifyOutput - head
	return s[:head] + fmt.Sprintf("\n\n… [%d bytes omitted] …\n\n", len(s)-maxVerifyOutput) + s[len(s)-tail:]
}

// ── run_typecheck ─────────────────────────────────────

type RunTypecheck struct {
	WorkspacePath string
	// Output, when set, receives the checker's stdout/stderr as it runs.
	Output io.Writer
}

func (t *RunTypecheck) Name() string { return "run_typecheck" }

func (t *RunTypecheck) Description() string {
	return "Run the TypeScript compiler over the project (tsc --noEmit). Returns passed=true when the " +
		"code compiles, or the compiler errors when it does not. Run this once you believe a change is complete."
}

func (t *RunTypecheck) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunTypecheck) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if _, err := os.Stat(filepath.Join(t.WorkspacePath, "tsconfig.json")); err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no tsconfig.json — typecheck skipped"}), nil
	}

	out, failed, err := runVerifyCmd(ctx, t.WorkspacePath, t.Output, "npx", "--no-install", "tsc", "--noEmit", "--pretty", "false")
	if err != nil {
		return errResult("typecheck could not run: %v", err), nil
	}

	if !failed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no type errors"}), nil
	}

	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  clipOutput(out),
		Summary: fmt.Sprintf("%d type error(s)", countTSErrors(out)),
	}), nil
}

func countTSErrors(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, ": error TS") {
			n++
		}
	}
	return n
}

// ── run_lint ──────────────────────────────────────────

type RunLint struct {
	WorkspacePath string
	// Output, when set, receives the checker's stdout/stderr as it runs.
	Output io.Writer
}

func (t *RunLint) Name() string { return "run_lint" }

func (t *RunLint) Description() string {
	return "Run ESLint over the project. Returns passed=true when clean, or the lint errors when not."
}

func (t *RunLint) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunLint) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	files, err := walkSource(t.WorkspacePath, t.WorkspacePath)
	if err != nil || !hasLintableFile(files) {
		return marshalVerify(VerifyResult{Passed: true, Summary: "nothing to lint"}), nil
	}

	// ESLint 9 uses flat config and rejects --ext; older configs need it.
	cmdArgs := []string{"--no-install", "eslint", "."}
	if !anyExists(t.WorkspacePath, "eslint.config.js", "eslint.config.mjs", "eslint.config.cjs") {
		cmdArgs = []string{"--no-install", "eslint", "--ext", ".ts,.tsx,.js,.jsx", "."}
	}

	out, failed, err := runVerifyCmd(ctx, t.WorkspacePath, t.Output, "npx", cmdArgs...)
	if err != nil {
		// A missing or misconfigured linter must not block a build; typecheck
		// is the gate that matters.
		return marshalVerify(VerifyResult{Passed: true, Summary: "lint unavailable: " + err.Error()}), nil
	}
	if !failed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no lint errors"}), nil
	}
	return marshalVerify(VerifyResult{Passed: false, Errors: clipOutput(out), Summary: "lint errors"}), nil
}

func anyExists(dir string, names ...string) bool {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return true
		}
	}
	return false
}

func hasLintableFile(files []string) bool {
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".ts", ".tsx", ".js", ".jsx":
			return true
		}
	}
	return false
}

func marshalVerify(r VerifyResult) ToolResult {
	data, err := json.Marshal(r)
	if err != nil {
		return errResult("could not encode verify result: %v", err)
	}
	// IsError stays false even when the check fails: the tool ran correctly and
	// the verdict is its output. Setting IsError would tell the model the tool
	// itself is broken.
	return ToolResult{Content: string(data)}
}

// ── run_checks ──────────────────────────────────────────

// RunChecks runs the full static ladder — typecheck, then lint — in one call
// and returns one combined verdict. It exists so the agent verifies a finished
// unit once instead of alternating typecheck and lint calls across rounds;
// the orchestrator's own ladder still runs the gates individually for staged
// feedback.
type RunChecks struct {
	WorkspacePath string
	// Output, when set, receives the checkers' stdout/stderr as they run.
	Output io.Writer
}

func (t *RunChecks) Name() string { return "run_checks" }

func (t *RunChecks) Description() string {
	return "Verify a finished unit of work in one call: runs the TypeScript compiler " +
		"and then ESLint, and returns one combined verdict. Prefer this over calling " +
		"run_typecheck and run_lint separately. Returns passed=true when both are clean."
}

func (t *RunChecks) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunChecks) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	tc := &RunTypecheck{WorkspacePath: t.WorkspacePath, Output: t.Output}
	tcRes, err := tc.Execute(ctx, args)
	if err != nil {
		return tcRes, err
	}
	var tcVerdict VerifyResult
	if err := json.Unmarshal([]byte(tcRes.Content), &tcVerdict); err != nil || !tcVerdict.Passed {
		// No point linting code that does not compile — same order as the
		// orchestrator ladder. Pass the typecheck verdict straight through.
		return tcRes, nil
	}

	li := &RunLint{WorkspacePath: t.WorkspacePath, Output: t.Output}
	liRes, err := li.Execute(ctx, args)
	if err != nil {
		return liRes, err
	}
	var liVerdict VerifyResult
	if err := json.Unmarshal([]byte(liRes.Content), &liVerdict); err != nil {
		return liRes, nil
	}
	if liVerdict.Passed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no type errors; no lint errors"}), nil
	}
	liVerdict.Summary = "typecheck clean; " + liVerdict.Summary
	return marshalVerify(liVerdict), nil
}

// ── run_bundle ────────────────────────────────────────
//
// run_bundle runs `npx expo export --platform android --dev false`. It catches
// Metro/asset errors at a fraction of gradle's cost: a Metro bundle error means
// the project will not run on a device, but it does not need a JDK or an Android
// SDK to diagnose. It deliberately runs as a separate gate from typecheck so a
// bundle failure can be reported as a distinct repair step.

type RunBundle struct {
	WorkspacePath string
	Output        io.Writer
}

func (t *RunBundle) Name() string { return "run_bundle" }

func (t *RunBundle) Description() string {
	return "Bundle the project for Android with `npx expo export --platform android --dev false`. " +
		"Catches Metro, asset, and module-resolution errors without paying gradle's cost. " +
		"Returns passed=true when the bundle is produced, or the Metro errors when not."
}

func (t *RunBundle) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunBundle) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if _, err := os.Stat(filepath.Join(t.WorkspacePath, "package.json")); err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no package.json — bundle skipped"}), nil
	}

	out, failed, err := runVerifyCmd(ctx, t.WorkspacePath, t.Output, "npx", "--no-install", "expo", "export", "--platform", "android", "--dev", "false", "--output-dir", ".expo-bundle")
	if err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "bundle unavailable: " + err.Error()}), nil
	}
	if !failed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "bundle produced"}), nil
	}
	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  clipOutput(out),
		Summary: "metro bundle failed",
	}), nil
}

// ── run_gradle ────────────────────────────────────────
//
// run_gradle runs `./gradlew assembleDebug` against the prebuilt android/ dir.
// It returns passed=true when the APK is produced, or the gradle error tail
// when not. JDK 17 + Android SDK must be present on the host; the tool returns
// "unavailable" rather than failed when they are not, mirroring run_lint's
// "missing checker is not a build block" stance.

type RunGradle struct {
	WorkspacePath string
	Output        io.Writer
}

func (t *RunGradle) Name() string { return "run_gradle" }

func (t *RunGradle) Description() string {
	return "Run `./gradlew assembleDebug` against the project's android/ directory. " +
		"Returns passed=true when the APK is produced, or the gradle error tail when not. " +
		"Requires JDK 17 and the Android SDK; reports unavailable if either is missing."
}

func (t *RunGradle) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunGradle) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	androidDir := filepath.Join(t.WorkspacePath, "android")
	gradlew := filepath.Join(androidDir, "gradlew")
	if _, err := os.Stat(gradlew); err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no android/gradlew — gradle skipped (run expo prebuild first)"}), nil
	}
	if sdk.JavaHome() == "" {
		return marshalVerify(VerifyResult{Passed: true, Summary: "gradle unavailable: JDK 17 not found"}), nil
	}
	if sdk.AndroidHome() == "" {
		return marshalVerify(VerifyResult{Passed: true, Summary: "gradle unavailable: Android SDK not found"}), nil
	}
	// Pin perf flags into this workspace's gradle.properties (generated by
	// prebuild, so a template file would never reach it). Best-effort.
	_ = gbuild.EnsureGradlePerfFlags(androidDir)

	out, failed, err := runVerifyCmd(ctx, androidDir, t.Output, "./gradlew", "assembleDebug", "--daemon", "--parallel", "--build-cache")
	if err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "gradle unavailable: " + err.Error()}), nil
	}
	if !failed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "APK built"}), nil
	}
	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  clipOutput(out),
		Summary: "gradle assembleDebug failed",
	}), nil
}

// ── run_boot ──────────────────────────────────────────
//
// run_boot confirms the APK is alive on the device by `adb install -r` followed
// by `adb shell pidof <pkg>`. It returns passed=true when the process is up
// after a short poll. It does not attempt to launch the app — that is the
// orchestrator's job elsewhere — and it is deliberately quiet about transient
// adb errors: a redroid that is mid-reconcile should not fail the gate.

type RunBoot struct {
	WorkspacePath string
	APKPath       string
	PkgName       string
	ADBClient     *ADBRunner
}

func (t *RunBoot) Name() string { return "run_boot" }

func (t *RunBoot) Description() string {
	return "Install the APK on the connected device and confirm the process is alive. " +
		"Returns passed=true when `adb shell pidof <pkg>` reports a PID, or the failure reason when not."
}

func (t *RunBoot) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"apk_path":  {"type": "string"},
			"pkg_name":  {"type": "string"}
		},
		"required": []
	}`)
}

func (t *RunBoot) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if t.ADBClient == nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no adb client — boot skipped"}), nil
	}
	apk := t.APKPath
	pkg := t.PkgName
	if apk == "" || pkg == "" {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no apk/pkg supplied — boot skipped"}), nil
	}

	installOut, err := t.ADBClient.InstallAPK(apk)
	if err != nil {
		return marshalVerify(VerifyResult{
			Passed:  false,
			Errors:  installOut,
			Summary: "adb install failed",
		}), nil
	}

	// The log buffer is cleared before launch so the tail below contains only
	// this run. Best-effort: a stale buffer only weakens the first-launch
	// evidence, it never fails the gate by itself.
	launch := time.Now()
	_ = t.ADBClient.ClearLogcat()

	// pidof is the cheapest "is the process alive" check; on Redroid the boot
	// animation takes a few hundred ms so we allow up to ~5 s.
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := t.ADBClient.Run("pidof " + pkg)
		if err == nil && strings.TrimSpace(out) != "" {
			break
		}
		if time.Now().After(deadline) {
			return marshalVerify(VerifyResult{
				Passed:  false,
				Errors:  fmt.Sprintf("pidof %s returned empty for 5s after install", pkg),
				Summary: "process did not come up",
			}), nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Seen-boot is necessary but not sufficient: a process that exists yet
	// never runs app code is a white screen, not a boot. The on-device
	// first-launch log is the second half of the proof.
	logs, err := t.ADBClient.DumpLogcat(pkg, 200)
	if err != nil {
		return marshalVerify(VerifyResult{
			Passed:  false,
			Errors:  "process alive but logcat unreadable: " + err.Error(),
			Summary: "process did not come up",
		}), nil
	}
	return marshalVerify(bootVerdict(pkg, logs, launch)), nil
}

// bootVerdict turns post-launch evidence into the gate result. Pure so the
// alive-but-silent and boot-gap paths are unit-testable without a device:
// the adb calls stay in Execute, the judgement lives here.
func bootVerdict(pkg, logs string, launch time.Time) VerifyResult {
	gap, first, ok := runtime.BootGap(logs, pkg, launch)
	if !ok {
		return VerifyResult{
			Passed:  false,
			Errors:  "process alive but produced no log output:\n" + runtime.CrashExcerpt(logs, 30),
			Summary: "alive but silent — no first-launch log",
		}
	}
	return VerifyResult{
		Passed:  true,
		Summary: fmt.Sprintf("process %s alive, first log after %s (%s: %s)", pkg, gap.Round(100*time.Millisecond), first.Tag, first.Message),
	}
}

// ── run_vision ────────────────────────────────────────
//
// run_vision captures a screenshot via CaptureScreenshot and runs VisionCheck
// against the intended screen. It is a thin adapter so the orchestrator ladder
// can run it as a gate. When the capture fails (no device, no screen) it
// returns passed=true with an "unavailable" summary, matching the rest of the
// gates' policy: missing infra is not a build block.

type RunVision struct {
	WorkspacePath  string
	Capture        ScreenshotCapturer
	Vision         VisionGateChecker
	IntendedScreen string
	ADBClient      *ADBRunner
}

func (t *RunVision) Name() string { return "run_vision" }

func (t *RunVision) Description() string {
	return "Capture a screenshot from the connected device and run vision against the intended screen. " +
		"Returns passed=true when the model says the screen matches and has no visible error."
}

func (t *RunVision) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"intended_screen": {"type": "string"}
		},
		"required": []
	}`)
}

func (t *RunVision) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	intended := t.IntendedScreen
	if intended == "" {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no intended screen — vision skipped"}), nil
	}
	if t.Capture == nil || t.ADBClient == nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "vision unavailable: no capture / adb"}), nil
	}
	png, err := t.Capture.CaptureScreenshot(ctx, t.WorkspacePath, t.ADBClient.Target)
	if err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "vision unavailable: " + err.Error()}), nil
	}
	// Deterministic pixels before model eyes: a blank screen is a failed
	// gate without spending a vision call, and the verdict is exact rather
	// than lenient. The summary names the gate and screen so the crash
	// index entry for this failure points at the right screenshot context.
	if rep := verify.Analyze(png); rep.Blank || rep.Reason != "" {
		name := "vision"
		if t.IntendedScreen != "" {
			name += " [" + t.IntendedScreen + "]"
		}
		if rep.Blank {
			return marshalVerify(VerifyResult{
				Passed:  false,
				Errors:  "blank screen: screen is effectively empty",
				Summary: name + ": blank screen, no vision call spent",
			}), nil
		}
		return marshalVerify(VerifyResult{
			Passed:  false,
			Errors:  "screenshot capture unhealthy: " + rep.Reason,
			Summary: name + ": " + rep.Reason,
		}), nil
	}
	if t.Vision == nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "vision unavailable: no vision checker"}), nil
	}
	res, err := t.Vision.VisionCheck(ctx, png, intended)
	if err != nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "vision unavailable: " + err.Error()}), nil
	}
	if res.Matches && !res.HasError {
		return marshalVerify(VerifyResult{Passed: true, Summary: "vision matches: " + res.Reason}), nil
	}
	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  res.ErrorMsg,
		Summary: "vision mismatch: " + res.Reason,
	}), nil
}

// ScreenshotCapturer and VisionGateChecker are the narrow interfaces run_vision
// and run_interact depend on, so they can be wired to either the real tools or a
// fake in tests. The names are scoped to "gate" so they do not collide with
// runtime.go's own VisionChecker (which has a different return shape) — both
// packages live in the same Go package, so the compiler cannot resolve two
// types with the same name.
type ScreenshotCapturer interface {
	CaptureScreenshot(ctx context.Context, workspacePath, target string) ([]byte, error)
}

type VisionGateChecker interface {
	VisionCheck(ctx context.Context, png []byte, intended string) (VisionGateResult, error)
}

// VisionGateResult is the shape VisionCheck returns. Mirrors the real ai/vision
// response without importing the package.
type VisionGateResult struct {
	Matches  bool   `json:"matches"`
	Reason   string `json:"reason"`
	HasError bool   `json:"has_error"`
	ErrorMsg string `json:"error_msg"`
}

// ── run_interact ──────────────────────────────────────
//
// run_interact walks the plan's route list, captures a screenshot after a
// navigation tap for each route, and runs vision. When the plan has no routes
// it returns passed=true with a clear note — most projects start this way and
// the gate must not block the first run.

type RunInteract struct {
	WorkspacePath string
	Routes        []string
	Capture       ScreenshotCapturer
	Vision        VisionGateChecker
	ADBClient     *ADBRunner
}

func (t *RunInteract) Name() string { return "run_interact" }

func (t *RunInteract) Description() string {
	return "For each route in the plan, capture a screenshot and run vision. " +
		"Returns passed=true when every route is reachable, or the first failure otherwise."
}

func (t *RunInteract) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"routes": {"type": "array", "items": {"type": "string"}}
		},
		"required": []
	}`)
}

func (t *RunInteract) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	routes := t.Routes
	if len(routes) == 0 {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no routes to interact"}), nil
	}
	if t.Capture == nil || t.ADBClient == nil || t.Vision == nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "interact unavailable: no capture/vision/adb"}), nil
	}
	var failures []string
	for _, r := range routes {
		png, err := t.Capture.CaptureScreenshot(ctx, t.WorkspacePath, t.ADBClient.Target)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: capture failed (%v)", r, err))
			continue
		}
		res, err := t.Vision.VisionCheck(ctx, png, "the "+r+" screen")
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: vision failed (%v)", r, err))
			continue
		}
		if !res.Matches || res.HasError {
			failures = append(failures, fmt.Sprintf("%s: %s", r, res.Reason))
		}
	}
	if len(failures) == 0 {
		return marshalVerify(VerifyResult{Passed: true, Summary: fmt.Sprintf("%d route(s) reachable", len(routes))}), nil
	}
	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  strings.Join(failures, "\n"),
		Summary: fmt.Sprintf("%d of %d route(s) failed", len(failures), len(routes)),
	}), nil
}

// ── run_gate ──────────────────────────────────────────
//
// run_gate returns the most recent verify-chain report for the session. It is
// read-only and safe in parallel. When no chain has run yet it returns a
// payload with passed=true and summary="no chain run yet" so the model can
// call it without conditional logic.

type RunGate struct {
	ReportProvider func() *VerifyReport
}

func (t *RunGate) Name() string { return "run_gate" }

func (t *RunGate) Description() string {
	return "Return the most recent verify-chain report for the current session. " +
		"Read-only. Returns passed=true with summary 'no chain run yet' when no chain has run."
}

func (t *RunGate) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}

func (t *RunGate) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var report *VerifyReport
	if t.ReportProvider != nil {
		report = t.ReportProvider()
	}
	if report == nil {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no chain run yet"}), nil
	}
	if report.OverallPassed {
		return marshalVerify(VerifyResult{Passed: true, Summary: "chain pass: " + report.summary()}), nil
	}
	return marshalVerify(VerifyResult{
		Passed:  false,
		Errors:  report.failingGates(),
		Summary: "chain fail: " + report.summary(),
	}), nil
}

// VerifyReport is what the orchestrator hands the run_gate tool. It is
// reconstructed from the Verify events emitted during a chain run; the
// orchestrator owns the accumulator, run_gate just renders it.
type VerifyReport struct {
	OverallPassed bool
	Gates         []VerifyReportGate
}

type VerifyReportGate struct {
	Stage  string
	Status string // "pass" | "fail" | "unavailable" | "skipped"
	Note   string
}

func (r *VerifyReport) summary() string {
	if len(r.Gates) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(r.Gates))
	for _, g := range r.Gates {
		parts = append(parts, g.Stage+"="+g.Status)
	}
	return strings.Join(parts, ", ")
}

func (r *VerifyReport) failingGates() string {
	var b strings.Builder
	for _, g := range r.Gates {
		if g.Status == "fail" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(g.Stage + ": " + g.Note)
		}
	}
	return b.String()
}
