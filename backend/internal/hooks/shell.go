package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandHandler is the shell handler type: stdin carries the Payload as
// JSON, stdout carries the Decision as JSON, and exit codes follow the
// contract — 0 = proceed, 2 = block with stderr as the model-visible
// reason. Any other exit, spawn failure, or timeout is an extension
// failure (error → the caller fails closed).
type commandHandler struct {
	command string
}

func (c *commandHandler) Handle(ctx context.Context, p Payload) (Decision, error) {
	in, err := json.Marshal(p)
	if err != nil {
		return Decision{}, fmt.Errorf("encode payload: %w", err)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", c.command)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			// Exit 2 = block: stderr is the reason the model reads.
			reason := strings.TrimSpace(stderr.String())
			if reason == "" {
				reason = "blocked by hook " + c.name()
			}
			return Decision{Decision: Deny, Reason: reason}, nil
		}
		// Timeout (ctx), spawn failure, any other exit: extension failure.
		if ctx.Err() != nil {
			return Decision{}, fmt.Errorf("hook command timed out: %w", ctx.Err())
		}
		if stderr.Len() > 0 {
			return Decision{}, fmt.Errorf("hook command failed (%v): %s", err, firstLine(stderr.String()))
		}
		return Decision{}, fmt.Errorf("hook command failed: %w", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		// Exit 0 with no output = proceed — the contract's default.
		return Decision{Decision: Allow}, nil
	}
	var d Decision
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		return Decision{}, fmt.Errorf("hook printed invalid decision JSON: %w", err)
	}
	switch d.Decision {
	case Allow, Deny, Ask:
		return d, nil
	case "":
		return Decision{Decision: Allow}, nil
	default:
		return Decision{}, invalidDecisionError(d.Decision)
	}
}

func (c *commandHandler) name() string { return "command:" + truncate(c.command, 60) }

// Built-in names config can reference instead of a command. The three the
// plan requires, each proving a different facet of the pattern: gating
// (sync, in-process), after-work (async, in-process), notification (async,
// network).
const (
	BuiltinBlockEnvEdits   = "block-env-edits"
	BuiltinRunTestsAfterWr = "run-tests-after-write"
	BuiltinSlackOnTurnFail = "slack-on-turn-fail"
)

// newBuiltin resolves a built-in name to its handler and whether the
// registry should run it async. Gating built-ins are sync (their decision
// must land before dispatch); work/notification built-ins are async by
// construction so they never delay a round. Each built-in validates the
// fields it uses and rejects the ones it does not — a typo'd parameter
// fails the whole config loudly instead of being silently ignored.
func newBuiltin(e entry) (Handler, bool, error) {
	switch e.Builtin {
	case BuiltinBlockEnvEdits:
		if e.Command != "" || e.Webhook != "" {
			return nil, false, fmt.Errorf("builtin %q takes no command/webhook", e.Builtin)
		}
		return &envBlockHandler{}, false, nil
	case BuiltinRunTestsAfterWr:
		if e.Webhook != "" {
			return nil, false, fmt.Errorf("builtin %q takes no webhook", e.Builtin)
		}
		if e.Command == "" {
			return nil, false, fmt.Errorf("builtin %q needs a \"command\" (the project's test command)", e.Builtin)
		}
		return &commandHandler{command: e.Command}, true, nil
	case BuiltinSlackOnTurnFail:
		if e.Command != "" {
			return nil, false, fmt.Errorf("builtin %q takes no command", e.Builtin)
		}
		if e.Webhook == "" {
			return nil, false, fmt.Errorf("builtin %q needs a \"webhook\" URL", e.Builtin)
		}
		return &slackHandler{url: e.Webhook}, true, nil
	}
	return nil, false, fmt.Errorf("unknown builtin %q (want %s, %s, %s)",
		e.Builtin, BuiltinBlockEnvEdits, BuiltinRunTestsAfterWr, BuiltinSlackOnTurnFail)
}

// envBlockHandler is the block-.env-edits built-in: it moves the secrets
// class out of prompt-text scanning and into enforcement — any file-writing
// tool whose target is a real .env file is denied with a model-visible
// reason. Template files (.env.example and friends) stay editable: they
// carry names, never values.
//
// Scope (documented boundary): path-targeted mutators only — write_file,
// edit_file, multi_edit, delete_file, try_edit. A shell command that
// redirects into .env is the sandbox/hard-deny layer's problem, not a
// path-matcher's guess.
type envBlockHandler struct{}

// envTemplateFiles are the .env* files that are allowed: public templates.
var envTemplateFiles = map[string]bool{
	".env.example":  true,
	".env.sample":   true,
	".env.template": true,
	".env.dist":     true,
}

// envMutatingTools are the path-targeted file writers (the same set
// mutatingToolPath classifies in the api composition).
var envMutatingTools = map[string]bool{
	"write_file":  true,
	"edit_file":   true,
	"multi_edit":  true,
	"delete_file": true,
	"try_edit":    true,
}

func (h *envBlockHandler) Handle(_ context.Context, p Payload) (Decision, error) {
	if p.Event != PreToolUse || !envMutatingTools[p.Tool] {
		return Decision{Decision: Allow}, nil
	}
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(p.Input, &in) != nil || in.Path == "" {
		// Malformed input: the tool itself produces the honest error.
		return Decision{Decision: Allow}, nil
	}
	base := filepath.Base(in.Path)
	if envTemplateFiles[base] || !strings.HasPrefix(base, ".env") {
		return Decision{Decision: Allow}, nil
	}
	return Decision{
		Decision: Deny,
		Reason: "editing " + base + " is blocked by the block-env-edits hook — secret values never enter the workspace through the agent. " +
			"Declare the key's *name* in .env.example and ask the user to set the real value.",
	}, nil
}

// slackHandler is the notification built-in: TurnStop with a failed
// outcome POSTs a small JSON body to the configured webhook. Success is a
// no-op (Allow without I/O). Always async — a notification never delays or
// decides a turn; its own failures are logged by the async path.
type slackHandler struct {
	url string
}

func (s *slackHandler) Handle(ctx context.Context, p Payload) (Decision, error) {
	if p.Event != TurnStop || p.Outcome != "failure" {
		return Decision{Decision: Allow}, nil
	}
	body := map[string]string{"text": "Talon turn failed" + failureSuffix(p)}
	raw, err := json.Marshal(body)
	if err != nil {
		return Decision{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(raw))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("notify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return Decision{}, fmt.Errorf("notify: webhook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return Decision{Decision: Allow}, nil
}

func failureSuffix(p Payload) string {
	if p.Reason == "" {
		return ""
	}
	return ": " + firstLine(p.Reason)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
