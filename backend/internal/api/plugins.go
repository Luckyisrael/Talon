package api

import (
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
