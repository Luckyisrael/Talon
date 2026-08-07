package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GrepCodebase struct {
	WorkspacePath string
}

func (t *GrepCodebase) Name() string { return "grep_codebase" }

func (t *GrepCodebase) Description() string {
	return "Search for a pattern in the codebase. Returns matching files and lines."
}

func (t *GrepCodebase) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regex pattern to search for"
			},
			"glob": {
				"type": "string",
				"description": "File pattern to search in (e.g., '*.tsx', '*.ts')"
			}
		},
		"required": ["pattern"]
	}`)
}

type GrepCodebaseArgs struct {
	Pattern string `json:"pattern"`
	Glob    string `json:"glob"`
}

type GrepMatch struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

func (t *GrepCodebase) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a GrepCodebaseArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("invalid regex: %v", err), IsError: true}, err
	}

	var matches []GrepMatch
	err = filepath.Walk(t.WorkspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		name := info.Name()
		if name == "node_modules" || name == ".expo" || name == ".staging" || name == ".git" {
			return filepath.SkipDir
		}

		if a.Glob != "" {
			matched, _ := filepath.Match(a.Glob, name)
			if !matched {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".ts" && ext != ".tsx" && ext != ".js" && ext != ".jsx" && ext != ".json" && ext != ".go" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				relPath, _ := filepath.Rel(t.WorkspacePath, path)
				matches = append(matches, GrepMatch{
					File: filepath.ToSlash(relPath),
					Line: i + 1,
					Text: strings.TrimSpace(line),
				})
			}
		}

		return nil
	})
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("error searching: %v", err), IsError: true}, err
	}

	if len(matches) == 0 {
		return ToolResult{Content: "No matches found"}, nil
	}

	data, _ := json.MarshalIndent(matches, "", "  ")
	return ToolResult{Content: string(data)}, nil
}
