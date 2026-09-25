package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A pin edit invalidates the snapshot memo on the very next call —
// regardless of how much TTL remains. This is what makes the model's
// enable step readable by its own next read_skill instead of after the
// cache window.
func TestSnapshotCacheInvalidatesOnPinEdit(t *testing.T) {
	srv := fixtureRegistry(t)
	dir := t.TempDir()
	root := t.TempDir()
	writePins(t, root, `{"plugins": {}}`) // file exists, nothing pinned
	reg := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: dir}
	cache := NewSnapshotCache(reg, time.Hour) // TTL far away: only the edit can refresh

	first := cache.ForRoot(baseCatalog(), root)
	if first.HasExtra() {
		t.Fatal("empty pin set must resolve no overlay")
	}

	// Enable the fixture plugin and bump mtime past any coarse
	// filesystem granularity so the change cannot hide inside a second.
	writePins(t, root, `{"plugins": {"web3-components": "1.0.0"}}`)
	pinsPath := filepath.Join(root, ".talon", "plugins.json")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(pinsPath, future, future); err != nil {
		t.Fatal(err)
	}

	second := cache.ForRoot(baseCatalog(), root)
	if !second.HasExtra() {
		t.Fatal("pin edit must invalidate the memo immediately, TTL aside")
	}
	if _, _, ok := second.Body("web3-connect"); !ok {
		t.Error("newly pinned skill must resolve after the edit")
	}

	// Un-pinning must propagate the same way — both directions.
	writePins(t, root, `{"plugins": {}}`)
	later := time.Now().Add(4 * time.Second)
	if err := os.Chtimes(pinsPath, later, later); err != nil {
		t.Fatal(err)
	}
	third := cache.ForRoot(baseCatalog(), root)
	if third.HasExtra() {
		t.Error("removing the pin must drop the overlay immediately")
	}
}

// With no edit and a fresh TTL the memo still holds: the mtime check must
// not turn every render into a re-resolve.
func TestSnapshotCacheStillMemoizesWithoutEdits(t *testing.T) {
	srv := fixtureRegistry(t)
	root := t.TempDir()
	writePins(t, root, `{"plugins": {"web3-components": "1.0.0"}}`)
	reg := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	cache := NewSnapshotCache(reg, time.Hour)

	first := cache.ForRoot(baseCatalog(), root)
	srv.Close() // network gone; an unchanged memo must not even try
	second := cache.ForRoot(baseCatalog(), root)
	if first != second {
		t.Error("unchanged pins within TTL must serve the same memoized snapshot")
	}
	if !second.HasExtra() {
		t.Error("memoized overlay must survive")
	}
}
