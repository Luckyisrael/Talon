package store

import (
	"math"
	"sort"
)

// ── W4.5: vector legs (in-process cosine) ─────────────────────────────
//
// sqlite-vec stays rejected (pure-Go build — W4.1's documented decision),
// so both vector searches brute-force cosine over the embedding BLOBs the
// W4.3 sweeps wrote. Workspace scale is the design point: thousands of
// rows × a query vector is microseconds-to-milliseconds in Go, and every
// decode goes through VecFromBytes — the format pair vecBytes writes.
//
// Score semantics differ per leg (bm25 on the lexical side, cosine here),
// and that is fine: the fusion reads ranks only, never raw scores.

// cosine returns the similarity and whether it could be computed at all.
// ok=false covers the two shapes a read path must never fuse: dimension
// mismatches (a model change mid-cache left rows of another width — the
// read-path validation W4.3's notes reserved) and zero-norm vectors (no
// direction, so no meaning).
func cosine(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0, false
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), true
}

// SearchMemoryVector is read_memory's vector leg: cosine over every
// vectored row of the project, best-first, decrypting content exactly the
// way SearchMemoryFTS does (the plaintext column is the searchable one —
// P2b keeps it for FTS until sqlcipher). entryType filters like the FTS
// leg; undecodable or mismatched rows are skipped, never errored — a stale
// vector must not fail a recall that the lexical leg can still answer.
// Scan order (created_at DESC, id DESC) is deterministic so cosine ties
// resolve by recency, matching the FTS leg's ordering intuition.
func (s *Store) SearchMemoryVector(projectID int64, query []float32, entryType string, limit int) ([]ProjectMemory, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	if len(query) == 0 {
		return nil, nil
	}
	q := `SELECT id, project_id, entry_type, content, content_encrypted, metadata, created_at, embedding
	      FROM project_memory WHERE project_id = ? AND embedding IS NOT NULL`
	args := []any{projectID}
	if entryType != "" {
		q += ` AND entry_type = ?`
		args = append(args, entryType)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		m     ProjectMemory
		score float64
	}
	var hits []scored
	for rows.Next() {
		var m ProjectMemory
		var enc string
		var blob []byte
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.EntryType, &m.Content, &enc, &m.Metadata, &m.CreatedAt, &blob); err != nil {
			return nil, err
		}
		if sc, ok := cosine(query, VecFromBytes(blob)); ok {
			m.Content = decryptContent(m.Content, enc)
			hits = append(hits, scored{m: m, score: sc})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]ProjectMemory, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.m)
	}
	return out, nil
}

// SearchCodeChunksVector is codebase_search's vector leg — the same
// brute-force cosine over code_chunks.embedding, with the lexical leg's
// hit shape (Score carries cosine here; fusion reads ranks only). Scan
// order is path/line so cosine ties are deterministic.
func (s *Store) SearchCodeChunksVector(projectID int64, query []float32, limit int) ([]CodeChunkHit, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if len(query) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT path, name, start_line, body, embedding
		 FROM code_chunks WHERE project_id = ? AND embedding IS NOT NULL
		 ORDER BY path, start_line, name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		hit CodeChunkHit
		sc  float64
	}
	var hits []scored
	for rows.Next() {
		var h CodeChunkHit
		var blob []byte
		if err := rows.Scan(&h.Path, &h.Name, &h.StartLine, &h.Body, &blob); err != nil {
			return nil, err
		}
		if sc, ok := cosine(query, VecFromBytes(blob)); ok {
			h.Score = sc
			hits = append(hits, scored{hit: h, sc: sc})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].sc > hits[j].sc })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]CodeChunkHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.hit)
	}
	return out, nil
}
