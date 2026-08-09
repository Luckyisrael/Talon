package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ListFiles struct {
	WorkspacePath string
}

func (t *ListFiles) Name() string { return "list_files" }

func (t *ListFiles) Description() string {
	return "List files in the project directory. Returns a list of file paths relative to the workspace."
}

func (t *ListFiles) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Relative directory path to list (default: project root)"
			}
		},
		"required": []
	}`)
}

type ListFilesArgs struct {
	Path string `json:"path"`
}

type FileEntry struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func (t *ListFiles) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a ListFilesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	dir := t.WorkspacePath
	if a.Path != "" {
		var err error
		dir, err = ValidateSandboxPath(t.WorkspacePath, a.Path)
		if err != nil {
			return ToolResult{Content: fmt.Sprintf("path rejected: %v", err), IsError: true}, nil
		}
	}

	var entries []FileEntry
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip symlinks to prevent escape
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || name == ".expo" || name == ".staging" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relPath, _ := filepath.Rel(t.WorkspacePath, path)
		entries = append(entries, FileEntry{
			Path:  filepath.ToSlash(relPath),
			IsDir: false,
			Size:  info.Size(),
		})
		return nil
	})
	if err != nil {
		return ToolResult{Content: "error listing files", IsError: true}, err
	}

	if entries == nil {
		entries = []FileEntry{}
	}

	data, _ := json.MarshalIndent(entries, "", "  ")
	return ToolResult{Content: string(data)}, nil
}
