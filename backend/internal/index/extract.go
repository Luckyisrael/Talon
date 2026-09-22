package index

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// The extractor is a deliberate, honest stopgap: W4's scope names tree-sitter,
// but this repo is a pure-Go build (modernc sqlite, no cgo) and the plan's own
// risk mitigation says the pure-Go lexical/graph layers ship first. So this is
// a line-aware scanner for TS/JSX — column-0 declarations, import specifiers,
// and name-matched calls — which is enough for BM25 retrieval and a useful
// callers/callees graph, with the parser slot behind the same Analyze seam for
// when a real parser lands.

// decl is one column-0 declaration — the unit symbols are carved on and the
// boundary chunks split at.
type decl struct {
	Line int // 1-based
	Name string
	Kind string
}

// declPatterns is first-match-wins, in source order. Column 0 only: anything
// indented is inside a body, and a body's `const` is not a project vertex.
// The `type X =` trailing `=` keeps `type Foo<T>` instantiations from
// pretending to be declarations... they don't match `=`-less forms anyway;
// the anchor just keeps `let x` style statements out of kinds it isn't.
var declPatterns = []struct {
	re   *regexp.Regexp
	kind string
}{
	{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)`), "function"},
	{regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)`), "class"},
	{regexp.MustCompile(`^(?:export\s+)?interface\s+([A-Za-z_$][A-Za-z0-9_$]*)`), "interface"},
	{regexp.MustCompile(`^(?:export\s+)?type\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:<[^>]*>)?\s*=`), "type"},
	{regexp.MustCompile(`^(?:export\s+)?enum\s+([A-Za-z_$][A-Za-z0-9_$]*)`), "enum"},
	{regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)`), "const"},
}

// scanDecls returns the file's column-0 declarations in line order.
func scanDecls(lines []string) []decl {
	var out []decl
	for i, line := range lines {
		for _, p := range declPatterns {
			if m := p.re.FindStringSubmatch(line); m != nil {
				out = append(out, decl{Line: i + 1, Name: m[1], Kind: p.kind})
				break
			}
		}
	}
	return out
}

var (
	importFromRe = regexp.MustCompile(`^\s*(?:import|export)\b[^;\n]*?\bfrom\s+['"]([^'"]+)['"]`)
	importBareRe = regexp.MustCompile(`^\s*import\s+['"]([^'"]+)['"]`)
	requireRe    = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`)
)

// extractImports returns one edge per distinct specifier. ToName is the
// specifier as written; ToPath stays "" until resolveLocal matches it against
// the workspace file set (needs the whole set, so resolution happens in
// Reconcile — see resolveLocal).
func extractImports(rel string, lines []string) []store.CodeEdge {
	seen := map[string]bool{}
	var out []store.CodeEdge
	add := func(spec string) {
		if spec == "" || seen[spec] {
			return
		}
		seen[spec] = true
		out = append(out, store.CodeEdge{FromPath: rel, Kind: "import", ToName: spec})
	}
	for _, line := range lines {
		if m := importFromRe.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
		if m := importBareRe.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
		for _, m := range requireRe.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
	}
	return out
}

// callWords are the identifiers that look like calls but are syntax: a
// keyword, a literal constructor, or the declaration itself.
var callWords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"function": true, "return": true, "typeof": true, "new": true,
	"await": true, "yield": true, "else": true, "do": true, "super": true,
	"instanceof": true, "delete": true, "void": true, "throw": true,
	"case": true, "in": true, "of": true, "as": true, "constructor": true,
	"String": true, "Number": true, "Boolean": true, "Object": true,
	"Array": true, "Promise": true, "Map": true, "Set": true, "Date": true,
	"RegExp": true, "Error": true, "JSON": true, "Math": true,
}

var callRe = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)

// methodDeclRe catches definition lines that would otherwise read as calls:
// `connect() {`, `async mount(ctx) {` — an indented ident(...) followed by a
// return-type or a brace. `return useAuth()` doesn't match (the ident sits
// after the keyword, not directly before the paren), so real calls stay.
var methodDeclRe = regexp.MustCompile(`^\s*(?:async\s+)?[A-Za-z_$][A-Za-z0-9_$]*\s*(?:<[^>]*>)?\s*\([^)]*\)\s*(?::[^=]+)?\s*[{;]\s*$`)

