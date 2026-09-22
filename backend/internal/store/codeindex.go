package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// ── W4: codebase index (lexical + graph layers) ──────────────────────
//
// Types and queries backing internal/index. Everything here is per-project
// (the tenant filter rides every statement); the FTS mirror joins back
// through code_chunks because it carries no project column — the same
// shape project_memory_fts uses. Shipped dark behind TALONSTUDIO_INDEX:
// tables exist from migrate on, but nothing writes or reads them until
// the flag is on.

// CodeChunk is one indexed slice of one file: a content-addressed body (the
// hash doubles as the embed-cache key when the vector layer lands) under the
// declaration it belongs to, so a hit can be reported as "name at path:line".
type CodeChunk struct {
	Path      string
	Hash      string // sha256 of Body — content-addressed
	Name      string // enclosing declaration, or the file's base name
	StartLine int    // 1-based line of Body's first line within Path
	Body      string
}

// CodeSymbol is one top-level declaration — the graph's vertices.
type CodeSymbol struct {
	Path string
	Name string
	Kind string // extractor's kind: "function" | "class" | "type" | "const" | …
	Line int    // 1-based declaration line
}

// CodeEdge is one directed link — the graph's edges. Edges belong to the
// FROM file (its imports, its symbol's calls), so a file's re-index replaces
// exactly its own edges; ToPath is filled when the target resolved inside the
// workspace (local import) and left "" when it did not (package, bare
// callee — resolution for calls happens by name at query time).
type CodeEdge struct {
	FromPath string
	FromName string // enclosing symbol; "" for file-level edges (imports)
	Kind     string // "import" | "call"
	ToPath   string
	ToName   string // module specifier (import) or callee identifier (call)
}

// CodeChunkHit is one search result: bm25 Score follows the FTS5 convention
// (lower = better); callers order by it already.
type CodeChunkHit struct {
	Path      string
	Name      string
	StartLine int
	Body      string
	Score     float64
}

