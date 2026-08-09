package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type DeleteFileStaged struct {
	StagingPath string
}

func (t *DeleteFileStaged) Name() string { return "delete_file" }

func (t *DeleteFileStaged) Description() string {
	return "Delete a file from the staging area."
}

func (t *DeleteFileStaged) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Relative file path to delete"
			}
		},
		"required": ["path"]
	}`)
}

type DeleteFileStagedArgs struct {
	Path string `json:"path"`
}

func (t *DeleteFileStaged) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a DeleteFileStagedArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	fullPath, err := ValidateSandboxPath(t.StagingPath, a.Path)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("path rejected: %v", err), IsError: true}, nil
	}

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return ToolResult{Content: "file not found"}, nil
		}
		return ToolResult{Content: "failed to delete file", IsError: true}, err
	}

	cleanPath := filepath.Clean(a.Path)
	return ToolResult{Content: fmt.Sprintf("File deleted from staging: %s", cleanPath)}, nil
}
