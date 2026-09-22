package store

import (
	"path/filepath"
	"testing"
)

// seedMemoryProject is the FK precondition the vector memory tests need
// (users → projects → project_memory at a known id, unlike store_test's
// auto-id seedProject).
func seedMemoryProject(t *testing.T, s *Store, id int64) {
	t.Helper()
	if _, err := s.db.Exec("INSERT OR IGNORE INTO users (id, email, password_hash) VALUES (?, ?, 'x')", id, "u"+filepath.Base(t.Name())+"@t"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := s.db.Exec("INSERT OR IGNORE INTO projects (id, user_id, name, slug) VALUES (?, ?, ?, ?)", id, id, "p", "p"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

// setMemoryVector writes a hand-built little-endian vector (vecBytes — the
// real encoder) onto one row, bypassing the provider the way a completed
// sweep would leave it.
func setMemoryVector(t *testing.T, s *Store, id int64, vec []float32) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE project_memory SET embedding = ? WHERE id = ?`, vecBytes(vec), id); err != nil {
		t.Fatalf("set vector: %v", err)
	}
}

// TestSearchMemoryVectorRanksByCosine is the vector leg's core: ordered by
// similarity, filtered by type, with undecodable rows skipped rather than
// failing the recall.
func TestSearchMemoryVectorRanksByCosine(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "vecmem.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(46)
	seedMemoryProject(t, s, pid)

	for _, c := range []string{"aligned", "orthogonal"} {
		if err := s.AddMemory(pid, "decision", c, "{}"); err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}
	if err := s.AddMemory(pid, "warning", "other-type", "{}"); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	// Fetch the row ids (created_at ties break on id DESC in scans, so
	// resolve by content instead of assuming insert order).
	rowsByContent := func() map[string]int64 {
		t.Helper()
		out := map[string]int64{}
		rows, err := s.db.Query(`SELECT id, content FROM project_memory WHERE project_id = ?`, pid)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var content string
			if err := rows.Scan(&id, &content); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out[content] = id
		}
		return out
	}()
	if len(rowsByContent) != 3 {
		t.Fatalf("seeded %d rows, want 3", len(rowsByContent))
	}

	setMemoryVector(t, s, rowsByContent["aligned"], []float32{1, 0})
	setMemoryVector(t, s, rowsByContent["orthogonal"], []float32{0, 1})
	// Orthogonal too: it must stay out of the way of the cosine-order
	// assertion (the type-filter subtest below checks its exclusion, not
	// its score) — a [1,0] tie would resolve by recency, not similarity.
	setMemoryVector(t, s, rowsByContent["other-type"], []float32{0, 1})

	// Query along +x: "aligned" (cos 1) beats "orthogonal" (cos 0).
	hits, err := s.SearchMemoryVector(pid, []float32{1, 0}, "", 10)
	if err != nil {
		t.Fatalf("SearchMemoryVector: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3 (cos 0 rows still rank, they just rank last)", len(hits))
	}
	if hits[0].Content != "aligned" {
		t.Errorf("hits[0] = %q, want aligned (cos 1 first)", hits[0].Content)
	}
	if hits[1].Content == "aligned" {
		t.Errorf("aligned appears twice: %v", hits)
	}

	// Type filter mirrors the FTS leg.
	filtered, err := s.SearchMemoryVector(pid, []float32{1, 0}, "decision", 10)
	if err != nil {
		t.Fatalf("SearchMemoryVector(filter): %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("type filter: got %d hits, want 2", len(filtered))
	}
	for _, h := range filtered {
		if h.EntryType != "decision" {
			t.Errorf("filter leaked entry_type=%q", h.EntryType)
		}
	}

	// limit caps.
	capped, err := s.SearchMemoryVector(pid, []float32{1, 0}, "", 1)
	if err != nil {
		t.Fatalf("SearchMemoryVector(limit): %v", err)
	}
	if len(capped) != 1 {
		t.Errorf("limit=1 returned %d hits", len(capped))
	}
}

// TestSearchMemoryVectorSkipsBadRows: the read-path validations W4.3
// reserved — wrong dimensions (a model change mid-cache) and zero norms
// must drop the row, never error the query the lexical leg still answers.
func TestSearchMemoryVectorSkipsBadRows(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "vecbad.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(47)
	seedMemoryProject(t, s, pid)
	for _, c := range []string{"good", "mismatched", "zero"} {
		if err := s.AddMemory(pid, "fact", c, "{}"); err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}
	var goodID, mismatchID, zeroID int64
	rows, err := s.db.Query(`SELECT id, content FROM project_memory WHERE project_id = ?`, pid)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		switch content {
		case "good":
			goodID = id
		case "mismatched":
			mismatchID = id
		case "zero":
			zeroID = id
		}
	}
	rows.Close()

	setMemoryVector(t, s, goodID, []float32{3, 4}) // 2-dim, valid
	// 3-dim: dimension mismatch against the query.
	if _, err := s.db.Exec(`UPDATE project_memory SET embedding = ? WHERE id = ?`, vecBytes([]float32{1, 2, 3}), mismatchID); err != nil {
		t.Fatalf("set: %v", err)
	}
	// zero norm: no direction.
	if _, err := s.db.Exec(`UPDATE project_memory SET embedding = ? WHERE id = ?`, vecBytes([]float32{0, 0}), zeroID); err != nil {
		t.Fatalf("set: %v", err)
	}
	// garbage blob: not a multiple of 4.
	if _, err := s.db.Exec(`UPDATE project_memory SET embedding = x'ff0f' WHERE id = ?`, goodID); err != nil {
		t.Fatalf("set: %v", err)
	}
	// put good back after the garbage test — actually keep garbage ON good
	// and add a fourth valid row instead.
	var validID int64
	if err := s.AddMemory(pid, "fact", "valid", "{}"); err != nil {
		t.Fatalf("AddMemory: %v", err)
	}
	if err := s.db.QueryRow(`SELECT id FROM project_memory WHERE project_id = ? AND content = ?`, pid, "valid").Scan(&validID); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	setMemoryVector(t, s, validID, []float32{1, 0})

	hits, err := s.SearchMemoryVector(pid, []float32{1, 0}, "", 10)
	if err != nil {
		t.Fatalf("SearchMemoryVector: %v", err)
	}
	if len(hits) != 1 || hits[0].Content != "valid" {
		contents := make([]string, 0, len(hits))
		for _, h := range hits {
			contents = append(contents, h.Content)
		}
		t.Errorf("bad rows leaked: got %v, want [valid]", contents)
	}

	// Empty query is a no-op, not a full scan with a zero vector.
	if hits, err := s.SearchMemoryVector(pid, nil, "", 10); err != nil || len(hits) != 0 {
		t.Errorf("empty query: (%v, %v), want ([], nil)", hits, err)
	}
}

// TestSearchCodeChunksVector: the codebase_search leg — same cosine law,
// lexical hit shape, cosine-carrying Score, deterministic tie order.
func TestSearchCodeChunksVector(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "vecchunk.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	const pid = int64(48)
	if err := s.ReplaceFileIndex(pid, "src/a.ts", "h", []CodeChunk{
		{Path: "src/a.ts", Hash: "up", Name: "up", StartLine: 1, Body: "const up = 1"},
		{Path: "src/a.ts", Hash: "side", Name: "side", StartLine: 5, Body: "const side = 2"},
		{Path: "src/a.ts", Hash: "novector", Name: "novector", StartLine: 9, Body: "const novector = 3"},
	}, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for hash, vec := range map[string][]float32{
		"up":   {1, 0},
		"side": {0, 1},
	} {
		if _, err := s.db.Exec(`UPDATE code_chunks SET embedding = ? WHERE project_id = ? AND hash = ?`, vecBytes(vec), pid, hash); err != nil {
			t.Fatalf("set %s: %v", hash, err)
		}
	}

	hits, err := s.SearchCodeChunksVector(pid, []float32{1, 0}, 10)
	if err != nil {
		t.Fatalf("SearchCodeChunksVector: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (novector has no BLOB)", len(hits))
	}
	if hits[0].Name != "up" || hits[1].Name != "side" {
		t.Errorf("order = [%s %s], want [up side]", hits[0].Name, hits[1].Name)
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("cosine not carried: scores %f <= %f", hits[0].Score, hits[1].Score)
	}
	if hits, err := s.SearchCodeChunksVector(pid, nil, 10); err != nil || len(hits) != 0 {
		t.Errorf("empty query: (%v, %v), want ([], nil)", hits, err)
	}
}

// TestCosineUnit: the scoring primitive itself — including the ok=false
// contracts (dimension mismatch, zero norms, empty).
func TestCosineUnit(t *testing.T) {
	if sc, ok := cosine([]float32{1, 0}, []float32{1, 0}); !ok || sc < 0.999 {
		t.Errorf("identical: (%f, %v)", sc, ok)
	}
	if sc, ok := cosine([]float32{1, 0}, []float32{0, 1}); !ok || sc > 0.001 {
		t.Errorf("orthogonal: (%f, %v)", sc, ok)
	}
	if _, ok := cosine([]float32{1, 0}, []float32{1, 0, 0}); ok {
		t.Error("dimension mismatch must not compute")
	}
	if _, ok := cosine([]float32{0, 0}, []float32{1, 1}); ok {
		t.Error("zero norm must not compute")
	}
	if _, ok := cosine(nil, []float32{1}); ok {
		t.Error("empty query must not compute")
	}
}
