package api

import (
	"log"

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

// pluginPins reads the workspace pin file through the skills package.
type pluginPins struct{}

func (pluginPins) ReadPins(root string) (map[string]string, error) {
	return skills.ReadWorkspacePins(root)
}

// pluginSkillLibrary resolves read_skill for one workspace: the orchestrator
// snapshot (base catalog plus pinned plugin overlay, base wins clashes), or
// nil when no plugin registry is attached — callers fall back to the global
// catalog.
func (h *PromptHandler) pluginSkillLibrary(workspacePath string) tools.SkillLibrary {
	if h.orch == nil {
		return nil
	}
	if snap := h.orch.PluginSnapshot(workspacePath); snap != nil {
		return snap
	}
	return nil
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
