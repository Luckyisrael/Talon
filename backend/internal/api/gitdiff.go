package api

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GitDiff renders the workspace's working-tree changes — the §7 diff
// surface. The baseline is the provision-time template commit; the diff is
// what the agent (and the person) changed since. Plain `git diff` output,
// colored client-side.
func (h *PromptHandler) GitDiff(w http.ResponseWriter, r *http.Request) {
	project, ok := h.ownedProject(w, r, projectIDFromRoute(r))
	if !ok {
		return
	}
	if h.workspaceDir == "" {
		writeError(w, http.StatusServiceUnavailable, "workspaces are not configured on this server")
		return
	}
	// The workspace manager's directory is <workspaceDir>/<projectID> —
	// numeric, matching workspace.go's provision convention (the slug is
	// display-only); the provision-time git baseline is what HEAD points at.
	dir := filepath.Join(h.workspaceDir, strconv.FormatInt(project.ID, 10))
	if dir == "" {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	// No shell, no user input: fixed argv against the workspace.
	cmd := exec.Command("git", "diff", "HEAD", "--", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The most common honest case: not a git repo (workspace predates
		// the baseline, or git is missing). Say so instead of a 500.
		if strings.Contains(string(out), "not a git repository") {
			writeJSON(w, http.StatusOK, map[string]string{"diff": "", "note": "no git baseline for this workspace"})
			return
		}
		writeError(w, http.StatusInternalServerError, "the diff could not be read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"diff": string(out)})
}

// projectIDFromRoute reads the chi URL param.
func projectIDFromRoute(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	return id
}
