package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ── project_map ───────────────────────────────────────
//
// A read-only tool (and a reusable builder) that renders the workspace as a
// lightweight code map: every source file with the symbols it exports and the
// modules it imports. The imports are a dependency graph in miniature — which
// files and libraries each file leans on — which is exactly what a model needs to
// stop guessing at a layout it cannot see. The same text is injected once at build
// start so the model begins grounded rather than spending a round rediscovering
// the project.

type ProjectMap struct{ FS *FS }

func (t *ProjectMap) Name() string { return "project_map" }

func (t *ProjectMap) Description() string {
	return "Survey the whole project at once: every source file with the symbols it exports and the " +
		"modules it imports — a quick dependency map. The same map is already in your context and refreshes " +
		"itself as files change, so call this only if the map disagrees with what the files actually contain. " +
		"Read a specific file with read_file when you need its contents."
}

func (t *ProjectMap) InputSchema() json.RawMessage {
	// No inputs: the map is always the whole project. An empty properties object
	// keeps the validator permissive and tells the model there is nothing to pass.
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *ProjectMap) Execute(_ context.Context, _ json.RawMessage) (ToolResult, error) {
	m := ProjectMapText(t.FS.Root())
	if m == "" {
		return ToolResult{Content: "No source files yet — this is a fresh project. Create files with write_file."}, nil
	}
	return ToolResult{Content: m}, nil
}

// maxMapBytes bounds the rendered map. It sits in the cached system-prompt prefix
// when injected and counts against a tool call's output budget when called, so a
// giant monorepo cannot swamp either; past the cap the map truncates with a note
// pointing at list_files/read_file for the rest.
const maxMapBytes = 12 * 1024

// ProjectMapText walks root and renders each source file's exported symbols and
// imported modules, grouped by directory. It returns "" when the project has no
// source files yet, so a caller can skip injecting an empty map. Output is
// deterministic (lexically ordered) so an unchanged project yields identical bytes
// and never busts the prompt cache.
func ProjectMapText(root string) string {
	type entry struct {
		rel     string
		exports []string
		imports []string
	}
	var entries []entry

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != root && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if !isMappableSource(info.Name()) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		src := string(data)
		entries = append(entries, entry{
			rel:     filepath.ToSlash(rel),
			exports: extractExports(src),
			imports: extractImports(src),
		})
		return nil
	})

	if len(entries) == 0 {
		return ""
	}

	// Group by directory so the tree reads the way the file system is laid out.
	byDir := map[string][]entry{}
	for _, e := range entries {
		dir := filepath.ToSlash(filepath.Dir(e.rel))
		byDir[dir] = append(byDir[dir], e)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var b strings.Builder
	fmt.Fprintf(&b, "## Current project structure\n\n%d source file(s). Each line lists a file's exported "+
		"symbols and the modules it imports. Use read_file for contents. This map refreshes itself "+
		"as files change — do not call project_map to refresh it.\n", len(entries))

	truncated := false
	for _, dir := range dirs {
		header := dir
		if dir == "." {
			header = "(project root)"
		}
		fmt.Fprintf(&b, "\n%s/\n", strings.TrimSuffix(header, "/"))
		files := byDir[dir]
		sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
		for _, e := range files {
			line := "  " + filepath.Base(e.rel) + fileClause(e.exports, e.imports) + "\n"
			if b.Len()+len(line) > maxMapBytes {
				truncated = true
				break
			}
			b.WriteString(line)
		}
		if truncated {
			break
		}
	}
	if truncated {
		b.WriteString("\n[map truncated — use list_files and read_file to explore the rest]\n")
	}

	return b.String()
}

// ProjectFingerprint hashes the workspace's mappable file set (path, mtime,
// size). It is the cheap staleness check for the injected project map: same
// fingerprint means the map still describes the tree, so the cached prompt
// prefix survives the round; a changed fingerprint rebuilds it. It walks the
// same file set ProjectMapText renders, so the two can never disagree about
// what "the project" contains.
func ProjectFingerprint(root string) string {
	h := fnv.New64a()
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if path != root && ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if !isMappableSource(info.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		fmt.Fprintf(h, "%s:%d:%d\n", filepath.ToSlash(rel), info.ModTime().Unix(), info.Size())
		return nil
	})
	return strconv.FormatUint(h.Sum64(), 16)
}

