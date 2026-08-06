package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/store"
)

type ProjectHandler struct {
	store      *store.Store
	templateDir string
	workspaceDir string
}

func NewProjectHandler(s *store.Store, templateDir, workspaceDir string) *ProjectHandler {
	return &ProjectHandler{
		store:       s,
		templateDir: templateDir,
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

	project, err := h.store.CreateProject(userID, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project: "+err.Error())
		return
	}

	// Copy template to workspace
	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(project.ID, 10))
	if err := copyDir(h.templateDir, projectDir); err != nil {
		// Template copy failed but project was created — log but don't fail
		log.Printf("WARN: template copy failed for project %d: %v", project.ID, err)
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

	// Remove workspace directory
	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(id, 10))
	os.RemoveAll(projectDir)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(dstPath, data, 0644)
	})
}
