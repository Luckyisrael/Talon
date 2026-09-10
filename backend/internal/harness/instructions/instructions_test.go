package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The chain assembles broad → specific and carries both files when small.
func TestChainAssembles(t *testing.T) {
	ws := t.TempDir()
	global := filepath.Join(t.TempDir(), "AGENTS.md")
	_ = os.WriteFile(global, []byte("global rule"), 0o600)
	_ = os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("project rule"), 0o600)
	_ = os.MkdirAll(filepath.Join(ws, "app"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "app", "AGENTS.md"), []byte("app rule"), 0o600)

	c := Loader{WorkspaceRoot: ws, GlobalPath: global}.Load()
	text := c.Text()
	for _, want := range []string{"global rule", "project rule", "app rule", "Global conventions", "Project conventions", "app conventions"} {
		if !strings.Contains(text, want) {
			t.Errorf("chain missing %q:\n%s", want, text)
		}
	}
}

// Empty workspaces yield an empty chain — absence is normal, not an error.
func TestChainEmpty(t *testing.T) {
	c := Loader{WorkspaceRoot: t.TempDir()}.Load()
	if c.Text() != "" {
		t.Errorf("empty workspace produced a chain: %q", c.Text())
	}
}

// Under budget pressure, broader files are omitted whole before the most
// specific is truncated; both markers are honest.
func TestChainBudget(t *testing.T) {
	ws := t.TempDir()
	_ = os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte(strings.Repeat("b", 40000)), 0o600)
	_ = os.MkdirAll(filepath.Join(ws, "app"), 0o755)
	_ = os.WriteFile(filepath.Join(ws, "app", "AGENTS.md"), []byte(strings.Repeat("s", 40000)), 0o600)

	c := Loader{WorkspaceRoot: ws}.Load()
	text := c.Text()
	if strings.Contains(text, strings.Repeat("b", 1000)) {
		t.Error("fault: broader file survived while the chain was over budget")
	}
	if !strings.Contains(text, strings.Repeat("s", 1000)) {
		t.Error("fault: the most specific file was omitted instead of truncated")
	}
	if !strings.Contains(text, "broader instruction file(s) omitted") {
		t.Error("fault: omission not marked")
	}
	if !strings.Contains(text, "truncated at the context budget") {
		t.Error("fault: truncation not marked")
	}
	if len(text) > MaxChainBytes+2000 {
		t.Errorf("fault: chain over budget: %d bytes", len(text))
	}
}
