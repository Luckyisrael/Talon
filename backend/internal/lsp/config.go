package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the workspace's .talon/config.json — the project-config keys
// the plan calls for (OpenCode parity). "lsp" overrides the server command
// (space-split, no shell: argv[0] plus args) for every language in that
// workspace; "formatter" names the workspace's formatter of record
// (run_prettier remains the default path — the key records the choice for
// tools that ask). A missing or unparsable file is the zero Config, never
// an error: project config is optional refinement, not a dependency.
type Config struct {
	LSP       string `json:"lsp"`
	Formatter string `json:"formatter"`
}

// LoadConfig reads root/.talon/config.json. Any failure — no file, bad
// JSON, unreadable — yields the zero Config so callers proceed on defaults.
func LoadConfig(root string) Config {
	var cfg Config
	raw, err := os.ReadFile(filepath.Join(root, ".talon", "config.json"))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

// Command splits an overridden server command into argv. Fields (not a
// shell) keeps config injection-free; an empty string means "no override".
func (c Config) Command() []string {
	if strings.TrimSpace(c.LSP) == "" {
		return nil
	}
	return strings.Fields(c.LSP)
}

// FormatterCommand splits the workspace's formatter key into argv — same
// Fields law (no shell). run_prettier consults it behind TALONSTUDIO_LSP
// and stays the formatter of record either way; empty means "prettier as
// shipped".
func (c Config) FormatterCommand() []string {
	if strings.TrimSpace(c.Formatter) == "" {
		return nil
	}
	return strings.Fields(c.Formatter)
}
