package store

import (
	"path/filepath"
	"testing"
)

func newCodeIndexStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "codeindex.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestCodeIndexRoundTrip covers the W4.1 store surface end to end: replace,
// hash set, FTS search (with the query-quoting guard), definitions, both edge
// directions, digest idempotence, and delete — the FTS external-content
// triggers are exercised implicitly, since a search that outlived a delete
// would mean the mirror drifted from code_chunks.
func TestCodeIndexRoundTrip(t *testing.T) {
	s := newCodeIndexStore(t)
	const pid = int64(3)

	appChunk := CodeChunk{Path: "src/app.ts", Hash: "h1", Name: "useAuth", StartLine: 6,
		Body: "export function useAuth() { return login() }"}
	authChunk := CodeChunk{Path: "src/auth.ts", Hash: "h2", Name: "auth.ts", StartLine: 1,
		Body: "export function login() { return sessionToken }"}
	symbols := []CodeSymbol{{Path: "src/app.ts", Name: "useAuth", Kind: "function", Line: 6}}
	edges := []CodeEdge{
		{FromPath: "src/app.ts", FromName: "useAuth", Kind: "call", ToName: "login"},
		{FromPath: "src/app.ts", Kind: "import", ToPath: "src/auth.ts", ToName: "./auth"},
	}
	if err := s.ReplaceFileIndex(pid, "src/app.ts", "HASHA", []CodeChunk{appChunk}, symbols, edges); err != nil {
		t.Fatalf("ReplaceFileIndex app: %v", err)
	}
	if err := s.ReplaceFileIndex(pid, "src/auth.ts", "HASHB", []CodeChunk{authChunk}, nil, nil); err != nil {
		t.Fatalf("ReplaceFileIndex auth: %v", err)
	}

	hashes, err := s.CodeFileHashes(pid)
	if err != nil {
		t.Fatalf("CodeFileHashes: %v", err)
	}
	if hashes["src/app.ts"] != "HASHA" || hashes["src/auth.ts"] != "HASHB" || len(hashes) != 2 {
		t.Errorf("hashes = %v", hashes)
	}

	// Search hits the right chunk by name and by body words.
	hits, err := s.SearchCodeChunks(pid, "useAuth", 10)
	if err != nil {
		t.Fatalf("SearchCodeChunks: %v", err)
	}
	if len(hits) == 0 || hits[0].Path != "src/app.ts" || hits[0].Name != "useAuth" || hits[0].StartLine != 6 {
		t.Errorf("useAuth hits = %+v", hits)
	}
	hits, err = s.SearchCodeChunks(pid, "sessionToken", 10)
	if err != nil || len(hits) == 0 || hits[0].Path != "src/auth.ts" {
		t.Errorf("sessionToken hits = %+v err=%v", hits, err)
	}
	// No alphanumeric tokens → no MATCH, no error.
	if hits, err = s.SearchCodeChunks(pid, "  ---  ", 10); err != nil || len(hits) != 0 {
		t.Errorf("punctuation query: hits=%v err=%v, want none", hits, err)
	}

	// FTS5 operator text must be quoted into literal tokens, never parsed as
	// syntax: these are exactly the shapes that break naive MATCH building.
	for _, hostile := range []string{`" OR 1=1 --`, `NEAR(a b)`, `path:secret`, `body*`, `((((`} {
		if _, err := s.SearchCodeChunks(pid, hostile, 5); err != nil {
			t.Errorf("query %q escaped the quoting guard: %v", hostile, err)
		}
	}

	if defs, err := s.CodeDefinitions(pid, "useAuth"); err != nil || len(defs) != 1 || defs[0].Line != 6 {
		t.Errorf("CodeDefinitions = %+v err=%v", defs, err)
	}
	if to, err := s.CodeEdgesTo(pid, "call", "login"); err != nil || len(to) != 1 || to[0].FromPath != "src/app.ts" {
		t.Errorf("CodeEdgesTo = %+v err=%v", to, err)
	}
	if from, err := s.CodeEdgesFrom(pid, "src/app.ts", ""); err != nil || len(from) != 2 {
		t.Errorf("CodeEdgesFrom (whole file) = %+v err=%v", from, err)
	}
	if named, err := s.CodeEdgesFrom(pid, "src/app.ts", "useAuth"); err != nil || len(named) != 1 {
		t.Errorf("CodeEdgesFrom (symbol) = %+v err=%v", named, err)
	}

	// Digest: idempotent replace → same digest; changed content → new digest.
	d1, err := s.CodeIndexDigest(pid)
	if err != nil || d1 == "" {
		t.Fatalf("CodeIndexDigest = %q err=%v", d1, err)
	}
	if err := s.ReplaceFileIndex(pid, "src/app.ts", "HASHA", []CodeChunk{appChunk}, symbols, edges); err != nil {
		t.Fatalf("idempotent replace: %v", err)
	}
	if d2, _ := s.CodeIndexDigest(pid); d2 != d1 {
		t.Error("replacing a file with identical rows moved the digest")
	}
	changed := appChunk
	changed.Body = "export function useAuth() { return authenticate() }"
	if err := s.ReplaceFileIndex(pid, "src/app.ts", "HASHA2", []CodeChunk{changed}, symbols, edges); err != nil {
		t.Fatalf("changed replace: %v", err)
	}
	if d3, _ := s.CodeIndexDigest(pid); d3 == d1 {
		t.Error("changed body did not move the digest")
	}

	// ClearEdgeTargets: the dependency record survives, the path goes.
	if err := s.ClearEdgeTargets(pid, "src/auth.ts"); err != nil {
		t.Fatalf("ClearEdgeTargets: %v", err)
	}
	from, _ := s.CodeEdgesFrom(pid, "src/app.ts", "")
	if len(from) != 2 {
		t.Fatalf("edges after clear = %+v", from)
	}
	// (the import edge's ToPath was src/auth.ts → must now be "")
	for _, e := range from {
		if e.Kind == "import" && e.ToPath != "" {
			t.Errorf("import ToPath = %q after ClearEdgeTargets", e.ToPath)
		}
	}

	// Delete removes the slice — and the FTS rows with it.
	if err := s.DeleteFileIndex(pid, "src/app.ts"); err != nil {
		t.Fatalf("DeleteFileIndex: %v", err)
	}
	if hits, err := s.SearchCodeChunks(pid, "useAuth", 10); err != nil || len(hits) != 0 {
		t.Errorf("search after delete = %+v err=%v, want empty (FTS trigger delete)", hits, err)
	}
	hashes, _ = s.CodeFileHashes(pid)
	if len(hashes) != 1 || hashes["src/auth.ts"] == "" {
		t.Errorf("hashes after delete = %v", hashes)
	}
	if to, _ := s.CodeEdgesTo(pid, "call", "login"); len(to) != 0 {
		t.Errorf("edges outlived their file: %+v", to)
	}
}

func TestFtsTokens(t *testing.T) {
	got := ftsTokens(`path: "a-b" c c d`)
	want := []string{"path", "a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("ftsTokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ftsTokens[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	long := ""
	for i := 0; i < 40; i++ {
		long += "tok "
	}
	if n := len(ftsTokens(long)); n > 16 {
		t.Errorf("ftsTokens kept %d tokens, want the 16 cap", n)
	}
}
