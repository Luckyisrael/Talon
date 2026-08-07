package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	cleanPath := filepath.Clean(a.Path)
	if strings.Contains(cleanPath, "..") {
		return ToolResult{Content: "path traversal not allowed", IsError: true}, nil
	}

	fullPath := filepath.Join(t.StagingPath, cleanPath)
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return ToolResult{Content: fmt.Sprintf("file not found: %s", cleanPath)}, nil
		}
		return ToolResult{Content: fmt.Sprintf("failed to delete file: %v", err), IsError: true}, err
	}

	return ToolResult{Content: fmt.Sprintf("File deleted from staging: %s", cleanPath)}, nil
}
