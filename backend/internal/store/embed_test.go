package store

import (
	"context"
	"path/filepath"
	"testing"
)

// fakeEmbedder is the W4.3 test double: deterministic vectors derived from
// the text, counting every call — so tests pin both "what landed" and
// "nothing was re-embedded".
type fakeEmbedder struct {
	calls   int
	batched []int
	texts   [][]string
}

func (f *fakeEmbedder) Name() string { return "fake" }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	f.batched = append(f.batched, len(texts))
	f.texts = append(f.texts, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = []float32{float32(len(s)), float32(i)}
	}
	return out, nil
}

// failEmbedder always errors — the sweep must leave rows NULL and retry next
// time, never half-commit a batch it couldn't finish.
type failEmbedder struct{}

func (failEmbedder) Name() string { return "fail" }

func (failEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, context.DeadlineExceeded
}

// embFor reads one chunk's stored vector. found=false means no such row.
func embFor(t *testing.T, s *Store, projectID int64, path, hash string) (vector []byte, found bool) {
	t.Helper()
	rows, err := s.db.Query(
		`SELECT embedding FROM code_chunks WHERE project_id = ? AND path = ? AND hash = ?`,
		projectID, path, hash)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false
	}
	if err := rows.Scan(&vector); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return vector, true
}

// TestReplaceFileIndexCarriesEmbeddings is Cursor's embed-cache law at the
// row level: a chunk whose content hash survives a file edit keeps its vector
// through the delete-and-reinsert swap; a new hash starts NULL (for the
// sweep); a vanished chunk's row is simply gone.
func TestReplaceFileIndexCarriesEmbeddings(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "carry.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(41)

	replace := func(version string, chunks ...CodeChunk) error {
		return s.ReplaceFileIndex(pid, "src/a.ts", version, chunks, nil, nil)
	}
	if err := replace("v1",
		CodeChunk{Path: "src/a.ts", Hash: "chunkA", Name: "alpha", StartLine: 1, Body: "export const alpha = 1"},
		CodeChunk{Path: "src/a.ts", Hash: "chunkB", Name: "beta", StartLine: 5, Body: "export const beta = 2"},
	); err != nil {
		t.Fatalf("seed replace: %v", err)
	}
	// What a completed sweep would have written for chunkA.
	if _, err := s.db.Exec(
		`UPDATE code_chunks SET embedding = x'0102' WHERE project_id = ? AND hash = ?`,
		pid, "chunkA"); err != nil {
		t.Fatalf("seed vector: %v", err)
	}
	// The file changes: chunkA survives (re-positioned), chunkB vanishes,
	// chunkC is new.
	if err := replace("v2",
		CodeChunk{Path: "src/a.ts", Hash: "chunkA", Name: "alpha", StartLine: 3, Body: "export const alpha = 1"},
		CodeChunk{Path: "src/a.ts", Hash: "chunkC", Name: "gamma", StartLine: 9, Body: "export const gamma = 3"},
	); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	if got, found := embFor(t, s, pid, "src/a.ts", "chunkA"); !found || string(got) != "\x01\x02" {
		t.Errorf("chunkA vector = %v (found=%v), want carried x'0102'", got, found)
	}
	if got, found := embFor(t, s, pid, "src/a.ts", "chunkC"); !found || found && got != nil {
		t.Errorf("chunkC (new hash) vector = %v, want NULL for the sweep to fill", got)
	}
	if _, found := embFor(t, s, pid, "src/a.ts", "chunkB"); found {
		t.Error("chunkB row survived its vanished chunk")
	}
}

