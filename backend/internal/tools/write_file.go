package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/seekersstudio/backend/internal/identity"
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

	fullPath, err := ValidateSandboxPath(t.StagingPath, a.Path)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("path rejected: %v", err), IsError: true}, nil
	}

	if err := identity.BlockIdentityFields(a.Path, a.Content); err != nil {
		return ToolResult{Content: err.Error(), IsError: true}, nil
	}

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{Content: "failed to create directory", IsError: true}, err
	}

	if err := os.WriteFile(fullPath, []byte(a.Content), 0644); err != nil {
		return ToolResult{Content: "failed to write file", IsError: true}, err
	}

	cleanPath := filepath.Clean(a.Path)
	return ToolResult{Content: fmt.Sprintf("File written to staging: %s", cleanPath)}, nil
}
