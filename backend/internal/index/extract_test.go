package index

import (
	"strings"
	"testing"
)

func TestEnabled(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "")
	if Enabled() {
		t.Error("unset flag must be off")
	}
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	if !Enabled() {
		t.Error("TALONSTUDIO_INDEX=v1 must be on")
	}
	t.Setenv("TALONSTUDIO_INDEX", "0")
	if Enabled() {
		t.Error(`"0" must be off`)
	}
}

const sampleTS = `import { login } from './auth'
import './styles.css'

const base = 1

export function useAuth() {
	const token = login()
	return token
}

export class Client {
	connect() {
		return useAuth()
	}
}

export interface Session {
	id: string
}
`

func TestScanDeclsColumnZeroOnly(t *testing.T) {
	lines := strings.Split(sampleTS, "\n")
	decls := scanDecls(lines)

	want := map[string]string{
		"base":    "const",
		"useAuth": "function",
		"Client":  "class",
		"Session": "interface",
	}
	got := map[string]string{}
	for _, d := range decls {
		got[d.Name] = d.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("decl %q kind = %q, want %q (all: %v)", name, got[name], kind, got)
		}
	}
	// `login()` and `const token` are inside bodies — indentation excludes them.
	if _, ok := got["token"]; ok {
		t.Error("an indented const was indexed as a project vertex")
	}
	// Declaration lines are in ascending line order (chunk boundaries depend on it).
	for i := 1; i < len(decls); i++ {
		if decls[i].Line <= decls[i-1].Line {
			t.Fatalf("decls not line-sorted: %v", decls)
		}
	}
}

func TestAnalyzeSymbolsChunksEdges(t *testing.T) {
	a := Analyze("src/app.ts", sampleTS)

	// Symbols mirror the decls.
	if len(a.Symbols) != 4 {
		t.Errorf("symbols = %v, want 4 (base, useAuth, Client, Session)", a.Symbols)
	}

	// Chunks: preamble (imports) under the file name, then one per decl.
	names := []string{}
	for _, c := range a.Chunks {
		if c.Path != "src/app.ts" {
			t.Errorf("chunk path = %q", c.Path)
		}
		if len(c.Hash) != 64 {
			t.Errorf("chunk hash %q is not sha256-hex", c.Hash)
		}
		names = append(names, c.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"app.ts", "base", "useAuth", "Client", "Session"} {
		if !strings.Contains(joined, want) {
			t.Errorf("chunk names %q missing %q", joined, want)
		}
	}
	// Start lines are 1-based and ascending, and bodies re-join the file.
	prev := 0
	for _, c := range a.Chunks {
		if c.StartLine <= prev {
			t.Errorf("chunk %q start %d not after %d", c.Name, c.StartLine, prev)
		}
		prev = c.StartLine
	}
	var rebuilt strings.Builder
	for _, c := range a.Chunks {
		rebuilt.WriteString(c.Body)
	}
	if rebuilt.String() != sampleTS {
		t.Error("chunks do not re-join the source byte-for-byte")
	}

	// Imports: two edges, specifiers as written, no resolution yet.
	imp := map[string]string{}
	for _, e := range a.Edges {
		if e.Kind == "import" {
			imp[e.ToName] = e.ToPath
		}
	}
	if len(imp) != 2 || imp["./auth"] != "" || imp["./styles.css"] != "" {
		t.Errorf("import edges = %v, want ./auth and ./styles.css with empty ToPath", imp)
	}

	// Calls: login() inside useAuth, useAuth() inside connect (enclosing Client).
	calls := map[string]string{} // enclosing → callee
	for _, e := range a.Edges {
		if e.Kind == "call" {
			calls[e.FromName+"\x00"+e.ToName] = e.ToName
		}
	}
	for _, want := range []string{"useAuth\x00login", "Client\x00useAuth"} {
		if _, ok := calls[want]; !ok {
			t.Errorf("missing call edge %q (have %v)", want, calls)
		}
	}
	// The declaration's own line must not self-edge.
	if _, ok := calls["useAuth\x00useAuth"]; ok {
		t.Error("useAuth self-edged from its own declaration line")
	}
}

func TestChunkOversizeSplitsSameName(t *testing.T) {
	var b strings.Builder
	b.WriteString("export function big() {\n")
	line := strings.Repeat("x", 80) + "\n"
	for b.Len() < maxChunkBytes*2 {
		b.WriteString("\t" + line)
	}
	b.WriteString("}\n")
	src := b.String()

	chunks := chunkFile("src/big.ts", strings.Split(src, "\n"), scanDecls(strings.Split(src, "\n")))
	if len(chunks) < 2 {
		t.Fatalf("oversized declaration produced %d chunk(s), want a split", len(chunks))
	}
	var rebuilt strings.Builder
	for _, c := range chunks {
		if c.Name != "big" {
			t.Errorf("split chunk named %q, want big", c.Name)
		}
		if len(c.Body) > maxChunkBytes+256 {
			t.Errorf("chunk body %d bytes exceeds the budget with line slack", len(c.Body))
		}
		rebuilt.WriteString(c.Body)
	}
	if rebuilt.String() != src {
		t.Error("oversize split does not re-join the source")
	}
}

func TestChunkHashesAreContentAddressed(t *testing.T) {
	lines := strings.Split(sampleTS, "\n")
	decls := scanDecls(lines)
	first := chunkFile("src/app.ts", lines, decls)
	second := chunkFile("src/app.ts", lines, decls)
	if len(first) != len(second) {
		t.Fatal("chunking is not deterministic in count")
	}
	for i := range first {
		if first[i].Hash != second[i].Hash || first[i].StartLine != second[i].StartLine {
			t.Fatalf("chunk %d not deterministic: %+v vs %+v", i, first[i], second[i])
		}
	}
	// One edited byte must move the affected chunk's hash (the embed-cache key
	// and the stored skip check both depend on this).
	edited := strings.Replace(sampleTS, "const base = 1", "const base = 2", 1)
	changed := false
	echunks := chunkFile("src/app.ts", strings.Split(edited, "\n"), scanDecls(strings.Split(edited, "\n")))
	for i := range first {
		if i < len(echunks) && first[i].Hash != echunks[i].Hash {
			changed = true
		}
	}
	if !changed {
		t.Error("no chunk hash moved after an edit — content addressing is broken")
	}
}

func TestResolveLocal(t *testing.T) {
	files := map[string]string{"src/app.ts": "h", "src/auth.ts": "h", "src/lib/index.tsx": "h"}
	cases := map[string]string{
		"./auth":        "src/auth.ts",
		"./auth.ts":     "src/auth.ts",
		"./lib":         "src/lib/index.tsx",
		"./nope":        "",
		"../outside":    "",
		"expo-router":   "", // packages never resolve locally
		"@scope/pkg":    "",
		"./lib/nope.ts": "",
	}
	for spec, want := range cases {
		if got := resolveLocal("src/app.ts", spec, files); got != want {
			t.Errorf("resolveLocal(%q) = %q, want %q", spec, got, want)
		}
	}
}