// fileClause renders the "— exports … · imports …" tail for one file, omitting
// whichever half is empty and returning "" when the file has neither.
func fileClause(exports, imports []string) string {
	const perFile = 10
	var parts []string
	if len(exports) > 0 {
		parts = append(parts, "exports "+strings.Join(capList(exports, perFile), ", "))
	}
	if len(imports) > 0 {
		parts = append(parts, "imports "+strings.Join(capList(imports, perFile), ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return " — " + strings.Join(parts, " · ")
}

// capList trims a list to at most n entries, marking the overflow with an ellipsis
// so a barrel file with fifty re-exports does not dominate the map.
func capList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	out := append([]string(nil), xs[:n]...)
	return append(out, fmt.Sprintf("…(+%d)", len(xs)-n))
}

func isMappableSource(name string) bool {
	// Declaration files are all signatures and no structure — noise in a map whose
	// point is to show the project's own code.
	if strings.HasSuffix(name, ".d.ts") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ts", ".tsx", ".js", ".jsx":
		return true
	}
	return false
}

// IsMappableSource is the exported predicate behind isMappableSource, for the
// codebase index (W4): the index must walk exactly the file set the map
// renders and ProjectFingerprint hashes — same-file-set law, by construction,
// not by copy. Widening the set (say, to .go) means changing this one switch
// and updating the map/fingerprint tests together with it.
func IsMappableSource(name string) bool { return isMappableSource(name) }

var (
	// A named declaration export, e.g. `export function foo`, `export const bar`,
	// `export class Baz`, `export interface Qux`. The optional `default` is
	// deliberately excluded here so `export default function` falls to defaultExportRe
	// as "default" rather than surfacing an incidental name.
	namedDeclRe = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?(?:function|const|let|var|class|type|interface|enum)\s+([A-Za-z0-9_$]+)`)
	// `export { a, b as c }` — a re-export list. The braces' contents are split and
	// resolved (the name after `as` wins) separately.
	namedListRe = regexp.MustCompile(`(?m)^\s*export\s+\{([^}]*)\}`)
	// Any `export default …`, counted once as the file's default export.
	defaultExportRe = regexp.MustCompile(`(?m)^\s*export\s+default\b`)

	// Module specifiers: `import … from 'x'` / `export … from 'x'`, bare `import 'x'`,
	// and `require('x')`.
	fromRe    = regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^;\n]*?\bfrom\s+['"]([^'"]+)['"]`)
	bareRe    = regexp.MustCompile(`(?m)^\s*import\s+['"]([^'"]+)['"]`)
	requireRe = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`)
)

// extractExports returns the public symbols a source file exports, in a stable
// order (declaration order, then re-export lists, then a trailing "default").
func extractExports(src string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	for _, m := range namedDeclRe.FindAllStringSubmatch(src, -1) {
		add(m[1])
	}
	for _, m := range namedListRe.FindAllStringSubmatch(src, -1) {
		for _, part := range strings.Split(m[1], ",") {
			part = strings.TrimSpace(part)
			if part == "" || part == "type" {
				continue
			}
			// `a as b` exports the name b; a bare `a` exports a. Drop a leading
			// `type ` modifier on either side.
			if i := strings.Index(part, " as "); i >= 0 {
				part = part[i+4:]
			}
			part = strings.TrimPrefix(part, "type ")
			add(strings.TrimSpace(part))
		}
	}
	if defaultExportRe.MatchString(src) {
		add("default")
	}
	return out
}

// extractImports returns the distinct modules a source file imports, sorted so the
// map is deterministic. Local paths and package names are kept as written — the
// point is the dependency edge, not resolving it.
func extractImports(src string) []string {
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{fromRe, bareRe, requireRe} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if s := strings.TrimSpace(m[1]); s != "" {
				seen[s] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
