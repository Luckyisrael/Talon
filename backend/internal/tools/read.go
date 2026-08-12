package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── read_file ─────────────────────────────────────────

type ReadFile struct{ FS *FS }

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Description() string {
	return "Read a file from the project. Output is prefixed with line numbers so you can quote exact " +
		"text back to edit_file. Reads up to 2000 lines; use offset and limit for longer files."
}

func (t *ReadFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the project root"},
			"offset": {"type": "integer", "description": "1-based line to start from (default 1)"},
			"limit": {"type": "integer", "description": "Maximum lines to return (default 2000)"}
		},
		"required": ["path"]
	}`)
}

const defaultReadLimit = 2000

func (t *ReadFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}

	content, truncated, err := t.FS.Read(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return errResult("%s does not exist. Use list_files to see what is there.", a.Path), nil
		}
		return errResult("could not read %s: %v", a.Path, err), nil
	}

	lines := strings.Split(content, "\n")
	start := a.Offset
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return errResult("offset %d is past the end of %s (%d lines)", start, a.Path, len(lines)), nil
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	end := start - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, lines[i])
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "\n[showing lines %d-%d of %d — pass offset=%d to continue]\n", start, end, len(lines), end+1)
	}
	if truncated {
		b.WriteString("\n[file exceeds the 256 KB read cap and was clipped]\n")
	}

	return ToolResult{Content: b.String()}, nil
}

// ── list_files ────────────────────────────────────────

type ListFiles struct{ FS *FS }

func (t *ListFiles) Name() string { return "list_files" }

func (t *ListFiles) Description() string {
	return "List the project's source files as a tree. Dependencies and build output are excluded."
}

func (t *ListFiles) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Subdirectory to list, relative to the project root. Omit for the whole project."}
		},
		"required": []
	}`)
}

func (t *ListFiles) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}

	dir := t.FS.Root()
	if a.Path != "" && a.Path != "." {
		resolved, err := t.FS.Resolve(a.Path)
		if err != nil {
			return errResult("path rejected: %v", err), nil
		}
		dir = resolved
	}

	files, err := walkSource(dir, t.FS.Root())
	if err != nil {
		return errResult("could not list %s: %v", a.Path, err), nil
	}
	if len(files) == 0 {
		return ToolResult{Content: "No source files yet — this is a fresh project. Create files with write_file."}, nil
	}

	sort.Strings(files)
	return ToolResult{Content: strings.Join(files, "\n")}, nil
}

// walkSource collects source-file paths under dir, relative to base, skipping
// dependency and build directories. Note the ordering: SkipDir must be returned
// for a directory entry, so the IsDir branch comes before the file handling.
func walkSource(dir, base string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

// ── grep_codebase ─────────────────────────────────────

type GrepCodebase struct{ FS *FS }

func (t *GrepCodebase) Name() string { return "grep_codebase" }

func (t *GrepCodebase) Description() string {
	return "Search the project's source files with a regular expression. Returns file, line number and " +
		"the matching line."
}

func (t *GrepCodebase) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "Go/RE2 regular expression"},
			"glob": {"type": "string", "description": "Restrict to filenames matching this glob, e.g. *.tsx"}
		},
		"required": ["pattern"]
	}`)
}

const maxGrepMatches = 200

func (t *GrepCodebase) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Glob    string `json:"glob"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}

	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return errResult("invalid regular expression: %v", err), nil
	}

	searchable := map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".json": true, ".md": true, ".css": true,
	}

	var out []string
	total := 0
	walkErr := filepath.Walk(t.FS.Root(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != t.FS.Root() && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if total >= maxGrepMatches {
			return filepath.SkipAll
		}
		if !searchable[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if a.Glob != "" {
			if matched, _ := filepath.Match(a.Glob, info.Name()); !matched {
				return nil
			}
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.FS.Root(), path)
		rel = filepath.ToSlash(rel)

		for i, line := range strings.Split(string(data), "\n") {
			if !re.MatchString(line) {
				continue
			}
			if total >= maxGrepMatches {
				return filepath.SkipAll
			}
			out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			total++
		}
		return nil
	})
	if walkErr != nil {
		return errResult("search failed: %v", walkErr), nil
	}

	if len(out) == 0 {
		return ToolResult{Content: fmt.Sprintf("No matches for %q", a.Pattern)}, nil
	}
	result := strings.Join(out, "\n")
	if total >= maxGrepMatches {
		result += fmt.Sprintf("\n\n[stopped at %d matches — narrow the pattern for more]", maxGrepMatches)
	}
	return ToolResult{Content: result}, nil
}
