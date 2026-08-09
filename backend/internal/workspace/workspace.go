package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Manager handles workspace creation using OverlayFS mounts.
// Each project gets a writable upper layer on top of a read-only template lower layer.
type Manager struct {
	templateDir string
	workspacesDir string
	mounts     map[int64]*Mount
	mu         sync.Mutex
}

type Mount struct {
	ProjectID  int64
	UpperDir   string // writable layer (per-project)
	WorkDir    string // OverlayFS workdir
	MountPoint string // the merged view
	IsOverlay  bool   // true if OverlayFS, false if fallback copy
}

func New(templateDir, workspacesDir string) (*Manager, error) {
	// Verify template has node_modules
	nmDir := filepath.Join(templateDir, "node_modules")
	if _, err := os.Stat(nmDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("template node_modules not found at %s — run build-template.sh first", nmDir)
	}

	// Verify template version file
	verFile := filepath.Join(templateDir, ".template-version")
	if _, err := os.Stat(verFile); os.IsNotExist(err) {
		log.Printf("WARNING: no .template-version file in template dir")
	}

	m := &Manager{
		templateDir:   templateDir,
		workspacesDir: workspacesDir,
		mounts:        make(map[int64]*Mount),
	}

	// Ensure workspaces dir exists
	os.MkdirAll(workspacesDir, 0755)

	return m, nil
}

// CreateWorkspace creates a new workspace for a project.
// Uses symlinked node_modules (shared from template, not duplicated).
func (m *Manager) CreateWorkspace(projectID int64) (*Mount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	projectDir := filepath.Join(m.workspacesDir, fmt.Sprintf("%d", projectID))
	upperDir := filepath.Join(projectDir, "upper")
	workDir := filepath.Join(projectDir, "work")

	// Clean up any existing workspace
	os.RemoveAll(projectDir)

	// Create directories
	os.MkdirAll(upperDir, 0755)
	os.MkdirAll(workDir, 0755)

	// Copy template files to upper layer with symlinked node_modules
	if err := m.fallbackCopy(projectDir, upperDir); err != nil {
		os.RemoveAll(projectDir)
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	mount := &Mount{
		ProjectID:  projectID,
		UpperDir:   upperDir,
		WorkDir:    workDir,
		MountPoint: projectDir,
		IsOverlay:  false,
	}
	m.mounts[projectID] = mount
	log.Printf("workspace %d: created (symlinked node_modules)", projectID)
	return mount, nil
}

// fallbackCopy creates an empty upper layer with symlinked node_modules.
// Template files are NOT copied — the file-tree reads only from the upper layer,
// so a fresh project returns empty. AI-generated files go here.
func (m *Manager) fallbackCopy(projectDir, upperDir string) error {
	// Symlink node_modules from template (shared, not duplicated)
	srcNM := filepath.Join(m.templateDir, "node_modules")
	dstNM := filepath.Join(upperDir, "node_modules")
	if _, err := os.Stat(srcNM); err == nil {
		os.Remove(dstNM) // remove if exists
		if err := os.Symlink(srcNM, dstNM); err != nil {
			return fmt.Errorf("failed to symlink node_modules: %w", err)
		}
	}

	return nil
}

// GetWorkspacePath returns the merged workspace path for a project.
func (m *Manager) GetWorkspacePath(projectID int64) string {
	return filepath.Join(m.workspacesDir, fmt.Sprintf("%d", projectID))
}

// GetUpperPath returns the writable upper layer path for a project.
func (m *Manager) GetUpperPath(projectID int64) string {
	return filepath.Join(m.workspacesDir, fmt.Sprintf("%d", projectID), "upper")
}

// DestroyWorkspace removes a project's workspace.
func (m *Manager) DestroyWorkspace(projectID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	projectDir := filepath.Join(m.workspacesDir, fmt.Sprintf("%d", projectID))
	delete(m.mounts, projectID)
	os.RemoveAll(projectDir)
	log.Printf("workspace %d: destroyed", projectID)
}

// TemplateVersion reads the template version from the version file.
func (m *Manager) TemplateVersion() string {
	verFile := filepath.Join(m.templateDir, ".template-version")
	data, err := os.ReadFile(verFile)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// IsOverlayFSAvailable checks if OverlayFS is usable.
func IsOverlayFSAvailable() bool {
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "overlay")
}
