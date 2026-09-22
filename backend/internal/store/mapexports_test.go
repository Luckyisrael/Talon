package store

import (
	"path/filepath"
	"testing"
)

// TestSetFileExportsKeepsHash pins the map-view row contract (W4.6): the
// exports upsert never clobbers the hash, and a hash write never clobbers
// exports — reconcile's two per-file writes compose in either order at read
// time, and other tenants see nothing.
func TestSetFileExportsKeepsHash(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	if err := s.SetFileExports(pid, "src/a.ts", []string{"A", "A2"}, []string{"./x"}); err != nil {
		t.Fatalf("SetFileExports: %v", err)
	}
	if err := s.ReplaceFileIndex(pid, "src/a.ts", "h1", nil, nil, nil); err != nil {
		t.Fatalf("ReplaceFileIndex: %v", err)
	}
	// Reconcile's exports-first order on a later change: exports land again;
	// the hash still must not move.
	if err := s.SetFileExports(pid, "src/a.ts", []string{"A3"}, nil); err != nil {
		t.Fatalf("second SetFileExports: %v", err)
	}

	rows, err := s.CodeFilesWithExports(pid)
	if err != nil {
		t.Fatalf("CodeFilesWithExports: %v", err)
	}
	if len(rows) != 1 || rows[0].Path != "src/a.ts" {
		t.Fatalf("rows = %+v", rows)
	}
	if len(rows[0].Exports) != 1 || rows[0].Exports[0] != "A3" || len(rows[0].Imports) != 0 {
		t.Errorf("exports not upserted: %+v", rows[0])
	}
	hashes, err := s.CodeFileHashes(pid)
	if err != nil {
		t.Fatalf("CodeFileHashes: %v", err)
	}
	if hashes["src/a.ts"] != "h1" {
		t.Errorf("hash clobbered by exports upsert: %q", hashes["src/a.ts"])
	}

	other, err := s.CodeFilesWithExports(99999)
	if err != nil || len(other) != 0 {
		t.Errorf("tenant leak or error: rows=%+v err=%v", other, err)
	}
}

// TestMapColumnsUpgradePath pins the W4.6 migration: a pre-view database
// gains the columns AND has its hashes invalidated once (so reconcile
// backfills the map data); reopening an already-upgraded database preserves
// both — no full re-index on every restart.
func TestMapColumnsUpgradePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")

	// Build a pre-W4.6 database: full migrate, then swap code_files for the
	// old three-column shape holding one already-indexed row.
	s1, err := New(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	for _, q := range []string{
		`DROP TABLE code_files`,
		`CREATE TABLE code_files (project_id INTEGER NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, PRIMARY KEY (project_id, path))`,
		`INSERT INTO code_files (project_id, path, hash) VALUES (7, 'a.ts', 'OLDHASH')`,
	} {
		if _, err := s1.db.Exec(q); err != nil {
			t.Fatalf("prep: %v", err)
		}
	}
	s1.Close()

	// Upgrade: columns added, hashes invalidated → next reconcile refills.
	s2, err := New(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if h, err := s2.CodeFileHashes(7); err != nil || len(h) != 1 || h["a.ts"] != "" {
		t.Errorf("upgrade did not invalidate the hash: h=%+v err=%v", h, err)
	}
	if rows, err := s2.CodeFilesWithExports(7); err != nil || len(rows) != 1 || rows[0].Path != "a.ts" {
		t.Errorf("columns missing after upgrade: rows=%+v err=%v", rows, err)
	}
	// Backfill (what reconcile would do), then reopen: preserved.
	if err := s2.SetFileExports(7, "a.ts", []string{"keep"}, []string{"./k"}); err != nil {
		t.Fatalf("SetFileExports: %v", err)
	}
	if err := s2.ReplaceFileIndex(7, "a.ts", "NEWHASH", nil, nil, nil); err != nil {
		t.Fatalf("ReplaceFileIndex: %v", err)
	}
	s2.Close()

	s3, err := New(path)
	if err != nil {
		t.Fatalf("third open: %v", err)
	}
	defer s3.Close()
	if h, err := s3.CodeFileHashes(7); err != nil || h["a.ts"] != "NEWHASH" {
		t.Errorf("re-open invalidated a migrated hash: h=%+v err=%v", h, err)
	}
	rows, err := s3.CodeFilesWithExports(7)
	if err != nil || len(rows) != 1 || len(rows[0].Exports) != 1 || rows[0].Exports[0] != "keep" {
		t.Errorf("re-open lost map rows: rows=%+v err=%v", rows, err)
	}
}
