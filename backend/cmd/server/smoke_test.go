package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

// TestBootKnowledgeLoads reproduces the exact path resolution main() uses at
// boot and proves the knowledge layer loads against the real template. It
// guards the seam between path resolution and knowledge.Load: if the template dir
// moves or package.json stops parsing, builds would silently run with no rules
// or skills, and this catches that before a deploy does.
func TestBootKnowledgeLoads(t *testing.T) {
	projectRoot := findProjectRoot()
	templateDir := resolveDir("TEMPLATE_DIR",
		filepath.Join(projectRoot, "template"),
		filepath.Join(projectRoot, "backend", "template"))

	reg, err := knowledge.Load(filepath.Join(templateDir, "package.json"))
	if err != nil {
		t.Fatalf("knowledge.Load at boot path %q: %v", templateDir, err)
	}

	if len(reg.SkillNames()) == 0 {
		t.Fatal("no skills loaded at boot")
	}
	// The preinstalled list is read from the real template, not embedded; its
	// presence proves that dynamic read reached the rendered rules.
	if !strings.Contains(reg.RulesBlock(), "Preinstalled packages") {
		t.Error("rules block missing the preinstalled list read from the template")
	}
	if !strings.Contains(reg.Manifest(), "Skills available") {
		t.Error("manifest missing its header")
	}
}
