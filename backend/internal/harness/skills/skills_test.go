package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// Embedded wins name clashes; workspace skills merge in; absence of the
// workspace directory is normal (empty contribution, no error).
func TestCatalogMerge(t *testing.T) {
	// A fake "embedded" provider with one skill.
	emb := sliceProvider{PName_: "embedded", Skills: []knowledge.Skill{{Name: "expo-router", Body: "embedded body", Description: "routing"}}}
	wsRoot := t.TempDir()
	fsProv := FSProvider{Root: wsRoot}

	cat := NewCatalog(emb, fsProv)
	if err := cat.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := cat.Names(); len(got) != 1 || got[0] != "expo-router" {
		t.Fatalf("empty workspace should still list embedded: %v", got)
	}

	// Author a workspace skill with the same name + a new one.
	dir := filepath.Join(wsRoot, ".talon", "skills", "local-debug")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(
		"---\nname: local-debug\ndescription: local conventions\n---\n\nLocal body."), 0o600)
	// A clashing name: embedded must win.
	_ = os.WriteFile(filepath.Join(wsRoot, ".talon", "skills", "expo-router", "SKILL.md"), []byte(
		"---\nname: expo-router\ndescription: hijack\n---\n\nHijacked body."), 0o600)

	_ = cat.Reload()
	if body, src, ok := cat.Body("expo-router"); !ok || body != "embedded body" || src != "embedded" {
		t.Errorf("fault: clash not won by embedded: %q %q %v", body, src, ok)
	}
	if body, src, ok := cat.Body("local-debug"); !ok || src != "workspace" || !contains(body, "Local body") {
		t.Errorf("healthy: workspace skill missing: %q %q %v", body, src, ok)
	}
	if m := cat.Manifest(); !contains(m, "local-debug") || !contains(m, "local conventions") {
		t.Errorf("manifest missing workspace skill: %s", m)
	}
}

// A malformed workspace skill (missing front matter) is skipped without
// sinking the catalog.
func TestCatalogSkipsMalformed(t *testing.T) {
	wsRoot := t.TempDir()
	dir := filepath.Join(wsRoot, ".talon", "skills")
	_ = os.MkdirAll(filepath.Join(dir, "broken"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "broken", "SKILL.md"), []byte("no front matter"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "good"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "good", "SKILL.md"), []byte(
		"---\nname: good\ndescription: fine\n---\n\nGood body."), 0o600)

	cat := NewCatalog(sliceProvider{PName_: "embedded"}, FSProvider{Root: wsRoot})
	_ = cat.Reload()
	if _, _, ok := cat.Body("broken"); ok {
		t.Error("fault: malformed skill entered the catalog")
	}
	if _, _, ok := cat.Body("good"); !ok {
		t.Error("healthy: valid skill missing")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// sliceProvider is a test provider with fixed skills.
type sliceProvider struct {
	PName_ string
	Skills []knowledge.Skill
}

func (p sliceProvider) Name() string { return p.PName_ }

func (p sliceProvider) Load() ([]knowledge.Skill, error) {
	out := make([]knowledge.Skill, len(p.Skills))
	copy(out, p.Skills)
	return out, nil
}

// FM-H6a (L20) — written first (TEST_RULES §3). The failure: NewCatalog
// shipped an empty merged map while Reload's own doc claimed it was
// "called at composition start" — and no caller existed (Run-4:
// skills/catalog=662 = the plugins guidance over a zero-skill base, so
// the prompt listed none of the 14 embedded skills and read_skill
// answered "none"; H6's "listed in the manifest, bodies only through
// read_skill" failed in both directions). The law: construction IS
// composition start — a catalog is populated the moment it exists, or
// every reader downstream silently serves nothing.
func TestNewCatalogLoadsAtConstruction(t *testing.T) {
	emb := sliceProvider{PName_: "embedded", Skills: []knowledge.Skill{
		{Name: "mobile-craft", Body: "the body", Description: "the design bar"},
	}}

	cat := NewCatalog(emb) // no Reload call: construction must load

	if got := cat.Names(); len(got) != 1 || got[0] != "mobile-craft" {
		t.Errorf("Names() = %v, want [mobile-craft] — the catalog was not loaded at construction", got)
	}
	if m := cat.Manifest(); !contains(m, "mobile-craft") || !contains(m, "the design bar") {
		t.Errorf("Manifest() = %q, want the skill listed with its description — the prompt would show no skills", m)
	}
	if body, _, ok := cat.Body("mobile-craft"); !ok || body != "the body" {
		t.Errorf("Body(mobile-craft) = %q, ok=%v — read_skill would answer none", body, ok)
	}
}

// FM-H6b (L20's completion) — written first (TEST_RULES §3). The
// failure: EmbeddedProvider.Load bridges knowledge.Registry →
// knowledge.Skill with {Name, Body} only, so Description — the one
// field Manifest() exists to render — never crosses. The previous test
// above proves nothing about it: its sliceProvider already carries a
// Description, so the assertion passed while production dropped the
// field. Measured symptom (Run-5): skills/catalog=1281 = 617 B of
// "- **name** — " rows with nothing after the dash + 662 B of
// unchanged plugins guidance, where L20 predicted ~5.5 KB; the
// manifest's own "match the description" instruction was therefore
// unsatisfiable and model-initiated read_skill stayed at 0 for a
// second run. The law: a bridge must carry every field its consumer
// renders — listing a skill with no description is the same discovery
// failure as not listing it, because the model still cannot choose.
func TestEmbeddedProviderCarriesDescriptionsIntoManifest(t *testing.T) {
	reg, err := knowledge.Load("")
	if err != nil {
		t.Fatalf("knowledge.Load: %v", err)
	}
	// The knowledge registry's own manifest is the reference: it renders
	// descriptions correctly (agent.go Manifest), so any divergence in
	// the catalog is the bridge's doing.
	want := manifestDescriptions(reg.Manifest())
	if len(want) == 0 {
		t.Fatal("registry manifest listed no skills — fixture is empty")
	}

	cat := NewCatalog(EmbeddedProvider{Registry: reg})
	got := manifestDescriptions(cat.Manifest())

	for name, desc := range want {
		if desc == "" {
			t.Errorf("registry rendered %q with no description — fixture problem", name)
		}
		if got[name] != desc {
			t.Errorf("skill %q: catalog description = %q, want %q — FM-H6b: the embedded bridge drops Description",
				name, got[name], desc)
		}
	}
	if len(got) != len(want) {
		t.Errorf("catalog listed %d skills, registry has %d — the bridge lost skills", len(got), len(want))
	}
}

// manifestDescriptions parses "- **name** — description" bullet lines
// out of either renderer's manifest. The separator is matched without a
// trailing space: a skill rendered with an EMPTY description is the
// last line, so TrimSpace has already eaten the space (FM-H6b's exact
// symptom — the parser must see an empty description as empty, not miss
// the skill entirely).
func manifestDescriptions(manifest string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(manifest, "\n") {
		if !strings.HasPrefix(line, "- **") {
			continue
		}
		rest := line[len("- **"):]
		i := strings.Index(rest, "** —")
		if i < 0 {
			continue
		}
		out[rest[:i]] = strings.TrimSpace(rest[i+len("** —"):])
	}
	return out
}
