package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/seekersstudio/backend/internal/knowledge"
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
	dir := filepath.Join(wsRoot, ".seeker", "skills", "local-debug")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(
		"---\nname: local-debug\ndescription: local conventions\n---\n\nLocal body."), 0o600)
	// A clashing name: embedded must win.
	_ = os.WriteFile(filepath.Join(wsRoot, ".seeker", "skills", "expo-router", "SKILL.md"), []byte(
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
	dir := filepath.Join(wsRoot, ".seeker", "skills")
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

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0) }

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
