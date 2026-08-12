package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/identity"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/workspace"
)

type ProjectHandler struct {
	store        *store.Store
	workspace    *workspace.Manager
	workspaceDir string
}

func NewProjectHandler(s *store.Store, ws *workspace.Manager, workspaceDir string) *ProjectHandler {
	return &ProjectHandler{
		store:        s,
		workspace:    ws,
		workspaceDir: workspaceDir,
	}
}

// Limits on what the file endpoints will carry. The editor is for source code;
// without a ceiling a single generated asset can stall the IDE.
const (
	maxFileBytes = 2 << 20  // 2 MiB per file
	maxTreeBytes = 24 << 20 // 24 MiB for a whole tree response
	maxTreeFiles = 3000
)

// skipDirs never appear in the editor's file tree. node_modules is a symlink
// into the shared template; the rest is build output.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	".expo":        true,
	"android":      true,
	"ios":          true,
	"dist":         true,
	"build":        true,
	".staging":     true,
	"upper":        true,
	"work":         true,
}

type createProjectRequest struct {
	Name string `json:"name"`
}

// project loads a project and confirms the caller owns it.
//
// Every handler below goes through this. Previously only Create checked the
// user, so any signed-in account could read, edit, or delete any other account's
// project by guessing an id.
func (h *ProjectHandler) project(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return nil, false
	}

	project, err := h.store.GetProject(id)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		// Deliberately the same response either way: a 403 on someone else's
		// project would confirm that the id exists.
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// dir returns a project's workspace directory.
func (h *ProjectHandler) dir(projectID int64) string {
	if h.workspace != nil {
		return h.workspace.Path(projectID)
	}
	return filepath.Join(h.workspaceDir, strconv.FormatInt(projectID, 10))
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(GetUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your projects")
		return
	}
	if projects == nil {
		projects = []store.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "give the project a name")
		return
	}
	if len(req.Name) > 120 {
		writeError(w, http.StatusBadRequest, "that name is too long — 120 characters at most")
		return
	}

	id := identity.Generate(req.Name)
	id = identity.EnsureUnique(id, h.store.SlugExists, h.store.AndroidPackageExists)

	templateVersion := "unknown"
	if h.workspace != nil {
		templateVersion = h.workspace.TemplateVersion()
	}

	project, err := h.store.CreateProject(userID, req.Name, templateVersion,
		id.Slug, id.AndroidPackage, id.IOSBundleID, id.Scheme)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the project")
		return
	}

	if h.workspace != nil {
		dir, err := h.workspace.Create(project.ID)
		if err != nil {
			// The project row exists but has nowhere to build. Say so now rather
			// than letting the first prompt fail with something cryptic.
			log.Printf("project %d: workspace creation failed: %v", project.ID, err)
			writeError(w, http.StatusInternalServerError,
				"the project was created but its workspace could not be prepared — try deleting and recreating it")
			return
		}
		if err := identity.RenderConfigs(dir, id, h.workspace.TemplateDir()); err != nil {
			log.Printf("project %d: could not render identity into configs: %v", project.ID, err)
		}
	}

	writeJSON(w, http.StatusCreated, project)
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, project)
}

type fileEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Truncated marks a file too large to send in full. The editor shows it
	// read-only rather than pretending the content is complete.
	Truncated bool `json:"truncated,omitempty"`
}

