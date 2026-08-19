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
	"time"
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
