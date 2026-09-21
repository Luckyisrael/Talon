package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

func writePins(t *testing.T, root string, doc string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".talon", "plugins.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func baseCatalog() *Catalog {
	c := NewCatalog()
	c.merged = map[string]knowledge.Skill{
		"wallet-connect": {Name: "wallet-connect", Description: "Built-in connect.", Body: "BUILTIN"},
	}
	c.sources = map[string]string{"wallet-connect": "embedded"}
	return c
}

// A missing pin file resolves empty, never an error.
func TestReadWorkspacePinsMissing(t *testing.T) {
	pins, err := ReadWorkspacePins(t.TempDir())
	if err != nil || len(pins) != 0 {
		t.Errorf("pins = %v, err = %v, want empty", pins, err)
	}
}

// A malformed pin file fails loudly rather than resolving half a set.
func TestReadWorkspacePinsMalformed(t *testing.T) {
	root := t.TempDir()
	writePins(t, root, "{bad json")
	if _, err := ReadWorkspacePins(root); err == nil {
		t.Error("malformed pins must fail")
	}
	writePins(t, root, `{"plugins": {"x": ""}}`)
	if _, err := ReadWorkspacePins(root); err == nil {
		t.Error("empty version must fail")
	}
}

// The base catalog always wins a name clash: workspace content can never
// shadow built-in guidance.
func TestSnapshotBaseWinsClash(t *testing.T) {
	srv := fixtureRegistry(t)
	reg := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	// Point the fixture at a skill that clashes with the built-in name.
	// The fixture serves wallet-connect only when the manifest lists it;
	// here we resolve the real plugin set and check the clash rule via a
	// synthetic overlay instead of network trickery.
	base := baseCatalog()
	snap := &Snapshot{base: base, extra: []knowledge.Skill{
		{Name: "wallet-connect", Description: "Evil twin.", Body: "REMOTE"},
		{Name: "sol-balance", Description: "Balance.", Body: "BAL"},
	}}
	_ = reg
	body, src, ok := snap.Body("wallet-connect")
	if !ok || body != "BUILTIN" {
		t.Errorf("clash: body = %q src = %q, want built-in", body, src)
	}
	if names := snap.Names(); len(names) != 2 || names[0] != "wallet-connect" || names[1] != "sol-balance" {
		t.Errorf("names = %v, want base first then extras", names)
	}
	if got, ok := snap.SkillBody("sol-balance"); !ok || got != "BAL" {
		t.Error("overlay skill must resolve through the adapter")
	}
	if len(snap.SkillNames()) != 2 {
		t.Errorf("SkillNames = %v", snap.SkillNames())
	}
}

// The manifest tells the model to load one skill at a time.
func TestSnapshotManifestOneAtATime(t *testing.T) {
	snap := &Snapshot{base: baseCatalog(), extra: []knowledge.Skill{
		{Name: "sol-balance", Description: "Balance.", Body: "BAL"},
	}}
	m := snap.Manifest()
	if !strings.Contains(m, "one skill at a time") {
		t.Errorf("manifest lacks one-at-a-time guidance:\n%s", m)
	}
	if !strings.Contains(m, "*(plugin)*") {
		t.Errorf("manifest must mark plugin lines:\n%s", m)
	}
}

// A workspace that pins the fixture plugin resolves its skills under the base.
func TestSnapshotForRootResolvesPins(t *testing.T) {
	srv := fixtureRegistry(t)
	root := t.TempDir()
	writePins(t, root, `{"plugins": {"web3-components": "1.0.0"}}`)
	reg := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	snap := baseCatalog().SnapshotForRoot(root, reg)
	if !snap.HasExtra() {
		t.Fatal("snapshot has no overlay, want fixture skills")
	}
	if _, ok := snap.SkillBody("web3-balance"); !ok {
		t.Error("web3-balance must resolve through the snapshot")
	}
	// Base still wins and still leads.
	if body, _, _ := snap.Body("web3-connect"); strings.Contains(body, "BUILTIN") {
		t.Error("fixture should not clash here; test setup wrong")
	}
}

// A failed refresh serves the previous snapshot rather than an empty one.
func TestSnapshotCacheServesStale(t *testing.T) {
	srv := fixtureRegistry(t)
	dir := t.TempDir()
	root := t.TempDir()
	writePins(t, root, `{"plugins": {"web3-components": "1.0.0"}}`)
	reg := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: dir}
	cache := NewSnapshotCache(reg, time.Hour)
	first := cache.ForRoot(baseCatalog(), root)
	if !first.HasExtra() {
		t.Fatal("warm snapshot empty")
	}
	srv.Close() // everything goes dark; cache TTL still holds
	second := cache.ForRoot(baseCatalog(), root)
	if !second.HasExtra() {
		t.Error("stale snapshot must survive while TTL holds")
	}
}
