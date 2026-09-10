package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runE2E(t *testing.T, tool *RunE2E) e2eOut {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool errored: %s", res.Content)
	}
	var out e2eOut
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("decode %q: %v", res.Content, err)
	}
	return out
}

// e2eOut mirrors the JSON the tool emits (verified, not trusted).
type e2eOut struct {
	Passed  bool     `json:"passed"`
	Summary string   `json:"summary"`
	Errors  string   `json:"errors,omitempty"`
	Flows   []string `json:"flows,omitempty"`
}

// Every unavailable state is an honest skip: passed=true (unavailable ≠
// failed) with a summary that says skipped — the ladder's convention.
// A gate that cannot run cannot claim success, and cannot claim failure.
func TestRunE2EUnavailableStates(t *testing.T) {
	ws := t.TempDir()

	// No device.
	out := runE2E(t, &RunE2E{WorkspacePath: ws})
	if !out.Passed || !strings.Contains(out.Summary, "no device") {
		t.Errorf("fault: no-device = %+v", out)
	}

	// Device but no maestro binary anywhere.
	out = runE2E(t, &RunE2E{WorkspacePath: ws, ADBTarget: "localhost:5555",
		MaestroPath: filepath.Join(t.TempDir(), "definitely-not-maestro")})
	if !out.Passed || !strings.Contains(out.Summary, "maestro is not installed") {
		t.Errorf("fault: no-maestro = %+v", out)
	}

	// Maestro present but no flows authored.
	fake := fakeMaestro(t, 0)
	out = runE2E(t, &RunE2E{WorkspacePath: ws, ADBTarget: "localhost:5555", MaestroPath: fake})
	if !out.Passed || !strings.Contains(out.Summary, "no .maestro/flows") {
		t.Errorf("fault: no-flows = %+v", out)
	}
}

// A passing flow set: every flow ran, all green.
func TestRunE2EPassesFlows(t *testing.T) {
	ws := t.TempDir()
	flows := filepath.Join(ws, ".maestro", "flows")
	if err := os.MkdirAll(flows, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"launch.yaml", "navigate.yaml"} {
		if err := os.WriteFile(filepath.Join(flows, f), []byte("appId: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := fakeMaestro(t, 0)
	out := runE2E(t, &RunE2E{WorkspacePath: ws, ADBTarget: "localhost:5555", MaestroPath: fake})
	if !out.Passed || len(out.Flows) != 2 {
		t.Errorf("fault: pass = %+v", out)
	}
	if !strings.Contains(out.Summary, "2 flows passed") {
		t.Errorf("fault: summary %q", out.Summary)
	}
}

// A failing flow: the verdict is false and the errors carry the flow's
// name and output tail — the crash-index evidence.
func TestRunE2EFailingFlow(t *testing.T) {
	ws := t.TempDir()
	flows := filepath.Join(ws, ".maestro", "flows")
	if err := os.MkdirAll(flows, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(flows, "good.yaml"), []byte("appId: x\n"), 0o644)
	os.WriteFile(filepath.Join(flows, "bad.yaml"), []byte("appId: x\n"), 0o644)

	fake := fakeMaestro(t, 1) // exit 1 for flows named "bad"
	out := runE2E(t, &RunE2E{WorkspacePath: ws, ADBTarget: "localhost:5555", MaestroPath: fake})
	if out.Passed {
		t.Errorf("fault: a failing flow reported passed: %+v", out)
	}
	if !strings.Contains(out.Errors, "bad.yaml") || !strings.Contains(out.Errors, "FAILED") {
		t.Errorf("fault: errors lack the flow evidence: %q", out.Errors)
	}
	if !strings.Contains(out.Summary, "1/2") {
		t.Errorf("fault: summary %q", out.Summary)
	}
}

// A single-flow selection runs only that flow.
func TestRunE2ESingleFlowSelection(t *testing.T) {
	ws := t.TempDir()
	flows := filepath.Join(ws, ".maestro", "flows")
	if err := os.MkdirAll(flows, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(flows, "one.yaml"), []byte("appId: x\n"), 0o644)
	os.WriteFile(filepath.Join(flows, "two.yaml"), []byte("appId: x\n"), 0o644)

	fake := fakeMaestro(t, 0)
	res, err := (&RunE2E{WorkspacePath: ws, ADBTarget: "localhost:5555", MaestroPath: fake}).
		Execute(context.Background(), json.RawMessage(`{"flow":"two"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out e2eOut
	json.Unmarshal([]byte(res.Content), &out)
	if len(out.Flows) != 1 || out.Flows[0] != "two.yaml" {
		t.Errorf("fault: selection ran %+v", out.Flows)
	}
}

// fakeMaestro writes a script that exits failCode when the flow argument
// contains "bad" — the pass/fail fork the tool must distinguish.
func fakeMaestro(t *testing.T, failCode int) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "maestro")
	script := `#!/bin/sh
# args: test --format simple <flow-path>
case "$*" in
  *bad*) echo "flow failed: element not found"; exit ` + itoaE2E(failCode) + `
esac
echo "flow passed"
exit 0
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoaE2E(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