// ReplaceFileIndex swaps one file's entire index slice atomically: its old
// chunks (and through the trigger, their FTS rows), symbols, and out-edges go
// away and the new ones land in one transaction. The content hash is
// upserted — that row is what lets a later Reconcile skip this file entirely.
// W4.3: embeddings whose content hash survives the swap ride along (the
// embed cache is keyed by chunk hash, so an unchanged chunk never re-embeds).
func (s *Store) ReplaceFileIndex(projectID int64, path, hash string, chunks []CodeChunk, symbols []CodeSymbol, edges []CodeEdge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Read this file's hash→vector pairs BEFORE the deletes below remove
	// them; re-insert applies them to surviving hashes, and everything else
	// lands NULL for the sweep to fill.
	carry := map[string][]byte{}
	rows, err := tx.Query(
		`SELECT hash, embedding FROM code_chunks WHERE project_id = ? AND path = ? AND embedding IS NOT NULL`,
		projectID, path)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h string
		var vec []byte
		if err := rows.Scan(&h, &vec); err != nil {
			rows.Close()
			return err
		}
		carry[h] = vec
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, q := range []struct {
		sql string
	}{
		{`DELETE FROM code_chunks WHERE project_id = ? AND path = ?`},
		{`DELETE FROM code_symbols WHERE project_id = ? AND path = ?`},
		{`DELETE FROM code_edges WHERE project_id = ? AND from_path = ?`},
	} {
		if _, err := tx.Exec(q.sql, projectID, path); err != nil {
			return err
		}
	}
	for _, c := range chunks {
		if _, err := tx.Exec(
			`INSERT INTO code_chunks (project_id, path, hash, name, start_line, body, embedding) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			projectID, path, c.Hash, c.Name, c.StartLine, c.Body, carry[c.Hash],
		); err != nil {
			return err
		}
	}
	for _, sym := range symbols {
		if _, err := tx.Exec(
			`INSERT INTO code_symbols (project_id, path, name, kind, line) VALUES (?, ?, ?, ?, ?)`,
			projectID, path, sym.Name, sym.Kind, sym.Line,
		); err != nil {
			return err
		}
	}
	for _, e := range edges {
		if _, err := tx.Exec(
			`INSERT INTO code_edges (project_id, from_path, from_name, kind, to_path, to_name) VALUES (?, ?, ?, ?, ?, ?)`,
			projectID, path, e.FromName, e.Kind, e.ToPath, e.ToName,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO code_files (project_id, path, hash) VALUES (?, ?, ?)
		 ON CONFLICT(project_id, path) DO UPDATE SET hash = excluded.hash`,
		projectID, path, hash,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteFileIndex removes a file's whole slice — the Merkle-ish complement of
// ReplaceFileIndex, called when the tree no longer contains the path.
func (s *Store) DeleteFileIndex(projectID int64, path string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM code_chunks WHERE project_id = ? AND path = ?`,
		`DELETE FROM code_symbols WHERE project_id = ? AND path = ?`,
		`DELETE FROM code_edges WHERE project_id = ? AND from_path = ?`,
		`DELETE FROM code_files WHERE project_id = ? AND path = ?`,
	} {
		if _, err := tx.Exec(q, projectID, path); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearEdgeTargets drops the resolved ToPath reference an edge held toward a
// deleted file — the dependency record (ToName) survives, the dead path does
// not. This is what keeps an incremental reconcile's edge state hash-equal to
// a fresh full build of the same tree.
func (s *Store) ClearEdgeTargets(projectID int64, path string) error {
	_, err := s.db.Exec(
		`UPDATE code_edges SET to_path = '' WHERE project_id = ? AND to_path = ?`,
		projectID, path)
	return err
}

// CodeFileRow is one file's map-view row (W4.6): its path plus the exports
// and imports the renderer prints — what the walk extracts, stored at index
// time so the map can render as a view instead of re-walking the tree.
type CodeFileRow struct {
	Path    string
	Exports []string
	Imports []string
}

// SetFileExports stores the map view's per-file exports/imports, upserting
// alongside — never touching — the hash: reconcile writes these BEFORE
// ReplaceFileIndex advances the hash, so a row whose hash matched can never
// be missing its map data. No-op on marshal failure only.
func (s *Store) SetFileExports(projectID int64, path string, exports, imports []string) error {
	ej, err := json.Marshal(exports)
	if err != nil {
		return err
	}
	ij, err := json.Marshal(imports)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO code_files (project_id, path, hash, exports, imports) VALUES (?, ?, '', ?, ?)
		 ON CONFLICT(project_id, path) DO UPDATE SET exports = excluded.exports, imports = excluded.imports`,
		projectID, path, string(ej), string(ij))
	return err
}

// CodeFilesWithExports returns every file's map row for one project in path
// order — the view's select. Corrupt JSON surfaces as an error: callers
// degrade to the walk rather than silently rendering a wrong map.
func (s *Store) CodeFilesWithExports(projectID int64) ([]CodeFileRow, error) {
	rows, err := s.db.Query(
		`SELECT path, exports, imports FROM code_files WHERE project_id = ? ORDER BY path`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeFileRow
	for rows.Next() {
		var r CodeFileRow
		var ej, ij string
		if err := rows.Scan(&r.Path, &ej, &ij); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ej), &r.Exports); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(ij), &r.Imports); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CodeFileHashes returns path → content hash: the reconcile comparison set.
func (s *Store) CodeFileHashes(projectID int64) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, hash FROM code_files WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

// ftsTokens turns a natural query into bare alphanumeric tokens — each will be
// individually quoted into the MATCH expression, so user text can never be
// read as FTS5 syntax (operators, column filters, unbalanced quotes).
func ftsTokens(query string) []string {
	seen := map[string]bool{}
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 && !seen[b.String()] {
			seen[b.String()] = true
			out = append(out, b.String())
		}
		b.Reset()
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	if len(out) > 16 {
		out = out[:16] // a monster query must not build a monster MATCH
	}
	return out
}

// SearchCodeChunks is the lexical layer: OR the tokens, let bm25 rank them,
// return the best chunks across the project. Porter stemming rides the
// tokenizer (walks → walk), which is exactly what a natural-language query
// wants over identifier-dense code.
func (s *Store) SearchCodeChunks(projectID int64, query string, limit int) ([]CodeChunkHit, error) {
	tokens := ftsTokens(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		quoted = append(quoted, `"`+t+`"`)
	}
	match := strings.Join(quoted, " OR ")
	rows, err := s.db.Query(`
		SELECT c.path, c.name, c.start_line, c.body, m.score
		FROM (SELECT rowid, bm25(code_chunks_fts) AS score
		      FROM code_chunks_fts WHERE code_chunks_fts MATCH ?) m
		JOIN code_chunks c ON c.id = m.rowid
		WHERE c.project_id = ?
		ORDER BY m.score
		LIMIT ?`, match, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeChunkHit
	for rows.Next() {
		var h CodeChunkHit
		if err := rows.Scan(&h.Path, &h.Name, &h.StartLine, &h.Body, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CodeDefinitions resolves a symbol name to its declarations — the graph's
// vertices lookup for codebase_graph.
func (s *Store) CodeDefinitions(projectID int64, name string) ([]CodeSymbol, error) {
	rows, err := s.db.Query(
		`SELECT path, name, kind, line FROM code_symbols WHERE project_id = ? AND name = ? ORDER BY path, line`,
		projectID, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeSymbol
	for rows.Next() {
		var sym CodeSymbol
		if err := rows.Scan(&sym.Path, &sym.Name, &sym.Kind, &sym.Line); err != nil {
			return nil, err
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// CodeEdgesTo finds edges pointing at a name — callers (kind "call") or
// importers of a path's basename edge-set (kind "import", ToName specifier).
func (s *Store) CodeEdgesTo(projectID int64, kind, toName string) ([]CodeEdge, error) {
	rows, err := s.db.Query(
		`SELECT from_path, from_name, kind, to_path, to_name FROM code_edges
		 WHERE project_id = ? AND kind = ? AND to_name = ? ORDER BY from_path, from_name`,
		projectID, kind, toName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeEdge
	for rows.Next() {
		var e CodeEdge
		if err := rows.Scan(&e.FromPath, &e.FromName, &e.Kind, &e.ToPath, &e.ToName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CodeEdgesFrom lists a file's out-edges, optionally narrowed to one
// enclosing symbol — the callees half of codebase_graph. fromName == "" means
// the whole file.
func (s *Store) CodeEdgesFrom(projectID int64, fromPath, fromName string) ([]CodeEdge, error) {
	query := `SELECT from_path, from_name, kind, to_path, to_name FROM code_edges
	          WHERE project_id = ? AND from_path = ?`
	args := []any{projectID, fromPath}
	if fromName != "" {
		query += ` AND from_name = ?`
		args = append(args, fromName)
	}
	query += ` ORDER BY from_name, kind, to_name`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeEdge
	for rows.Next() {
		var e CodeEdge
		if err := rows.Scan(&e.FromPath, &e.FromName, &e.Kind, &e.ToPath, &e.ToName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CodeEdgesFromSymbol is callees-by-name: every file's out-edges whose
// enclosing symbol carries this NAME, whatever path declared it. Call sites
// don't care which file a function lives in, only that it is the function —
// which is why name, not path, is the join here (from_path stays a filter
// only for the file-scoped form above).
func (s *Store) CodeEdgesFromSymbol(projectID int64, fromName string) ([]CodeEdge, error) {
	rows, err := s.db.Query(
		`SELECT from_path, from_name, kind, to_path, to_name FROM code_edges
		 WHERE project_id = ? AND from_name = ? ORDER BY from_path, kind, to_name`,
		projectID, fromName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CodeEdge
	for rows.Next() {
		var e CodeEdge
		if err := rows.Scan(&e.FromPath, &e.FromName, &e.Kind, &e.ToPath, &e.ToName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── W4.3: embed sweeps ────────────────────────────────────────────────
//
// The store declares its own slice of the provider interface (structural
// typing: any embed.Provider satisfies it) so store never imports embed —
// the StoreAdapter law, applied one layer deeper.

// Embedder is all a sweep needs from a provider: texts in, vectors out,
// positionally aligned.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// defaultEmbedBatch mirrors the provider's conservative input cap.
const defaultEmbedBatch = 64

// EmbedCodeChunks fills NULL vectors for a project's chunks — the sweep half
// of the embed cache (ReplaceFileIndex's carry handles the rest inside its
// own transaction). Idempotent: embedded rows are never selected, so a
// provider outage just retries next time. batch <= 0 means defaultEmbedBatch.
func (s *Store) EmbedCodeChunks(ctx context.Context, projectID int64, emb Embedder, batch int) (int, error) {
	return s.embedRows(ctx, emb, batch,
		`SELECT id, body FROM code_chunks WHERE project_id = ? AND embedding IS NULL ORDER BY id`,
		[]any{projectID},
		`UPDATE code_chunks SET embedding = ? WHERE id = ?`)
}

// EmbedMemories activates the existing project_memory.embedding column (in
// the schema since P1c): rows without a vector fill, vectored rows are never
// touched. Same idempotence law as the chunk sweep.
func (s *Store) EmbedMemories(ctx context.Context, projectID int64, emb Embedder, batch int) (int, error) {
	return s.embedRows(ctx, emb, batch,
		`SELECT id, content FROM project_memory WHERE project_id = ? AND embedding IS NULL ORDER BY id`,
		[]any{projectID},
		`UPDATE project_memory SET embedding = ? WHERE id = ?`)
}

// embedRows is the shared sweep shape: collect (id, text) pairs lacking a
// vector, embed in batches, update by primary key. Failures surface after
// whatever earlier batches landed — safe, because each row's vector depends
// only on that row's own text; the rest stay NULL and the next sweep resumes
// exactly where this one stopped.
func (s *Store) embedRows(ctx context.Context, emb Embedder, batch int, query string, args []any, update string) (int, error) {
	if batch <= 0 {
		batch = defaultEmbedBatch
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return 0, err
	}
	var ids []int64
	var texts []string
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		texts = append(texts, text)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(texts) == 0 {
		return 0, nil
	}
	total := 0
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := emb.Embed(ctx, texts[start:end])
		if err != nil {
			return total, fmt.Errorf("embed batch at %d: %w", start, err)
		}
		if len(vecs) != end-start {
			return total, fmt.Errorf("embed returned %d vectors for %d texts", len(vecs), end-start)
		}
		for i, vec := range vecs {
			if _, err := s.db.Exec(update, vecBytes(vec), ids[start+i]); err != nil {
				return total, err
			}
			total++
		}
	}
	return total, nil
}

// vecBytes is THE on-disk vector format: little-endian IEEE-754 float32s,
// 4 bytes each (math.Float32bits, then low byte first). The sqlite driver
// refuses []float32 outright, which is how this encoding earned its name —
// W4.5's cosine scan reads exactly this back through VecFromBytes: change
// one, change both.
func vecBytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		bits := math.Float32bits(f)
		b[4*i] = byte(bits)
		b[4*i+1] = byte(bits >> 8)
		b[4*i+2] = byte(bits >> 16)
		b[4*i+3] = byte(bits >> 24)
	}
	return b
}

// VecFromBytes decodes the format vecBytes writes. A length that isn't a
// whole number of float32s returns nil — a truncated blob is corruption, and
// a garbage vector is worse than none.
func VecFromBytes(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		j := 4 * i
		out[i] = math.Float32frombits(
			uint32(b[j]) | uint32(b[j+1])<<8 | uint32(b[j+2])<<16 | uint32(b[j+3])<<24)
	}
	return out
}

// CodeIndexDigest is the eval's hash-equal witness: a deterministic sha256
// over the project's whole index state (files, chunks, symbols, edges — each
// set in sorted order). Two index builds over identical trees must produce
// the same digest; that is the "incremental == full re-index" gate.
func (s *Store) CodeIndexDigest(projectID int64) (string, error) {
	var b strings.Builder

	files, err := s.CodeFileHashes(projectID)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Fprintf(&b, "f\t%s\t%s\n", p, files[p])
	}

	// emit renders one ordered query as "prefix<tab-joined columns>\n" lines.
	// SQLite hands integers back as int64; Scan converts them into the string
	// targets (via convertAssign), so every column round-trips textually.
	emit := func(query, prefix string, args ...any) error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			vals := make([]string, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			b.WriteString(prefix + strings.Join(vals, "\t") + "\n")
		}
		return rows.Err()
	}
	if err := emit(
		`SELECT path, hash, name, start_line FROM code_chunks WHERE project_id = ? ORDER BY path, start_line, hash`,
		"c\t", projectID); err != nil {
		return "", err
	}
	if err := emit(
		`SELECT path, name, kind, line FROM code_symbols WHERE project_id = ? ORDER BY path, line, name`,
		"s\t", projectID); err != nil {
		return "", err
	}
	if err := emit(
		`SELECT from_path, from_name, kind, to_path, to_name FROM code_edges WHERE project_id = ? ORDER BY from_path, from_name, kind, to_path, to_name`,
		"e\t", projectID); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}
