package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/harness/sandbox"
)

// RunShell gives the agent the same kind of shell access a developer has when
// debugging: inspect anything, probe localhost services, drive adb, run node
// one-liners — instead of reasoning about the system blind.
//
// It is a tiered shell, not an open one. Every command is classified:
//
//	Tier 0 — read-only diagnostics, always allowed (ls, cat, grep, localhost
//	  curl, read-only adb/git commands).
//	Tier 1 — workspace-scoped mutation, allowed and logged (mkdir, cp, npm,
//	  adb install, node -e probes).
//	Tier 2 — anything destructive, exfiltrating, or outside the workspace.
//	  Routed through the H4 approval seam: one strictly-wider ask, audited to
//	  the session log, the turn's approval the binding record. When the seam
//	  cannot answer (no seam, nobody asked, nobody answered yet), the command
//	  is refused with an explanation — the agent surfaces it via ask_user and
//	  runs it only after they approve.
//
// Regardless of tier the command runs with the workspace as its working
// directory, a scrubbed environment (see sanitizedEnv), a bounded timeout,
// and capped output.
type RunShell struct {
	WorkspacePath string
	// Output, when set, receives the command's stdout/stderr as it runs.
	Output io.Writer

	// Sandbox + Approval + PromptID wire tier-2 commands to the H4 approval
	// seam: Escalate asks for exactly this command, once, audited. Nil
	// Sandbox or Approval means no seam can be asked — tier 2 then falls
	// back to the ask_user refusal, exactly the pre-seam behavior.
	Sandbox  *sandbox.Capability
	Approval *approval.Seam
	PromptID int64
}

func (t *RunShell) Name() string { return "run_shell" }

func (t *RunShell) Description() string {
	return "Run a shell command in the workspace for inspection and debugging: " +
		"grep logs, probe localhost services with curl, drive adb, run node one-liners. " +
		"Read-only commands run freely; workspace-scoped changes (mkdir, cp, npm, adb install) run logged; " +
		"destructive commands, network egress beyond localhost, and anything outside the workspace need approval — " +
		"the tool asks the turn's approval itself; when it comes back APPROVAL REQUIRED, " +
		"surface the exact command to the user with ask_user and run it only after they approve. " +
		"Never try to read secrets: .env files, tokens, keys, and the server database are blocked."
}

func (t *RunShell) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "Shell command, e.g. 'grep -rn authorize logcat.txt | head -20'"},
			"timeout_seconds": {"type": "integer", "description": "Maximum runtime, 1-300 (default 60)"}
		},
		"required": ["command"]
	}`)
}

// shellTier is the privilege level of one command segment.
type shellTier int

const (
	tierReadOnly shellTier = iota
	tierMutate
	tierApproval
	tierBlocked
)

// tier0Commands are binaries with no side effects in how the agent may invoke
// them. Anything not named here defaults to tierMutate or stricter.
var tier0Commands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true,
	"file": true, "stat": true, "du": true, "df": true, "pwd": true,
	"uname": true, "whoami": true, "echo": true, "printf": true,
	"find": true, "grep": true, "rg": true, "diff": true, "jq": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "xargs": true,
	"sleep": true, "true": true, "dirname": true, "basename": true,
}

// tier1Commands may mutate the workspace or the test device but cannot reach
// outside them. node/python one-liners land here: they are arbitrary code, so
// they are never treated as read-only.
var tier1Commands = map[string]bool{
	"mkdir": true, "cp": true, "mv": true, "ln": true, "touch": true,
	"npm": true, "npx": true, "yarn": true, "expo": true, "tsc": true,
	"eslint": true, "node": true, "python3": true, "git": true,
	"adb": true, "gradlew": true,
}

// Always-blocked patterns: no approval path exists for these. Checked against
// the raw command before any classification.
var blockedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[;&|])\s*sudo\b`),
	regexp.MustCompile(`(^|[;&|])\s*su\b`),
	regexp.MustCompile(`\|\s*(sh|bash|zsh)\b`),
	regexp.MustCompile(`(^|[;&|\s])(sh|bash)\s+-c\b.*\b(sh|bash)\s+-c\b`),
	regexp.MustCompile(`:\(\)\s*\{`),                               // fork bomb
	regexp.MustCompile(`\bmkfs\b|\bdd\b.*\bof=/dev`),               // disk destruction
	regexp.MustCompile(`\bchmod\s+-R\s+777\b`),                     // permission stripping
	regexp.MustCompile(`>\s*/dev/(sda|nvme|mmcblk)`),               // block devices
	regexp.MustCompile(`(?i)\b(rm\s+-rf?\s+/( |$)|rm\s+-rf?\s+~)`), // root/home wipe
	regexp.MustCompile(`(?i)\.env(\.|$|["'\s])`),                   // server secrets
	regexp.MustCompile(`(?i)(id_rsa|id_ed25519|\.pem\b|\.key\b|credentials\.json|talon\.db\b)`),
	regexp.MustCompile(`~\/\.ssh|/etc/(passwd|shadow|ssh)|/root/`),
	regexp.MustCompile(`(^|[\s'"])\.\.(/|$|["'\s])`), // workspace escape
	regexp.MustCompile(`(^|[\s'"])/(etc|root|proc|sys|dev|var|usr|bin|sbin|home)(/|$|["'\s])`),
	regexp.MustCompile(`169\.254\.169\.254|100\.100\.100\.200`), // cloud metadata endpoints
}

