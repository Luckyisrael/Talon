package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PluginFileFetcher serves one hash-verified reference file from a plugin
// pinned for the workspace. Implemented by the skills package's registry
// provider; the interface keeps tools free of that import, matching how
// ReadSkill depends on the small SkillLibrary interface rather than a
// concrete.
type PluginFileFetcher interface {
	FetchPinnedFile(pins map[string]string, plugin, rel string) ([]byte, error)
}

// PluginPinsReader reads the workspace pin set (<root>/.talon/plugins.json).
// Implemented beside the fetcher; kept separate so tests can stub either.
type PluginPinsReader interface {
	ReadPins(root string) (map[string]string, error)
}

// ReadPluginFile fetches one starting-point file a skill points at
// (references/…). Skills carry the pattern inline; the reference is the
// full working version to adapt — never executed, only read, then copied
// into the workspace where it can be inspected like any other file.
//
// Guardrails: the plugin must be pinned for this workspace at the exact
// version served, the path must live under references/ with no escapes,
// and every byte is sha256-checked against the signed manifest before it
// is served. Unpinned plugins, non-reference paths, and hash failures all
// fail closed with an actionable message.
type ReadPluginFile struct {
	WorkspaceRoot string
	Fetcher       PluginFileFetcher
	Pins          PluginPinsReader
}

func (t *ReadPluginFile) Name() string { return "read_plugin_file" }

func (t *ReadPluginFile) Description() string {
	return "Read one verified starting-point file from a plugin pinned for this project " +
		"(references/…). Use it after read_skill, one file at a time, when the skill names its " +
		"full working version — adapt the code into the workspace, never execute it in place."
}

func (t *ReadPluginFile) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"plugin": {"type": "string", "description": "The plugin name, exactly as pinned in .talon/plugins.json"},
			"path": {"type": "string", "description": "The reference path the skill named, e.g. references/connect-button.tsx"}
		},
		"required": ["plugin", "path"]
	}`)
}

// ReadOnly (in readonly.go) lets read_plugin_file run alongside other reads
// in a turn — it only fetches verified bytes and touches nothing.

func (t *ReadPluginFile) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Plugin string `json:"plugin"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	plugin, rel := strings.TrimSpace(a.Plugin), strings.TrimSpace(a.Path)
	if plugin == "" || rel == "" {
		return errResult("read_plugin_file needs a plugin and a path, e.g. {\"plugin\": \"talon-mobile-ui\", \"path\": \"references/connect-button.tsx\"}."), nil
	}
	if t.Fetcher == nil || t.Pins == nil {
		return errResult("no plugins are configured for this project."), nil
	}
	pins, err := t.Pins.ReadPins(t.WorkspaceRoot)
	if err != nil {
		return errResult("could not read plugin pins: %v", err), nil
	}
	data, err := t.Fetcher.FetchPinnedFile(pins, plugin, rel)
	if err != nil {
		return errResult("%v", err), nil
	}
	const maxChars = 24_000
	text := string(data)
	if len(text) > maxChars {
		text = text[:maxChars] + fmt.Sprintf("\n\n…(truncated at %d chars; the file is served whole from cache on disk)", maxChars)
	}
	return ToolResult{Content: text}, nil
}
