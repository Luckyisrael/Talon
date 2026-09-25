package api

import (
	"log"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/harness/skills"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// pluginFetcher adapts the verified registry provider to the tool's fetch
// interface. The tools package stays free of the skills import; this file is
// the single place that bridges them.
type pluginFetcher struct{ reg skills.RegistryProvider }

func (f pluginFetcher) FetchPinnedFile(pins map[string]string, plugin, rel string) ([]byte, error) {
	return f.reg.FetchPinnedFile(pins, plugin, rel)
}

// pluginSearcher adapts the registry index search to the tool's search
// seam: metadata in, hits out — no skill bodies cross this bridge.
type pluginSearcher struct{ reg skills.RegistryProvider }

func (f pluginSearcher) Search(query string, pins map[string]string) ([]tools.PluginSearchHit, error) {
	hits, err := f.reg.Search(query, pins)
	if err != nil {
		return nil, err
	}
	out := make([]tools.PluginSearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, tools.PluginSearchHit{
			Name:        h.Name,
			Version:     h.Version,
			Description: h.Description,
			License:     h.License,
			Keywords:    h.Keywords,
			Matched:     h.Matched,
			Pinned:      h.Pinned,
		})
	}
	return out, nil
}

// pluginPins reads the workspace pin file through the skills package.
type pluginPins struct{}

func (pluginPins) ReadPins(root string) (map[string]string, error) {
	return skills.ReadWorkspacePins(root)
}

// liveSkillLibrary resolves read_skill for one workspace as a LIVE view:
// a per-call adapter over the orchestrator's snapshot cache rather than
// the snapshot frozen when the session's tools were built. Without it, a
// pin the model adds mid-session would only be readable after the cache
// TTL — the enable step and its read_skill would miss each other. The
// cache's pin-file mtime check keeps the network side memoized anyway.
type liveSkillLibrary struct {
	h    *PromptHandler
	root string
}

func (l liveSkillLibrary) SkillBody(name string) (string, bool) {
	snap := l.h.orch.PluginSnapshot(l.root)
	if snap == nil {
		return "", false
	}
	return snap.SkillBody(name)
}

func (l liveSkillLibrary) SkillNames() []string {
	snap := l.h.orch.PluginSnapshot(l.root)
	if snap == nil {
		return nil
	}
	return snap.SkillNames()
}

// pluginSkillLibrary resolves read_skill for one workspace: a live view
// over the orchestrator's snapshot (base catalog plus pinned plugin
// overlay, base wins clashes), or nil when no plugin registry is attached
// — callers fall back to the global catalog.
func (h *PromptHandler) pluginSkillLibrary(workspacePath string) tools.SkillLibrary {
	if h.orch == nil {
		return nil
	}
	if _, ok := h.orch.PluginRegistry(); !ok || strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	return liveSkillLibrary{h: h, root: workspacePath}
}

// pluginFileTool builds the reference-file fetcher for one workspace, or nil
// when no plugin registry is attached.
func (h *PromptHandler) pluginFileTool(workspacePath string) *tools.ReadPluginFile {
	if h.orch == nil {
		return nil
	}
	reg, ok := h.orch.PluginRegistry()
	if !ok {
		return nil
	}
	return &tools.ReadPluginFile{
		WorkspaceRoot: workspacePath,
		Fetcher:       pluginFetcher{reg: reg},
		Pins:          pluginPins{},
	}
}

// pluginSearchTool builds directory search for one workspace, or nil when
// no plugin registry is attached. Unlike the two tools above it needs no
// pins: its whole purpose is finding the plugin to pin.
func (h *PromptHandler) pluginSearchTool(workspacePath string) *tools.SearchPlugins {
	if h.orch == nil {
		return nil
	}
	reg, ok := h.orch.PluginRegistry()
	if !ok {
		return nil
	}
	return &tools.SearchPlugins{
		WorkspaceRoot: workspacePath,
		Searcher:      pluginSearcher{reg: reg},
		Pins:          pluginPins{},
	}
}

// registerPluginTools layers pinned plugins' declared tools (W11 item 2)
// into the registry under plugin__<plugin>__<tool> — the same
// per-workspace, pins-gated layering read_plugin_file already uses, now
// with executable declarations. The skills provider does the heavy part
// (pin exactness, hash verification, read-only contract, skip-logs); here
// they just register. No pins, no plugin registry attached, or a resolve
// failure: no tools, byte-identical to today.
func (h *PromptHandler) registerPluginTools(reg *tools.Registry, workspacePath string) {
	if h.orch == nil {
		return
	}
	pr, ok := h.orch.PluginRegistry()
	if !ok {
		return
	}
	pins, err := skills.ReadWorkspacePins(workspacePath)
	if err != nil {
		// Malformed pins already fail loudly at every other reader; tools
		// are additive, so log and register none rather than half.
		log.Printf("plugins: %v", err)
		return
	}
	decls, err := pr.PinnedTools(pins)
	if err != nil {
		log.Printf("plugins: tool declarations unavailable: %v", err)
		return
	}
	for _, d := range decls {
		reg.Register(&tools.PluginTool{
			Plugin: d.Plugin,
			Remote: d.Name,
			Desc:   d.Description,
			Schema: d.InputSchema,
			Argv:   d.Command,
			Dir:    d.Dir,
		})
	}
}