// localhostOnly restricts curl/wget to loopback targets.
var localhostRe = regexp.MustCompile(`(?i)https?://(localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(:\d+)?(/|$)`)

// classifySegment assigns a tier to one pipeline stage (no pipes inside).
func classifySegment(segment string) shellTier {
	fields := shellFields(segment)
	if len(fields) == 0 {
		return tierReadOnly
	}
	name := fields[0]
	args := fields[1:]

	switch {
	case name == "curl" || name == "wget":
		if localhostOnly(args) {
			return tierReadOnly
		}
		return tierApproval // network egress needs approval
	case name == "adb":
		return classifyADB(args)
	case name == "git":
		return classifyGit(args)
	case tier0Commands[name]:
		// Read-only does not mean read-anywhere: file operands must stay
		// inside the workspace. Absolute paths and parent escapes need
		// approval even for reads — /tmp and /home can hold other projects'
		// secrets, and the read_* tools already cover the workspace.
		for _, op := range args {
			if strings.HasPrefix(op, "/") || op == ".." || strings.HasPrefix(op, "../") {
				return tierApproval
			}
		}
		return tierReadOnly
	case tier1Commands[name]:
		return tierMutate
	default:
		// Unknown binaries are not automatically trusted: the agent can ask.
		return tierApproval
	}
}

// classifyADB splits adb's surface: inspection is free, device mutation is
// logged, destructive device acts need approval.
func classifyADB(args []string) shellTier {
	if len(args) == 0 {
		return tierReadOnly
	}
	// Strip "-s <target>" prefix.
	if args[0] == "-s" && len(args) > 2 {
		args = args[2:]
	}
	if len(args) == 0 {
		return tierReadOnly
	}
	switch args[0] {
	case "shell":
		if len(args) < 2 {
			return tierApproval
		}
		sub := args[1]
		switch sub {
		case "dumpsys", "screencap", "getprop", "wm", "settings", "pm":
			if len(args) > 2 && args[2] == "list" {
				return tierReadOnly
			}
			if sub == "dumpsys" || sub == "screencap" || sub == "getprop" || sub == "wm" {
				return tierReadOnly
			}
			return tierMutate
		case "input", "uiautomator", "am":
			return tierMutate // drives the device, reversible
		case "rm", "reboot", "locksettings":
			return tierApproval
		default:
			return tierApproval
		}
	case "install", "push", "pull", "connect", "forward", "reverse":
		return tierMutate
	case "devices", "logcat":
		return tierReadOnly // pure inspection, no device state change
	case "uninstall", "disconnect":
		return tierApproval
	default:
		return tierApproval
	}
}

// classifyGit keeps history inspection free but never lets the agent rewrite
// history or push on its own.
func classifyGit(args []string) shellTier {
	if len(args) == 0 {
		return tierReadOnly
	}
	switch args[0] {
	case "status", "diff", "log", "show", "branch", "stash", "ls-files":
		return tierReadOnly
	case "add", "checkout", "restore", "mv", "rm":
		return tierMutate
	default:
		return tierApproval // commit, push, reset, rebase, clean…
	}
}

// localhostOnly reports whether every URL-looking argument targets loopback.
func localhostOnly(args []string) bool {
	foundURL := false
	for _, a := range args {
		if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
			foundURL = true
			if !localhostRe.MatchString(a) {
				return false
			}
		}
	}
	return foundURL
}

// shellFields splits a command segment on whitespace, honouring single and
// double quotes. Good enough for classification; the shell itself parses.
func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// splitChain splits a command on &&, || and ; … but not on single pipes, so
// each element is one pipeline. Pipes are split separately by the caller.
func splitChain(cmd string) []string {
	re := regexp.MustCompile(`&&|\|\||;`)
	var out []string
	last := 0
	for _, loc := range re.FindAllStringIndex(cmd, -1) {
		out = append(out, cmd[last:loc[0]])
		last = loc[1]
	}
	return append(out, cmd[last:])
}

