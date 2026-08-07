package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/store"
)

const scaffoldManifest = ".scaffold-manifest.json"

type ProjectHandler struct {
	store       *store.Store
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

	// Copy template to workspace and write scaffold manifest
	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(project.ID, 10))
	if err := copyDirWithManifest(h.templateDir, projectDir); err != nil {
		log.Printf("WARN: template copy failed for project %d: %v", project.ID, err)
	}

	// Run npm install in background (non-blocking)
	go func() {
		cmd := exec.Command("npm", "install")
		cmd.Dir = projectDir
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("npm install failed for project %d: %v\n%s", project.ID, err, string(output))
		} else {
			log.Printf("npm install completed for project %d", project.ID)
		}
	}()

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

	includeScaffold := r.URL.Query().Get("includeScaffold") != "false"

	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(id, 10))
	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, []fileEntry{})
		return
	}

	// Load scaffold manifest
	scaffoldPaths := make(map[string]bool)
	hasManifest := false
	if !includeScaffold {
		manifestPath := filepath.Join(projectDir, scaffoldManifest)
		if data, err := os.ReadFile(manifestPath); err == nil {
			var paths []string
			if json.Unmarshal(data, &paths) == nil {
				hasManifest = true
				for _, p := range paths {
					scaffoldPaths[p] = true
				}
			}
		}
		// If no manifest exists and includeScaffold=false, return empty
		// (all files are treated as scaffold since we can't distinguish)
		if !hasManifest {
			writeJSON(w, http.StatusOK, []fileEntry{})
			return
		}
	}

	var files []fileEntry
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		relPath, _ := filepath.Rel(projectDir, path)

		// Skip node_modules, .expo, and the scaffold manifest itself
		if strings.Contains(relPath, "node_modules") || strings.Contains(relPath, ".expo") || relPath == scaffoldManifest {
			return nil
		}

		// Skip scaffold files if requested
		if !includeScaffold && scaffoldPaths[relPath] {
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

	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(id, 10))

	for _, f := range req.Files {
		// Validate: no path traversal
		clean := filepath.Clean(f.Path)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			writeError(w, http.StatusBadRequest, "invalid file path: "+f.Path)
			return
		}

		fullPath := filepath.Join(projectDir, clean)
		dir := filepath.Dir(fullPath)

		if err := os.MkdirAll(dir, 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create directory")
			return
		}

		if err := os.WriteFile(fullPath, []byte(f.Content), 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to write file: "+f.Path)
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

	// Validate: no path traversal
	clean := filepath.Clean(filePath)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		writeError(w, http.StatusBadRequest, "invalid file path")
		return
	}

	// Don't allow deleting the scaffold manifest
	if clean == scaffoldManifest {
		writeError(w, http.StatusForbidden, "cannot delete scaffold manifest")
		return
	}

	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(id, 10))
	fullPath := filepath.Join(projectDir, clean)

	// Ensure the path is within the project directory
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

	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(id, 10))
	os.RemoveAll(projectDir)

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// copyDirWithManifest copies src to dst and writes a scaffold manifest listing all copied files.
func copyDirWithManifest(src, dst string) error {
	var manifest []string

	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
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

		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return err
		}

		manifest = append(manifest, relPath)
		return nil
	})

	if err != nil {
		return err
	}

	// Write the scaffold manifest
	manifestData, _ := json.Marshal(manifest)
	return os.WriteFile(filepath.Join(dst, scaffoldManifest), manifestData, 0644)
}
