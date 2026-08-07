package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type WriteFileStaged struct {
	StagingPath string
}

func (t *WriteFileStaged) Name() string { return "write_file" }

func (t *WriteFileStaged) Description() string {
	return "Write or overwrite a file in the staging area. Files are not live until promoted."
}

func (t *WriteFileStaged) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Relative file path (e.g., src/screens/HomeScreen.tsx)"
			},
			"content": {
				"type": "string",
				"description": "Full file content"
			}
		},
		"required": ["path", "content"]
	}`)
}

type WriteFileStagedArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileStaged) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WriteFileStagedArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	cleanPath := filepath.Clean(a.Path)
	if strings.Contains(cleanPath, "..") {
		return ToolResult{Content: "path traversal not allowed", IsError: true}, nil
	}

	fullPath := filepath.Join(t.StagingPath, cleanPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{Content: fmt.Sprintf("failed to create directory: %v", err), IsError: true}, err
	}

	if err := os.WriteFile(fullPath, []byte(a.Content), 0644); err != nil {
		return ToolResult{Content: fmt.Sprintf("failed to write file: %v", err), IsError: true}, err
	}

	return ToolResult{Content: fmt.Sprintf("File written to staging: %s", cleanPath)}, nil
}