// classifyCommand returns the highest tier across the whole command. A pipe is
// only free when every stage is read-only — otherwise piped output could carry
// inspected data straight into a mutating or exfiltrating command.
func classifyCommand(cmd string) (shellTier, string) {
	for _, re := range blockedPatterns {
		if re.MatchString(cmd) {
			return tierBlocked, fmt.Sprintf("matched blocked pattern %q", re.String())
		}
	}
	overall := tierReadOnly
	for _, link := range splitChain(cmd) {
		stages := strings.Split(link, "|")
		if len(stages) > 1 {
			for _, st := range stages {
				if classifySegment(strings.TrimSpace(st)) != tierReadOnly {
					return tierApproval, "pipelines may only combine read-only commands"
				}
			}
			continue
		}
		if t := classifySegment(strings.TrimSpace(link)); t > overall {
			overall = t
		}
	}
	return overall, ""
}

const (
	defaultShellTimeout = 60 * time.Second
	maxShellTimeout     = 300 * time.Second
)

func (t *RunShell) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	command := strings.TrimSpace(a.Command)
	if command == "" {
		return errResult("command is empty"), nil
	}

	tier, reason := classifyCommand(command)
	switch tier {
	case tierBlocked:
		return errResult(
			"BLOCKED: this command is never allowed (%s). It risks secrets, the host, or the network. "+
				"Describe what you need and use ask_user to request it instead.", reason), nil
	case tierApproval:
		// H4: the tier-2 path is the approval seam — one strictly-wider ask,
		// one audit event, turn_approvals the binding record. Only a granted
		// escalation falls through to execution (exactly this command, once);
		// every other outcome is a terminal tool result.
		if res, done := t.escalateTier2(command, reason); done {
			return res, nil
		}
	}

	timeout := defaultShellTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
		if timeout > maxShellTimeout {
			timeout = maxShellTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = t.WorkspacePath
	cmd.Env = sanitizedEnv()
	// Kill the whole process group on timeout, not just the sh wrapper:
	// otherwise an orphaned child (sleep, a hung dev server) keeps Wait
	// blocked and the timeout never actually fires. Linux-only, matching
	// the rest of the host tooling (adb, docker, redroid).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	raw, runErr := runCaptured(cmd, t.Output)
	output := clipCommandOutput(string(raw))

	if ctx.Err() == context.DeadlineExceeded {
		return errResult("%s timed out after %s.\nOutput so far:\n%s", command, timeout, output), nil
	}
	if runErr != nil {
		return errResult("command failed: %s\nOutput:\n%s", runErr, output), nil
	}
	if output == "" {
		output = "(no output)"
	}
	level := "diagnostic"
	if tier == tierMutate {
		level = "workspace change"
	}
	return ToolResult{Content: fmt.Sprintf("%s succeeded [%s].\nOutput:\n%s", command, level, output)}, nil
}

// escalateTier2 routes a tier-2 command through the H4 approval seam: one
// strictly-wider ask (Escalate), one audit event, the turn's approval the
// binding record. The result is terminal unless the escalation granted the
// run — AllowedOnce falls through to execution for exactly this command,
// once. Unavailable (no seam, no answerer, nobody answered yet) keeps the
// ask_user refusal as the model-visible fallback; Rejected is final for the
// operation.
func (t *RunShell) escalateTier2(command, reason string) (ToolResult, bool) {
	if strings.TrimSpace(reason) == "" {
		// Escalate refuses unjustified requests, and the classifier often
		// returns no reason for a tier-2 verdict — the tier itself is the
		// justification, stated in the shape the seam requires.
		reason = "outside the always-allowed shell set"
	}
	var (
		d   sandbox.Decision
		out approval.Outcome
	)
	if t.Sandbox == nil || t.Approval == nil {
		// No seam to ask: unavailable, never unseamed. This is the pre-W1.6
		// behavior — the ask_user refusal below — preserved for bare tools.
		d, out = sandbox.Decision{Allowed: false, Reason: "no approval seam is registered"}, approval.Unavailable
	} else {
		d, out = t.Sandbox.Escalate(sandbox.Escalation{
			From:          t.Sandbox.Mode(),
			To:            sandbox.ModeDangerFull,
			Operation:     sandbox.Operation{Verb: "shell", Command: command},
			Justification: reason,
			PromptID:      t.PromptID,
		}, t.Approval)
	}
	switch out {
	case approval.AllowedOnce:
		return ToolResult{}, false // granted: run exactly this command once.
	case approval.Rejected:
		return errResult(
			"REFUSED: approval to run this command was denied (%s). "+
				"Do not retry it — the refusal is final for this command; "+
				"explain the alternative to the user instead.", d.Reason), true
	default: // unavailable / cancelled — nobody could answer right now.
		return errResult(
			"APPROVAL REQUIRED: this command is outside the always-allowed set (%s). "+
				"Escalation through the approval seam is %s. "+
				"Do not run it. Use ask_user to show the user the exact command and why you need it, "+
				"and run it only after they approve.", reason, out.String()), true
	}
}