func (h *ProjectHandler) GetFiles(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}

	projectDir := h.dir(project.ID)
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, []fileEntry{})
		return
	}

	files := make([]fileEntry, 0, 64)
	total := 0

	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree should not fail the whole listing.
			return nil
		}

		name := d.Name()

		if d.IsDir() {
			// Returning SkipDir is what actually prunes the walk. Without it the
			// walker descended into node_modules and read tens of thousands of
			// files before discarding them one at a time.
			if path != projectDir && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlinks are not followed: node_modules is one, and following it would
		// walk the shared template.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if len(files) >= maxTreeFiles || total >= maxTreeBytes {
			return filepath.SkipAll
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(projectDir, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		if info.Size() > maxFileBytes {
			files = append(files, fileEntry{Path: relPath, Truncated: true})
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		total += len(data)
		files = append(files, fileEntry{Path: relPath, Content: string(data)})
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read the project files")
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	writeJSON(w, http.StatusOK, files)
}

type writeFilesRequest struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

func (h *ProjectHandler) WriteFiles(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}

	var req writeFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "no files to save")
		return
	}

	projectDir := h.dir(project.ID)

	// Validate everything before writing anything, so a bad path in the middle of
	// a batch cannot leave the project half-saved.
	type pending struct {
		full    string
		content string
	}
	writes := make([]pending, 0, len(req.Files))

	for _, f := range req.Files {
		full, err := resolveInside(projectDir, f.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(f.Content) > maxFileBytes {
			writeError(w, http.StatusRequestEntityTooLarge,
				"that file is too large to save through the editor: "+f.Path)
			return
		}
		// The app's package name, bundle id and scheme are what tie a build to a
		// signing identity. Changing them from the editor breaks the installed
		// dev client, so they are managed by the platform.
		if err := identity.BlockIdentityFields(filepath.ToSlash(relOf(projectDir, full)), f.Content); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
		writes = append(writes, pending{full: full, content: f.Content})
	}

	for _, wr := range writes {
		if err := os.MkdirAll(filepath.Dir(wr.full), 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create the containing folder")
			return
		}
		if err := os.WriteFile(wr.full, []byte(wr.content), 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save the file")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "saved",
		"count":  len(writes),
	})
}

func (h *ProjectHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "which file?")
		return
	}

	projectDir := h.dir(project.ID)
	full, err := resolveInside(projectDir, filePath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := os.Lstat(full); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "that file does not exist")
		return
	}

	if err := os.RemoveAll(full); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete it")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *ProjectHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}

	var req struct {
		PlanJSON string `json:"plan_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}

	// Store valid JSON or nothing. A malformed plan silently breaks the plan
	// viewer on every later load.
	if trimmed := strings.TrimSpace(req.PlanJSON); trimmed != "" {
		if !json.Valid([]byte(trimmed)) {
			writeError(w, http.StatusBadRequest, "plan_json is not valid JSON")
			return
		}
		req.PlanJSON = trimmed
	}

	if err := h.store.UpdateProjectPlan(project.ID, req.PlanJSON); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the plan")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	project, ok := h.project(w, r)
	if !ok {
		return
	}

	if err := h.store.DeleteProject(project.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete the project")
		return
	}

	if h.workspace != nil {
		h.workspace.Destroy(project.ID)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// resolveInside turns a client-supplied relative path into an absolute path and
// proves it stays inside root.
//
// The previous check was strings.HasPrefix(abs, root), which passes for
// "/workspaces/34-other" when root is "/workspaces/34". filepath.Rel with an
// explicit ".." test has no such boundary bug.
func resolveInside(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errPath("a path is required")
	}
	if filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", errPath("that path is not allowed: " + rel)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", errPath("the project workspace could not be resolved")
	}

	full := filepath.Join(absRoot, filepath.FromSlash(rel))
	inside, err := filepath.Rel(absRoot, full)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", errPath("that path is outside the project: " + rel)
	}

	// Reject anything under a directory the editor does not manage — a write into
	// node_modules would land in the shared template through the symlink.
	for _, part := range strings.Split(filepath.ToSlash(inside), "/") {
		if skipDirs[part] {
			return "", errPath("the editor does not manage " + part + "/")
		}
	}

	return full, nil
}

func relOf(root, full string) string {
	if rel, err := filepath.Rel(root, full); err == nil {
		return rel
	}
	return full
}

type errPath string

func (e errPath) Error() string { return string(e) }
