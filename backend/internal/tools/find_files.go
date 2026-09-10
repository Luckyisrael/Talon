package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// find_files locates files by name pattern without reading them. It is the
// counterpart to read_file when the model knows the name (or partial name) of
// what it wants but not where it lives. Path traversal checks run through
// FS.Resolve, so a malicious pattern like "../../etc/passwd" is rejected the
// same way read_file rejects it.
type FindFiles struct{ FS *FS }

func (t *FindFiles) Name() string { return "find_files" }

func (t *FindFiles) Description() string {
	return "Locate files by name pattern. Useful when you know (or partly know) the filename but " +
		"not the path. The pattern is a shell glob (e.g. \"*.tsx\", \"*Button*\") matched against " +
		"basenames. Pass path to scope the search to a subdirectory. Returns matching paths " +
		"relative to the project root, sorted."
}

func (t *FindFiles) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Shell glob matched against the basename, e.g. \"*.tsx\" or \"*Button*\""},
			"path":    {"type": "string", "description": "Subdirectory to scope the search, relative to the project root. Omit for the whole project."}
		},
		"required": ["pattern"]
	}`)
}

const maxFindMatches = 200

func (t *FindFiles) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return errResult("find_files needs a non-empty pattern"), nil
	}

	dir := t.FS.Root()
	if a.Path != "" && a.Path != "." {
		resolved, err := t.FS.Resolve(a.Path)
		if err != nil {
			return errResult("path rejected: %v", err), nil
		}
		dir = resolved
	}

	var out []string
	truncated := false
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != dir && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		// filepath.Match's syntax is "the pattern must match the whole name",
		// which is exactly what we want for a basename search.
		if matched, _ := filepath.Match(a.Pattern, info.Name()); !matched {
			return nil
		}
		if len(out) >= maxFindMatches {
			truncated = true
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(t.FS.Root(), path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return errResult("find failed: %v", walkErr), nil
	}

	if len(out) == 0 {
		return ToolResult{Content: fmt.Sprintf("No files matching %q", a.Pattern)}, nil
	}
	sort.Strings(out)
	result := strings.Join(out, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n[stopped at %d matches — narrow the pattern for more]", maxFindMatches)
	}
	return ToolResult{Content: result}, nil
}
