package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
func (s *Store) ReplaceFileIndex(projectID int64, path, hash string, chunks []CodeChunk, symbols []CodeSymbol, edges []CodeEdge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
			`INSERT INTO code_chunks (project_id, path, hash, name, start_line, body) VALUES (?, ?, ?, ?, ?, ?)`,
			projectID, path, c.Hash, c.Name, c.StartLine, c.Body,
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
