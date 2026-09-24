package api

import (
	"net/http"
	"os"
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
	//
	// Plain `git diff HEAD` never shows untracked files — and a build turn's
	// most common result IS one (the file it created), so the §7 panel could
	// not list the turn's own work on any path (row 45's live browser E2E).
	// Diff through a THROWAWAY index that intent-adds the worktree
	// (`read-tree HEAD` + `add -N .`): new files come back as `+++ b/…`
	// sections while the workspace's real index — the one the agent's
	// git_status/git_diff tools read — stays untouched. Every step degrades
	// to the plain diff: the honest pre-existing surface, never a 500.
	runDiff := func(env []string) ([]byte, error) {
		cmd := exec.Command("git", "diff", "HEAD", "--", ".")
		cmd.Dir = dir
		if env != nil {
			cmd.Env = env
		}
		return cmd.CombinedOutput()
	}
	var out []byte
	var err error
	if idx, cerr := os.CreateTemp("", "talon-diff-index-*"); cerr != nil {
		out, err = runDiff(nil)
	} else {
		idxPath := idx.Name()
		_ = idx.Close()
		_ = os.Remove(idxPath) // git creates it; only the path matters
		defer os.Remove(idxPath)
		env := append(os.Environ(), "GIT_INDEX_FILE="+idxPath)
		indexed := true
		for _, argv := range [][]string{{"read-tree", "HEAD"}, {"add", "-N", "."}} {
			c := exec.Command("git", argv...)
			c.Dir = dir
			c.Env = env
			if o, e := c.CombinedOutput(); e != nil {
				_ = o
				indexed = false
				break
			}
		}
		if indexed {
			out, err = runDiff(env)
		} else {
			out, err = runDiff(nil)
		}
	}
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
