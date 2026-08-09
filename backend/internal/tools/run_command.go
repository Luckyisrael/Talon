package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type RunCommand struct {
	WorkspacePath string
}

func (t *RunCommand) Name() string { return "run_command" }

func (t *RunCommand) Description() string {
	return "Execute a build/install command. Only allowed commands: npm, yarn, tsc, eslint, expo, gradlew."
}

func (t *RunCommand) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The command to execute (e.g., 'npm install', 'tsc --noEmit')"
			}
		},
		"required": ["command"]
	}`)
}

type RunCommandArgs struct {
	Command string `json:"command"`
}

// Strict allowlist — npx removed because it can download and execute arbitrary packages
var allowedCommands = map[string]bool{
	"npm":     true,
	"yarn":    true,
	"tsc":     true,
	"eslint":  true,
	"expo":    true,
	"gradlew": true,
}

// Blocked arguments that could cause destructive actions
var blockedArgs = map[string]bool{
	"--prefix=/":    true,
	"--global":      true,
	"-g":            true,
	"rm":            true,
	"eval":          true,
	"--eval":        true,
}

func (t *RunCommand) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a RunCommandArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	parts := strings.Fields(a.Command)
	if len(parts) == 0 {
		return ToolResult{Content: "empty command", IsError: true}, nil
	}

	cmdName := parts[0]
	if !allowedCommands[cmdName] {
		return ToolResult{
			Content: fmt.Sprintf("command not allowed: %s. Allowed: npm, yarn, tsc, eslint, expo, gradlew", cmdName),
			IsError: true,
		}, nil
	}

	// Check for blocked arguments
	for _, arg := range parts[1:] {
		if blockedArgs[arg] {
			return ToolResult{
				Content: fmt.Sprintf("argument not allowed: %s", arg),
				IsError: true,
			}, nil
		}
		// Block arguments that look like absolute paths
		if strings.HasPrefix(arg, "/") {
			return ToolResult{
				Content: "absolute paths in arguments are not allowed",
				IsError: true,
			}, nil
		}
	}

	var execCmd *exec.Cmd
	if len(parts) > 1 {
		execCmd = exec.CommandContext(ctx, cmdName, parts[1:]...)
	} else {
		execCmd = exec.CommandContext(ctx, cmdName)
	}
	execCmd.Dir = t.WorkspacePath

	output, err := execCmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("Command failed: %s\nOutput:\n%s", a.Command, outputStr),
			IsError: true,
		}, err
	}

	return ToolResult{
		Content: fmt.Sprintf("Command succeeded: %s\nOutput:\n%s", a.Command, outputStr),
	}, nil
}
