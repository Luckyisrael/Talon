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
	return "Execute a build/install command. Only allowed commands: npm, npx, yarn, tsc, eslint, expo, gradlew."
}

func (t *RunCommand) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The command to execute (e.g., 'npm install', 'npx tsc --noEmit')"
			}
		},
		"required": ["command"]
	}`)
}

type RunCommandArgs struct {
	Command string `json:"command"`
}

var allowedCommands = map[string]bool{
	"npm":     true,
	"npx":     true,
	"yarn":    true,
	"tsc":     true,
	"eslint":  true,
	"expo":    true,
	"gradlew": true,
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
			Content: fmt.Sprintf("command not allowed: %s. Allowed: npm, npx, yarn, tsc, eslint, expo, gradlew", cmdName),
			IsError: true,
		}, nil
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
			Content: fmt.Sprintf("Command failed: %s\nOutput:\n%s\nError: %v", a.Command, outputStr, err),
			IsError: true,
		}, err
	}

	return ToolResult{
		Content: fmt.Sprintf("Command succeeded: %s\nOutput:\n%s", a.Command, outputStr),
	}, nil
}
