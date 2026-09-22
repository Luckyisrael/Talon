package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsAndOverride(t *testing.T) {
	root := t.TempDir()
	if cfg := LoadConfig(root); cfg.LSP != "" || cfg.Formatter != "" || cfg.Command() != nil {
		t.Errorf("missing file must be the zero config: %+v", cfg)
	}

	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"lsp": "/opt/langservers/tsserver --stdio", "formatter": "biome"}`
	if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig(root)
	if cfg.LSP != "/opt/langservers/tsserver --stdio" || cfg.Formatter != "biome" {
		t.Errorf("config = %+v", cfg)
	}
	cmd := cfg.Command()
	if len(cmd) != 2 || cmd[0] != "/opt/langservers/tsserver" || cmd[1] != "--stdio" {
		t.Errorf("Command() = %q, want argv split without a shell", cmd)
	}
}

func TestLoadConfigBadJSONIsZero(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg := LoadConfig(root); cfg.LSP != "" || cfg.Formatter != "" {
		t.Errorf("unparsable config must degrade to zero: %+v", cfg)
	}
}

func TestEnabledFlag(t *testing.T) {
	for _, tc := range []struct {
		val  string
		want bool
	}{{"", false}, {"0", false}, {"v1", true}, {"true", true}, {"on", true}, {"yes", false}} {
		t.Setenv("TALONSTUDIO_LSP", tc.val)
		if got := Enabled(); got != tc.want {
			t.Errorf("Enabled(%q) = %v, want %v (same truthy set as every other flag)", tc.val, got, tc.want)
		}
	}
}
