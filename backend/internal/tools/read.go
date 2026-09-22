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

// SpillResolver resolves a spill locator — the replacement line the model was
// given for an oversized tool output, or its bare path — to the file inside
// the spill store. compaction.SpillStore implements it. A nil resolver (no
// store wired: headless runs, tests) disables resolution; spill paths then
// simply do not exist for read_file, exactly as before W1.2.
type SpillResolver interface {
	ResolvePath(candidate string) string
}

type ReadFile struct {
	FS    *FS
	Spill SpillResolver
}

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Description() string {
	return "Read a file from the project. Output is prefixed with line numbers so you can quote exact " +
		"text back to edit_file. Reads up to 2000 lines; use offset and limit for longer files. " +
		"Oversized tool outputs that were spilled to disk are read back the same way: pass the " +
		"locator line you were shown (or its path) as the path."
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

	// H8 retrieval (W1.2): an oversized tool output was filed to disk and
	// replaced by a locator line. The model reads it back by handing that
	// locator — or its bare path — straight to read_file. Resolution goes
	// through the spill store's own containment check (its directory, .txt
	// only), so a locator is never an arbitrary file-read token, and the
	// body is read outside the workspace root and outside FS.Read's 256 KB
	// cap: that cap is exactly why the output spilled.
	if t.Spill != nil {
		if resolved := t.Spill.ResolvePath(a.Path); resolved != "" {
			raw, rerr := os.ReadFile(resolved)
			if rerr != nil {
				return errResult("could not read the spilled output: %v", rerr), nil
			}
			return renderReadWindow(string(raw), false, a.Offset, a.Limit, resolved), nil
		}
	}

	content, truncated, err := t.FS.Read(a.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return errResult("%s does not exist. Use list_files to see what is there.", a.Path), nil
		}
		return errResult("could not read %s: %v", a.Path, err), nil
	}
	return renderReadWindow(content, truncated, a.Offset, a.Limit, a.Path), nil
}

// renderReadWindow is read_file's line window, shared by workspace reads and
// spill read-backs so both render identically: 1-based offset, default
// 2000-line limit, a continuation nudge naming the true line total, and — for
// clipped workspace reads — the cap notice.
func renderReadWindow(content string, clipped bool, offset, limit int, name string) ToolResult {
	lines := strings.Split(content, "\n")
	start := offset
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return errResult("offset %d is past the end of %s (%d lines)", start, name, len(lines))
	}
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
	if clipped {
		b.WriteString("\n[file exceeds the 256 KB read cap and was clipped]\n")
	}
	return ToolResult{Content: b.String()}
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

// SearchFiles is a name-alias for GrepCodebase. The original tool's contract is
// unchanged; the alias is registered alongside it so a model that asks for
// either "grep_codebase" or "search_files" reaches the same implementation.
// The AI providers cache the tool schema by name, so renaming would bust the
// cache for callers that already learned the old name — aliasing is the
// forward-compatible shape.
type SearchFiles struct{ Inner *GrepCodebase }

func (t *SearchFiles) Name() string { return "search_files" }

func (t *SearchFiles) Description() string {
	return "Search the project's source files with a regular expression. Alias of grep_codebase " +
		"for callers that prefer the search_files name. Returns file, line number and the matching " +
		"line."
}

func (t *SearchFiles) InputSchema() json.RawMessage { return t.Inner.InputSchema() }
func (t *SearchFiles) ReadOnly() bool               { return true }

func (t *SearchFiles) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	return t.Inner.Execute(ctx, args)
}
