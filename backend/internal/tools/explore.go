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

// explore is a single-call combo of list_files + find_files + grep_codebase,
// designed for the very first turn of a build when the model needs a quick map
// of the project. It saves the cache prefix from paying for three separate
// tool descriptions per call, and saves a round that would otherwise be spent
// sequencing the three calls.
//
// The combo is bounded: tree is capped at 200 entries, file matches at 50,
// grep matches at 50, content reads at 1. Past those limits the tool says so
// and points the model at the focused tool to use next.
type Explore struct{ FS *FS }

func (t *Explore) Name() string { return "explore" }

func (t *Explore) Description() string {
	return "One-call overview of the project. Combines: (1) a tree of source files, (2) files " +
		"matching a name pattern, (3) files containing a regex, and (4) the contents of one " +
		"specific file. All outputs are bounded — past the cap, use the focused tool " +
		"(list_files, find_files, search_files, read_file) for the rest. Use this when you are " +
		"orienting to an unfamiliar project and do not yet know which focused tool to call."
}

func (t *Explore) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path":     {"type": "string", "description": "Subdirectory to scope, relative to the project root. Omit for the whole project."},
			"name":     {"type": "string", "description": "Optional glob matched against basenames, e.g. \"*.tsx\""},
			"pattern":  {"type": "string", "description": "Optional regular expression searched across source files (Go/RE2 syntax)"},
			"read":     {"type": "string", "description": "Optional path to a single file to read. Pair with the others or use alone."}
		},
		"required": []
	}`)
}

const (
	exploreTreeCap   = 200
	exploreNameCap   = 50
	exploreGrepCap   = 50
	exploreGrepExts  = ".ts .tsx .js .jsx .json .md .css"
)

func (t *Explore) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Path    string `json:"path"`
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
		Read    string `json:"read"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}

	// The four sub-actions are independent. A failure in one must not poison the
	// others — a tree is useful even when the read target is missing.
	var sections []string
	anyRan := false

	dir := t.FS.Root()
	if a.Path != "" && a.Path != "." {
		resolved, err := t.FS.Resolve(a.Path)
		if err != nil {
			return errResult("path rejected: %v", err), nil
		}
		dir = resolved
	}

	if tree, err := exploreTree(dir, t.FS.Root()); err != nil {
		sections = append(sections, "## tree (failed: "+err.Error()+")")
	} else if tree != "" {
		anyRan = true
		sections = append(sections, "## tree\n"+tree)
	}

	if a.Name != "" {
		if matches, err := exploreFind(dir, t.FS.Root(), a.Name); err != nil {
			sections = append(sections, "## find_files "+a.Name+" (failed: "+err.Error()+")")
		} else if matches != "" {
			anyRan = true
			sections = append(sections, "## find_files "+a.Name+"\n"+matches)
		} else {
			sections = append(sections, "## find_files "+a.Name+"\n(no matches)")
		}
	}

	if a.Pattern != "" {
		if hits, err := exploreGrep(t.FS.Root(), a.Pattern, a.Path); err != nil {
			sections = append(sections, "## pattern "+a.Pattern+" (failed: "+err.Error()+")")
		} else if hits != "" {
			anyRan = true
			sections = append(sections, "## pattern "+a.Pattern+"\n"+hits)
		} else {
			sections = append(sections, "## pattern "+a.Pattern+"\n(no matches)")
		}
	}

	if a.Read != "" {
		content, _, err := t.FS.Read(a.Read)
		if err != nil {
			sections = append(sections, "## read_file "+a.Read+"\n(could not read: "+err.Error()+". Use list_files to see what exists.)")
		} else {
			anyRan = true
			sections = append(sections, "## read_file "+a.Read+"\n"+content)
		}
	}

	if !anyRan {
		return errResult("explore needs at least one of: name, pattern, read. Pass nothing and you learn nothing."), nil
	}
	return ToolResult{Content: strings.Join(sections, "\n\n")}, nil
}

func exploreTree(dir, base string) (string, error) {
	files, err := walkSource(dir, base)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No source files yet.", nil
	}
	sort.Strings(files)
	if len(files) > exploreTreeCap {
		return strings.Join(files[:exploreTreeCap], "\n") +
			fmt.Sprintf("\n\n[%d more files — pass path= or use list_files with a subdir to see the rest]", len(files)-exploreTreeCap), nil
	}
	return strings.Join(files, "\n"), nil
}

func exploreFind(dir, base, pattern string) (string, error) {
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
		if matched, _ := filepath.Match(pattern, info.Name()); !matched {
			return nil
		}
		if len(out) >= exploreNameCap {
			truncated = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if len(out) == 0 {
		return "", nil
	}
	sort.Strings(out)
	result := strings.Join(out, "\n")
	if truncated {
		result += fmt.Sprintf("\n[%d+ matches — narrow the pattern for more]", exploreNameCap)
	}
	return result, nil
}

func exploreGrep(root, pattern, scope string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %v", err)
	}
	exts := map[string]bool{}
	for _, e := range strings.Fields(exploreGrepExts) {
		exts[e] = true
	}
	dir := root
	if scope != "" && scope != "." {
		resolved, err := ValidateSandboxPath(root, scope)
		if err != nil {
			return "", err
		}
		dir = resolved
	}
	var out []string
	total := 0
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
		if total >= exploreGrepCap {
			return filepath.SkipAll
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(data), "\n") {
			if !re.MatchString(line) {
				continue
			}
			if total >= exploreGrepCap {
				return filepath.SkipAll
			}
			out = append(out, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			total++
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if len(out) == 0 {
		return "", nil
	}
	if total >= exploreGrepCap {
		out = append(out, fmt.Sprintf("[stopped at %d matches — narrow the pattern for more]", exploreGrepCap))
	}
	return strings.Join(out, "\n"), nil
}


