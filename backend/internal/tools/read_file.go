package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

type ReadFile struct {
	WorkspacePath string
}

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Description() string {
	return "Read the contents of a file in the project. Returns the full file content."
}

func (t *ReadFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Relative file path to read"
			}
		},
		"required": ["path"]
	}`)
}

type ReadFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a ReadFileArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	fullPath, err := ValidateSandboxPath(t.WorkspacePath, a.Path)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("path rejected: %v", err), IsError: true}, nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ToolResult{Content: "error reading file", IsError: true}, err
	}

	return ToolResult{Content: string(data)}, nil
}
