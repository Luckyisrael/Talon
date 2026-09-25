package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PluginSearchHit is one directory match: catalog metadata only (name,
// version, license, why it matched). Skill bodies never ride search
// results — they still reach the model only through read_skill after a
// pin exists, the same boundary read_plugin_file enforces for files.
type PluginSearchHit struct {
	Name        string
	Version     string
	Description string
	License     string
	Keywords    []string
	// Matched lists the query terms this entry scored on — the "why this
	// one" the model can repeat to the user.
	Matched []string
	// Pinned is true when the workspace already pins this plugin.
	Pinned bool
}

// PluginSearcher ranks the public plugin directory against a query.
// Implemented by the skills package's registry provider and bridged in
// the api layer — the same seam style as PluginFileFetcher, so this
// package stays free of that import.
type PluginSearcher interface {
	Search(query string, pins map[string]string) ([]PluginSearchHit, error)
}

// SearchPlugins is the discovery half of the plugin system: the model
// names the capability the request needs and gets the directory's ranked
// matches — enough metadata to choose ONE plugin to pin, never enough to
// bypass read_skill for bodies.
//
// Registered whenever a plugin registry is attached, pin or no pin:
// discovery has to work before anything is enabled, or the agent's only
// option is guessing which integration to hand-roll.
type SearchPlugins struct {
	WorkspaceRoot string
	Searcher      PluginSearcher
	Pins          PluginPinsReader
}

func (t *SearchPlugins) Name() string { return "search_plugins" }

func (t *SearchPlugins) Description() string {
	return "Search the public plugin directory for a third-party capability — a payment provider, " +
		"an external API, a UI pack — that the request needs and no available skill covers. Returns " +
		"matching plugins with name, version and license (metadata only, ranked by query fit). " +
		"Pick ONE plugin, add its pin to .talon/plugins.json to enable it, then load only the " +
		"skill that matches the job with read_skill. Never search speculatively, never pin a " +
		"plugin the request does not need, never preload skills from several."
}

func (t *SearchPlugins) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "The capability in plain words, e.g. \"card payment\", \"prediction market\", \"sol balance\". An empty query lists the whole directory."}
		}
	}`)
}

// ReadOnly (in readonly.go) lets search run alongside other reads in a
// turn — it only reads the directory index and the workspace pin file.

// Execute resolves pins (so results can say what is already enabled) and
// asks the searcher for ranked matches. Every failure mode degrades to an
// honest error: an unreachable directory is "unreachable", not "no
// plugins exist".
func (t *SearchPlugins) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	query := strings.TrimSpace(a.Query)
	if t.Searcher == nil || t.Pins == nil {
		return errResult("plugin directory search is not configured for this project."), nil
	}
	pins, err := t.Pins.ReadPins(t.WorkspaceRoot)
	if err != nil {
		return errResult("could not read plugin pins: %v", err), nil
	}
	hits, err := t.Searcher.Search(query, pins)
	if err != nil {
		return errResult("plugin directory unreachable: %v — plugins already pinned for this project keep working from their verified cache.", err), nil
	}
	if len(hits) == 0 {
		if query == "" {
			return ToolResult{Content: "The plugin directory is empty."}, nil
		}
		return ToolResult{Content: fmt.Sprintf("No plugin in the directory matches %q. Build the integration from the skills listed in the manifest — do not keep searching.", query)}, nil
	}
	var b strings.Builder
	if query == "" {
		fmt.Fprintf(&b, "The directory offers %d plugin(s):\n", len(hits))
	} else {
		fmt.Fprintf(&b, "%d plugin(s) match %q:\n", len(hits), query)
	}
	for _, h := range hits {
		fmt.Fprintf(&b, "\n- **%s@%s** — %s", h.Name, h.Version, h.Description)
		if h.License != "" {
			fmt.Fprintf(&b, " (%s)", h.License)
		}
		if len(h.Matched) > 0 {
			fmt.Fprintf(&b, " — matched: %s", strings.Join(h.Matched, ", "))
		}
		if h.Pinned {
			b.WriteString(" — already pinned for this project; its skills are listed under Workspace plugins")
		}
	}
	b.WriteString("\n\nTo enable one: add {\"plugins\": {\"<name>\": \"<version>\"}} to .talon/plugins.json " +
		"(write_file; read-only mode refuses pin edits). The pin file is what the user reviews — enable " +
		"only the plugin this request chose. Its skills then appear under Workspace plugins: " +
		"read_skill loads exactly one, then read_plugin_file when it names a reference.")
	return ToolResult{Content: strings.TrimSpace(b.String())}, nil
}
