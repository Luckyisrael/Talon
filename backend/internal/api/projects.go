package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/identity"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/workspace"
)

type ProjectHandler struct {
	store       *store.Store
	workspace   *workspace.Manager
	workspaceDir string
}

func NewProjectHandler(s *store.Store, ws *workspace.Manager, workspaceDir string) *ProjectHandler {
	return &ProjectHandler{
		store:       s,
		workspace:   ws,
		workspaceDir: workspaceDir,
	}
}

type createProjectRequest struct {
	Name string `json:"name"`
}

func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	projects, err := h.store.ListProjects(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list projects")
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
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}

	// Generate unique identity from name
	id := identity.Generate(req.Name)
	id = identity.EnsureUnique(id, h.store.SlugExists, h.store.AndroidPackageExists)

	// Get template version for pinning
	templateVersion := h.workspace.TemplateVersion()

	project, err := h.store.CreateProject(userID, req.Name, templateVersion, id.Slug, id.AndroidPackage, id.IOSBundleID, id.Scheme)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project")
		return
	}

	// Create workspace via OverlayFS mount (instant, no npm install)
	mount, err := h.workspace.CreateWorkspace(project.ID)
	if err != nil {
		log.Printf("WARN: workspace creation failed for project %d: %v", project.ID, err)
	} else if mount != nil {
		// Render identity into config files after mount
		if err := identity.RenderConfigs(mount.UpperDir, id); err != nil {
			log.Printf("WARN: identity render failed for project %d: %v", project.ID, err)
		}
	}

	writeJSON(w, http.StatusCreated, project)
}

func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	project, err := h.store.GetProject(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	writeJSON(w, http.StatusOK, project)
}

type fileEntry struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (h *ProjectHandler) GetFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	// Read ONLY from the writable upper layer — no scaffold flag needed
	projectDir := h.workspace.GetUpperPath(id)
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, []fileEntry{})
		return
	}

	var files []fileEntry
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(projectDir, path)

		// Skip node_modules symlink and hidden files
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		files = append(files, fileEntry{
			Path:    relPath,
			Content: string(data),
		})
		return nil
	})

	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read project files")
		return
	}

	if files == nil {
		files = []fileEntry{}
	}

	writeJSON(w, http.StatusOK, files)
}

type writeFilesRequest struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

func (h *ProjectHandler) WriteFiles(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	var req writeFilesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Files) == 0 {
		writeError(w, http.StatusBadRequest, "files array required")
		return
	}

	// Write to the writable upper layer
	projectDir := h.workspace.GetUpperPath(id)

	for _, f := range req.Files {
		clean := filepath.Clean(f.Path)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			writeError(w, http.StatusBadRequest, "invalid file path: "+f.Path)
			return
		}

		// Block writes to identity fields
		if err := identity.BlockIdentityFields(clean, f.Content); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}

		fullPath := filepath.Join(projectDir, clean)
		dir := filepath.Dir(fullPath)

		if err := os.MkdirAll(dir, 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create directory")
			return
		}

		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write file")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *ProjectHandler) DeleteFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}

	clean := filepath.Clean(filePath)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		writeError(w, http.StatusBadRequest, "invalid file path")
		return
	}

	// Delete from the writable upper layer
	projectDir := h.workspace.GetUpperPath(id)
	fullPath := filepath.Join(projectDir, clean)

	absProject, _ := filepath.Abs(projectDir)
	absFull, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absFull, absProject) {
		writeError(w, http.StatusForbidden, "path outside project directory")
		return
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}

	if err := os.RemoveAll(fullPath); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete file")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *ProjectHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	var req struct {
		PlanJSON string `json:"plan_json"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.store.UpdateProjectPlan(id, req.PlanJSON); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update plan")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	if err := h.store.DeleteProject(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project")
		return
	}

	// Destroy workspace (unmount OverlayFS if mounted)
	h.workspace.DestroyWorkspace(id)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
