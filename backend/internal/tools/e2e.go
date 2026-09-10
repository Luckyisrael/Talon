package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunE2E is the DV5 gate: the agent authors Maestro flows under
// .maestro/flows/ and this tool runs them on the session's device.
//
// Maestro is the chosen E2E vocabulary (over Espresso) because flows are
// YAML the agent can write without a build step — the same reason the
// ladder's other gates are scripts, not code. The tool is honest about
// absence: no maestro binary, no adb target, or no flows means skipped,
// never passed — a gate that cannot run cannot claim success.
//
// A flow's own crashes (Maestro "flow failed" with a device exception)
// surface as gate failures with the flow's stderr tail, which lands in
// the crash index through run_e2e's isDeviceStep membership.
type RunE2E struct {
	WorkspacePath string
	ADBTarget     string
	// FlowDir overrides .maestro/flows for tests; empty means the default.
	FlowDir string
	// MaestroPath overrides `maestro` on PATH for tests; empty means PATH.
	MaestroPath string
}

func (t *RunE2E) Name() string { return "run_e2e" }

func (t *RunE2E) Description() string {
	return "Run Maestro end-to-end flows (.maestro/flows/*.yaml) on the connected device. " +
		"Each flow is a YAML script of taps, swipes, and assertions. " +
		"Returns passed=true only when every flow ran and passed; skipped when " +
		"maestro or the device is unavailable — never a fabricated pass."
}

func (t *RunE2E) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"flow": {"type": "string", "description": "a single flow name under .maestro/flows/ (without .yaml); empty runs all"}
		}
	}`)
}

// e2eResult embeds the ladder's shape: one verdict, honest skips.
// A skip is Passed:true + a "skipped" summary — the convention every
// other gate uses (unavailable ≠ failed, and a gate that cannot run
// cannot claim success either).
type e2eResult struct {
	VerifyResult
	Flows []string `json:"flows,omitempty"`
}

func (t *RunE2E) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	// Absence order: device, binary, flows. Each skip is one honest line.
	if t.ADBTarget == "" {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no device attached — e2e skipped"}), nil
	}
	maestroBin := t.MaestroPath
	if maestroBin == "" {
		maestroBin = "maestro"
	}
	if _, err := exec.LookPath(maestroBin); err != nil {
		return marshalVerify(VerifyResult{
			Passed:  true,
			Summary: "maestro is not installed on this host — e2e skipped (install: curl -fsSL 'https://get.maestro.dev' | bash)",
		}), nil
	}

	flowDir := t.FlowDir
	if flowDir == "" {
		flowDir = filepath.Join(t.WorkspacePath, ".maestro", "flows")
	}
	entries, err := os.ReadDir(flowDir)
	if err != nil || len(entries) == 0 {
		return marshalVerify(VerifyResult{Passed: true, Summary: "no .maestro/flows/*.yaml authored yet — e2e skipped"}), nil
	}

	var input struct {
		Flow string `json:"flow"`
	}
	_ = json.Unmarshal(args, &input)

	// Resolve flows: a named flow, or every .yaml in the dir.
	var flows []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if input.Flow == "" || name == input.Flow {
			flows = append(flows, e.Name())
		}
	}
	if len(flows) == 0 {
		if input.Flow != "" {
			return marshalVerify(VerifyResult{Passed: true, Summary: fmt.Sprintf("flow %q not found under .maestro/flows/ — e2e skipped", input.Flow)}), nil
		}
		return marshalVerify(VerifyResult{Passed: true, Summary: "no .maestro/flows/*.yaml authored yet — e2e skipped"}), nil
	}

	var failErrs []string
	var passed []string
	for _, flow := range flows {
		// Maestro talks to the device over adb; the target travels via
		// MAESTRO_ANDROID_ADB_TARGET? Maestro's own env is
		// ANDROID_SERIAL, the standard adb selector.
		cmd := exec.CommandContext(ctx, maestroBin, "test",
			"--format", "simple",
			filepath.Join(flowDir, flow))
		cmd.Env = append(os.Environ(), "ANDROID_SERIAL="+t.ADBTarget)
		cmd.Dir = t.WorkspacePath
		// A generous bound: flows sleep by design (launch animations),
		// but a wedged flow must not hold the run forever. Stop releases
		// the timer when the flow ends on its own.
		kill := time.AfterFunc(3*time.Minute, func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		out, err := cmd.CombinedOutput()
		kill.Stop()
		if err != nil {
			failErrs = append(failErrs, fmt.Sprintf("%s FAILED: %s", flow, tail(string(out), 15)))
			continue
		}
		passed = append(passed, flow)
	}

	res := e2eResult{Flows: passed}
	if len(failErrs) > 0 {
		res.Passed = false
		res.Errors = strings.Join(failErrs, "\n")
		res.Summary = fmt.Sprintf("%d/%d flows passed", len(passed), len(passed)+len(failErrs))
	} else {
		res.Passed = true
		res.Summary = fmt.Sprintf("all %d flows passed", len(passed))
	}
	data, err := json.Marshal(res)
	if err != nil {
		return errResult("could not encode e2e result: %v", err), nil
	}
	return ToolResult{Content: string(data)}, nil
}

// tail keeps the last n lines — the crash evidence, not the log flood.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