// extractCalls maps each `ident(` to the declaration whose line range encloses
// it (file-level calls belong to ""). Name-matching only: ToPath stays "" and
// resolution happens at query time through code_symbols — a heuristic, and
// documented as one.
func extractCalls(rel string, lines []string, decls []decl) []store.CodeEdge {
	seen := map[string]bool{}
	var out []store.CodeEdge
	declLines := map[int]bool{}
	for _, d := range decls {
		declLines[d.Line] = true
	}
	di := 0 // monotonic cursor into decls — lines are visited in order
	for i, line := range lines {
		lineNo := i + 1
		// A declaration's own line names itself in `function foo(` — not a call.
		if declLines[lineNo] {
			continue
		}
		// Nor is a method definition (`connect() {`) a call to itself.
		if methodDeclRe.MatchString(line) {
			continue
		}
		for di+1 < len(decls) && decls[di+1].Line <= lineNo {
			di++
		}
		from := ""
		if len(decls) > 0 && di < len(decls) && decls[di].Line <= lineNo {
			from = decls[di].Name
		}
		for _, m := range callRe.FindAllStringSubmatch(line, -1) {
			callee := m[1]
			if callWords[callee] {
				continue
			}
			key := from + "\x00" + callee
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, store.CodeEdge{
				FromPath: rel, FromName: from, Kind: "call", ToName: callee,
			})
		}
	}
	return out
}

// maxChunkBytes bounds one indexed body: past it a declaration splits on line
// boundaries (same Name, continuing StartLine). Small enough that FTS hits
// point at a tight region, large enough that a normal component is one hit.
const maxChunkBytes = 4096

// chunkFile carves the file into content-addressed bodies: a chunk per
// declaration (the syntactic unit we have without a parser), the preamble
// before the first declaration under the file's base name, and hard splits for
// declarations that outgrow maxChunkBytes. Whitespace-only chunks drop.
func chunkFile(rel string, lines []string, decls []decl) []store.CodeChunk {
	base := path.Base(rel)
	nameAt := func(lineNo int) string {
		name := base
		for _, d := range decls {
			if d.Line > lineNo {
				break
			}
			name = d.Name
		}
		return name
	}

	var out []store.CodeChunk
	var body strings.Builder
	start := 1
	flush := func() {
		text := body.String()
		body.Reset()
		if strings.TrimSpace(text) == "" {
			return
		}
		sum := sha256.Sum256([]byte(text))
		out = append(out, store.CodeChunk{
			Path: rel, Hash: hex.EncodeToString(sum[:]),
			Name: nameAt(start), StartLine: start, Body: text,
		})
	}
	declAt := map[int]bool{}
	for _, d := range decls {
		declAt[d.Line] = true
	}
	// Lines carry their own trailing newline (so chunk boundaries keep the
	// separator), except the split's final "" — src ended with "\n" and the
	// previous line already owns it. That makes the bodies re-join the file
	// byte-for-byte, the invariant TestAnalyze pins.
	for i, line := range lines {
		isLast := i == len(lines)-1
		if isLast && line == "" {
			continue
		}
		lineNo := i + 1
		if body.Len() > 0 && (declAt[lineNo] || body.Len()+len(line)+1 > maxChunkBytes) {
			flush()
		}
		if body.Len() == 0 {
			start = lineNo
		}
		body.WriteString(line)
		if !isLast {
			body.WriteByte('\n')
		}
	}
	flush()
	return out
}

// fileAnalysis is everything one file contributes: symbols (decls), chunks,
// and both edge kinds with import resolution left pending (ToPath "").
type fileAnalysis struct {
	Decls   []decl
	Symbols []store.CodeSymbol
	Chunks  []store.CodeChunk
	Edges   []store.CodeEdge
}

// Analyze is the extractor's seam — the one function a real parser (tree-sitter
// via cgo or WASM, when the build story allows it) would replace wholesale.
// The file's lines are scanned once; decls, symbols, chunks, imports, and
// calls all derive from that single pass's inputs.
func Analyze(rel, src string) fileAnalysis {
	lines := strings.Split(src, "\n")
	decls := scanDecls(lines)
	a := fileAnalysis{Decls: decls}
	for _, d := range decls {
		a.Symbols = append(a.Symbols, store.CodeSymbol{Path: rel, Name: d.Name, Kind: d.Kind, Line: d.Line})
	}
	a.Chunks = chunkFile(rel, lines, decls)
	a.Edges = append(extractImports(rel, lines), extractCalls(rel, lines, decls)...)
	return a
}

// resolveLocal turns an import specifier into a workspace path when it is one:
// relative specifiers resolve against fromPath's directory with the usual
// candidate extensions and index files, and only count when the candidate is
// actually in the indexed file set. Packages and unresolvable specifiers keep
// ToPath "" — the edge still records the dependency.
func resolveLocal(fromPath, spec string, fileSet map[string]string) string {
	if !strings.HasPrefix(spec, ".") {
		return ""
	}
	base := path.Join(path.Dir(fromPath), spec)
	for _, cand := range []string{
		base, base + ".ts", base + ".tsx", base + ".js", base + ".jsx",
		path.Join(base, "index.ts"), path.Join(base, "index.tsx"),
		path.Join(base, "index.js"), path.Join(base, "index.jsx"),
	} {
		if _, ok := fileSet[cand]; ok {
			return cand
		}
	}
	return ""
}
