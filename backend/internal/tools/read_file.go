package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	cleanPath := filepath.Clean(a.Path)
	if strings.Contains(cleanPath, "..") {
		return ToolResult{Content: "path traversal not allowed", IsError: true}, nil
	}

	fullPath := filepath.Join(t.WorkspacePath, cleanPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("error reading file: %v", err), IsError: true}, err
	}

	return ToolResult{Content: string(data)}, nil
}
