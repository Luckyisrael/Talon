package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RunCommand struct {
	WorkspacePath string
	// Output, when set, receives the command's stdout/stderr as it runs, so the
	// frontend can show the developer what the agent's command is doing live.
	Output io.Writer
}

func (t *RunCommand) Name() string { return "run_command" }

func (t *RunCommand) Description() string {
	return "Run a project command in the workspace. Allowed: npm, yarn, npx, tsc, eslint, expo, node, git. " +
		"The command is not run through a shell, so pipes, redirects and command chaining are not available."
}

func (t *RunCommand) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "Command with arguments, e.g. 'npm run build'"},
			"timeout_seconds": {"type": "integer", "description": "Maximum runtime, 1-600 (default 180)"}
		},
		"required": ["command"]
	}`)
}

// allowedCommands is an allowlist rather than a denylist: anything not named
// here cannot run, so a new binary appearing on the host does not widen what
// the agent can execute.
var allowedCommands = map[string]bool{
	"npm":     true,
	"yarn":    true,
	"npx":     true,
	"tsc":     true,
	"eslint":  true,
	"expo":    true,
	"node":    true,
	"git":     true,
	"gradlew": true,
}

// blockedArgs would install outside the workspace or hand the argument string
// to an interpreter.
var blockedArgs = map[string]bool{
	"--global": true,
	"-g":       true,
	"eval":     true,
	"--eval":   true,
	"-e":       true,
	"--prefix": true,
}

// scrubbedEnvPrefixes are dropped from the child environment. A build script is
// not a trust boundary, so it must not inherit the server's credentials.
var scrubbedEnvPrefixes = []string{
	"AI_", "ANTHROPIC_", "OPENROUTER_", "OPENAI_", "JWT_", "DB_",
	"AWS_", "GITHUB_TOKEN", "GH_TOKEN", "CORS_",
}

// sanitizedEnv returns the host environment minus anything secret.
func sanitizedEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		blocked := false
		for _, p := range scrubbedEnvPrefixes {
			if strings.HasPrefix(name, p) {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, kv)
		}
	}
	return append(out, "CI=1", "FORCE_COLOR=0", "NO_COLOR=1")
}

const (
	defaultCommandTimeout = 180 * time.Second
	maxCommandTimeout     = 600 * time.Second
	maxCommandOutput      = 16 * 1024
)

func (t *RunCommand) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}

	// Reject shell metacharacters outright. The command never reaches a shell,
	// so these can only be a sign the model expected shell semantics — better
	// to say so than to pass "&&" along as a literal argument.
	if i := strings.IndexAny(a.Command, "|&;><`$\n"); i >= 0 {
		return errResult(
			"%q contains the shell character %q, but commands do not run through a shell. "+
				"Run one command per call.", a.Command, a.Command[i:i+1]), nil
	}

	parts := strings.Fields(a.Command)
	if len(parts) == 0 {
		return errResult("command is empty"), nil
	}

	name := parts[0]
	if !allowedCommands[name] {
		allowed := make([]string, 0, len(allowedCommands))
		for k := range allowedCommands {
			allowed = append(allowed, k)
		}
		return errResult("%q is not an allowed command. Allowed: %s", name, strings.Join(allowed, ", ")), nil
	}

	for _, arg := range parts[1:] {
		if blockedArgs[arg] || strings.HasPrefix(arg, "--prefix=") {
			return errResult("the argument %q is not allowed", arg), nil
		}
		if strings.HasPrefix(arg, "/") {
			return errResult("absolute paths are not allowed in arguments (%q)", arg), nil
		}
		if arg == ".." || strings.HasPrefix(arg, "../") {
			return errResult("arguments may not point outside the project (%q)", arg), nil
		}
	}

	timeout := defaultCommandTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
		if timeout > maxCommandTimeout {
			timeout = maxCommandTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, parts[1:]...)
	cmd.Dir = t.WorkspacePath
	cmd.Env = sanitizedEnv()

	raw, runErr := runCaptured(cmd, t.Output)
	output := clipCommandOutput(string(raw))

	if ctx.Err() == context.DeadlineExceeded {
		return errResult("%s timed out after %s.\nOutput so far:\n%s", a.Command, timeout, output), nil
	}

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return errResult("%s exited with status %d.\nOutput:\n%s",
				a.Command, exitErr.ExitCode(), output), nil
		}
		return errResult("could not start %s: %v", a.Command, runErr), nil
	}

	if output == "" {
		output = "(no output)"
	}
	return ToolResult{Content: fmt.Sprintf("%s succeeded.\nOutput:\n%s", a.Command, output)}, nil
}

// clipCommandOutput keeps the tail: for build tools the error and summary are
// at the end, while the head is usually progress noise.
func clipCommandOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxCommandOutput {
		return s
	}
	return fmt.Sprintf("… [%d earlier bytes omitted] …\n", len(s)-maxCommandOutput) + s[len(s)-maxCommandOutput:]
}
