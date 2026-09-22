package api

import (
	"github.com/luckyisrael/talon/backend/internal/index"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W4.6: the project map becomes a view over the code index wherever the
// store is reachable. Both seams share one shape — reconcile (hash-diff:
// only changed files re-parse), then select stored exports/imports, then
// render through the SAME renderer the walk uses — so flag-on bytes equal
// flag-off bytes for the same tree, and any failure degrades to the walk
// (the flag-off path, verbatim). With TALONSTUDIO_INDEX unset,
// index.Enabled() is false: no hook is set, no reconcile runs, and every
// path here is the old walk.

// projectMapTool returns the project_map tool, index-wired when the flag is
// on. Reconcile/IndexFiles are optional fields — nil keeps Execute on the
// walk exactly as before.
func (h *PromptHandler) projectMapTool(fs *tools.FS, projectID int64) *tools.ProjectMap {
	pm := &tools.ProjectMap{FS: fs}
	if !index.Enabled() || h.store == nil {
		return pm
	}
	pm.Reconcile = func() error {
		_, err := index.Reconcile(fs.Root(), projectID, h.store)
		return err
	}
	pm.IndexFiles = func() ([]tools.MapFile, error) {
		rows, err := h.store.CodeFilesWithExports(projectID)
		if err != nil {
			return nil, err
		}
		return mapFilesFromRows(rows), nil
	}
	return pm
}

// projectMapView renders the injected map: the index view when the flag is
// on (fresh by construction — reconcile runs at every render, which is what
// retires the one-shot snapshot), the walk otherwise or on any index failure.
func (h *PromptHandler) projectMapView(workspacePath string, projectID int64) string {
	if index.Enabled() && h.store != nil {
		if _, err := index.Reconcile(workspacePath, projectID, h.store); err == nil {
			if rows, err := h.store.CodeFilesWithExports(projectID); err == nil {
				return tools.ProjectMapTextFromIndex(mapFilesFromRows(rows))
			}
		}
	}
	return tools.ProjectMapText(workspacePath)
}

// mapFilesFromRows converts store rows to the tools boundary type — the
// StoreAdapter law: the tools package never imports the store.
func mapFilesFromRows(rows []store.CodeFileRow) []tools.MapFile {
	files := make([]tools.MapFile, 0, len(rows))
	for _, r := range rows {
		files = append(files, tools.MapFile{Path: r.Path, Exports: r.Exports, Imports: r.Imports})
	}
	return files
}
