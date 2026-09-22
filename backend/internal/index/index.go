// Package index is W4's indexer: the workspace parsed into symbols, chunks,
// and edges, kept fresh by content-hash reconciliation — the repo's
// reconciliation-on-touch law, with no filesystem watcher — and queried
// through the store's lexical (FTS5/bm25) and graph layers.
//
// Shipped dark behind TALONSTUDIO_INDEX: flag off, Reconcile is a no-op and
// the tools that read the index never register, so the model keeps grep-only
// behavior — no hard dependency, exactly as the plan's risk note requires
// (cgo/WASM tree-sitter and a vector provider both defer to later slices
// behind the seams this package already has: Analyze is the parser seam; the
// content-addressed chunk hashes are the future embed-cache keys).
package index

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// Enabled reads TALONSTUDIO_INDEX with the same truthy set every other
// W-flag uses — one convention, one reading, wherever it is checked.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_INDEX"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// Stats is what one Reconcile did — the numbers the time-budget and
// incremental gates read.
type Stats struct {
	Indexed   int // files parsed and written (new or content-changed)
	Removed   int // files whose rows were dropped (gone from the tree)
	Unchanged int // files whose stored hash matched — skipped entirely
	Bytes     int // bytes across the indexed (changed) files
}

// Reconcile brings the stored index in line with root. It walks exactly the
// file set the project map renders and ProjectFingerprint hashes
// (tools.IsMappableSource + ShouldSkipDir — the same-file-set law by
// construction), content-hashes every file, and touches only what moved:
// changed/new files are Analyzed and ReplaceFileIndex'd, vanished files are
// DeleteFileIndex'd (their inbound ToPath refs cleared), and unchanged files
// are never re-parsed or re-written — the Merkle-ish claim the
// incremental==full digest gate verifies.
//
// Hashing reads every file's bytes each call (content addressing needs the
// content); what the stored per-file hash buys is skipping parse, chunk, and
// SQL work for the unchanged majority, which is where the cost lives.
func Reconcile(root string, projectID int64, st *store.Store) (Stats, error) {
	var stats Stats
	if !Enabled() || st == nil {
		return stats, nil
	}
	stored, err := st.CodeFileHashes(projectID)
	if err != nil {
		return stats, err
	}

	type file struct{ src, hash string }
	current := map[string]file{}
	fileSet := map[string]string{} // rel → stored hash; membership set for resolveLocal
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if p != root && tools.ShouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || strings.HasPrefix(info.Name(), ".") {
			return nil
		}
		if !tools.IsMappableSource(info.Name()) {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		sum := sha256.Sum256(src)
		hash := hex.EncodeToString(sum[:])
		current[rel] = file{src: string(src), hash: hash}
		fileSet[rel] = hash
		return nil
	})

	for rel, f := range current {
		if stored[rel] == f.hash && stored[rel] != "" {
			stats.Unchanged++
			continue
		}
		a := Analyze(rel, f.src)
		for i := range a.Edges {
			if a.Edges[i].Kind == "import" {
				a.Edges[i].ToPath = resolveLocal(rel, a.Edges[i].ToName, fileSet)
			}
		}
		// W4.6: the map view's rows land BEFORE the hash upsert — if this
		// fails the hash never advances, so the next reconcile retries both.
		// The other order would leave a hash-matched row permanently missing
		// its exports (reconcile skips hash-matches forever).
		if err := st.SetFileExports(projectID, rel, tools.ExtractExports(f.src), tools.ExtractImports(f.src)); err != nil {
			return stats, err
		}
		if err := st.ReplaceFileIndex(projectID, rel, f.hash, a.Chunks, a.Symbols, a.Edges); err != nil {
			return stats, err
		}
		stats.Indexed++
		stats.Bytes += len(f.src)
	}
	for rel := range stored {
		if _, ok := current[rel]; ok {
			continue
		}
		if err := st.DeleteFileIndex(projectID, rel); err != nil {
			return stats, err
		}
		// Edges from SURVIVING files may have pointed here; they keep their
		// dependency record (ToName) but lose the dead path reference.
		if err := st.ClearEdgeTargets(projectID, rel); err != nil {
			return stats, err
		}
		stats.Removed++
	}
	return stats, nil
}