// TestEmbeddingExcludedFromDigest: vectors are orthogonal state — the
// incremental==full gate (CodeIndexDigest) must not notice them, or a swept
// incremental build would stop matching a fresh full build.
func TestEmbeddingExcludedFromDigest(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "digest.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(42)
	if err := s.ReplaceFileIndex(pid, "src/b.ts", "h1", []CodeChunk{
		{Path: "src/b.ts", Hash: "c1", Name: "one", StartLine: 1, Body: "const one = 1"},
	}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, err := s.CodeIndexDigest(pid)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE code_chunks SET embedding = x'ff00ff' WHERE project_id = ?`, pid); err != nil {
		t.Fatalf("set vector: %v", err)
	}
	after, err := s.CodeIndexDigest(pid)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if before != after {
		t.Errorf("digest moved when vectors landed: %s -> %s", before, after)
	}
}

// TestEmbedCodeChunksSweepIdempotent is the cache's second half: the sweep
// fills only NULL rows, never calls the provider when there's nothing to do,
// and after a file edit embeds ONLY the new hash (the carried one stays put).
func TestEmbedCodeChunksSweepIdempotent(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "sweep.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(43)
	seed := func(version string, chunks ...CodeChunk) error {
		return s.ReplaceFileIndex(pid, "src/c.ts", version, chunks, nil, nil)
	}
	if err := seed("v1",
		CodeChunk{Path: "src/c.ts", Hash: "keep", Name: "k", StartLine: 1, Body: "function keep() {}"},
		CodeChunk{Path: "src/c.ts", Hash: "also", Name: "a", StartLine: 3, Body: "function also() {}"},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	f := &fakeEmbedder{}
	n, err := s.EmbedCodeChunks(context.Background(), pid, f, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 || f.calls != 1 || f.batched[0] != 2 {
		t.Errorf("first sweep: n=%d calls=%v batches=%v, want 2/1/[2]", n, f.calls, f.batched)
	}
	// Second sweep: everything filled → provider must not be called at all.
	n, err = s.EmbedCodeChunks(context.Background(), pid, f, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 || f.calls != 1 {
		t.Errorf("idempotent sweep: n=%d calls=%d, want 0 and no new call", n, f.calls)
	}

	// File edit: "keep" carries its vector; "fresh" is new → exactly one
	// provider call with exactly that body.
	if err := seed("v2",
		CodeChunk{Path: "src/c.ts", Hash: "keep", Name: "k", StartLine: 1, Body: "function keep() {}"},
		CodeChunk{Path: "src/c.ts", Hash: "fresh", Name: "n", StartLine: 7, Body: "function fresh() {}"},
	); err != nil {
		t.Fatalf("edit: %v", err)
	}
	n, err = s.EmbedCodeChunks(context.Background(), pid, f, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 || f.calls != 2 {
		t.Errorf("post-edit sweep: n=%d calls=%d, want 1/2 (carried chunk must not re-embed)", n, f.calls)
	}
	if got := f.texts[len(f.texts)-1]; len(got) != 1 || got[0] != "function fresh() {}" {
		t.Errorf("provider got %v, want only the fresh chunk's body", got)
	}
	if got, found := embFor(t, s, pid, "src/c.ts", "keep"); !found || len(got) == 0 {
		t.Errorf("carried chunk lost its vector: %v (found=%v)", got, found)
	}
}

// TestEmbedSweepFailureLeavesRowsNull: a provider outage defers — rows stay
// NULL (retryable next sweep), never half-written.
func TestEmbedSweepFailureLeavesRowsNull(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "fail.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(44)
	if err := s.ReplaceFileIndex(pid, "src/d.ts", "h", []CodeChunk{
		{Path: "src/d.ts", Hash: "x", Name: "x", StartLine: 1, Body: "const x = 1"},
	}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.EmbedCodeChunks(context.Background(), pid, failEmbedder{}, 0); err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if got, found := embFor(t, s, pid, "src/d.ts", "x"); !found || got != nil {
		t.Errorf("vector = %v, want NULL after a failed sweep", got)
	}
	// Recovery: a healthy provider picks the same row up next time.
	f := &fakeEmbedder{}
	if n, err := s.EmbedCodeChunks(context.Background(), pid, f, 0); err != nil || n != 1 {
		t.Errorf("recovery sweep: n=%d err=%v, want 1/nil", n, err)
	}
}

// TestEmbedMemoriesSweepIdempotent activates the project_memory.embedding
// column the schema has carried since P1c: existing rows fill, second sweep
// is a no-op, new rows fill on the next pass — and the stored bytes decode
// through VecFromBytes back to exactly what the provider returned (the
// on-disk format law W4.5 fuses over).
func TestEmbedMemoriesSweepIdempotent(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(45)
	// project_memory carries a real FK — seed the rows it points at, the
	// way memory_recall_test does.
	if _, err := s.db.Exec("INSERT OR IGNORE INTO users (id, email, password_hash) VALUES (45, 'e45@t', 'x')"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.db.Exec("INSERT OR IGNORE INTO projects (id, user_id, name, slug) VALUES (45, 45, 'p45', 'p45')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for _, content := range []string{"we chose sqlite over postgres", "auth is JWT with refresh rotation"} {
		if err := s.AddMemory(pid, "decision", content, "{}"); err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	countEmbedded := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM project_memory WHERE project_id = ? AND embedding IS NOT NULL`,
			pid).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	f := &fakeEmbedder{}
	n, err := s.EmbedMemories(context.Background(), pid, f, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 2 || countEmbedded() != 2 {
		t.Errorf("first sweep: n=%d embedded=%d, want 2/2", n, countEmbedded())
	}

	// Format round-trip: BLOB → VecFromBytes must reproduce the provider's
	// vector ([len(text), 0]) — the encode/decode pair stays honest.
	var blob []byte
	if err := s.db.QueryRow(
		`SELECT embedding FROM project_memory WHERE project_id = ? AND content = ?`,
		pid, "we chose sqlite over postgres").Scan(&blob); err != nil {
		t.Fatalf("read vector: %v", err)
	}
	decoded := VecFromBytes(blob)
	if len(decoded) != 2 || decoded[0] != float32(len("we chose sqlite over postgres")) || decoded[1] != 0 {
		t.Errorf("round-trip = %v, want [%d 0]", decoded, len("we chose sqlite over postgres"))
	}
	if VecFromBytes(blob[:len(blob)-1]) != nil {
		t.Error("truncated blob must decode to nil, not a garbage vector")
	}

	if n, err = s.EmbedMemories(context.Background(), pid, f, 0); err != nil || n != 0 || f.calls != 1 {
		t.Errorf("idempotent sweep: n=%d calls=%d err=%v, want 0/1/nil", n, f.calls, err)
	}
	if err := s.AddMemory(pid, "note", "vector fusion lands in W4.5", "{}"); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if n, err = s.EmbedMemories(context.Background(), pid, f, 0); err != nil || n != 1 || countEmbedded() != 3 {
		t.Errorf("incremental sweep: n=%d embedded=%d err=%v, want 1/3/nil", n, countEmbedded(), err)
	}
}
