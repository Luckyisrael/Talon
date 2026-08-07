package staging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manager struct {
	stagingPath   string
	workspacePath string
}

func NewManager(stagingPath, workspacePath string) *Manager {
	return &Manager{
		stagingPath:   stagingPath,
		workspacePath: workspacePath,
	}
}

func (m *Manager) Init() error {
	return os.MkdirAll(m.stagingPath, 0755)
}

func (m *Manager) WriteFile(relPath, content string) error {
	cleanPath := filepath.Clean(relPath)
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path traversal not allowed: %s", relPath)
	}

	fullPath := filepath.Join(m.stagingPath, cleanPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", cleanPath, err)
	}

	return nil
}

func (m *Manager) ReadFile(relPath string) (string, error) {
	cleanPath := filepath.Clean(relPath)
	if strings.Contains(cleanPath, "..") {
		return "", fmt.Errorf("path traversal not allowed: %s", relPath)
	}

	fullPath := filepath.Join(m.stagingPath, cleanPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", cleanPath, err)
	}

	return string(data), nil
}

func (m *Manager) DeleteFile(relPath string) error {
	cleanPath := filepath.Clean(relPath)
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path traversal not allowed: %s", relPath)
	}

	fullPath := filepath.Join(m.stagingPath, cleanPath)
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete file %s: %w", cleanPath, err)
	}

	return nil
}

func (m *Manager) ListFiles() ([]string, error) {
	var files []string
	err := filepath.Walk(m.stagingPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(m.stagingPath, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(relPath))
		return nil
	})
	return files, err
}

type Patch struct {
	Files []PatchFile
}

type PatchFile struct {
	Path    string
	Content string
}

// PromoteBatch atomically moves all files from staging to the live workspace.
// If any file fails to promote, the entire batch is aborted.
func (m *Manager) PromoteBatch(patch Patch) error {
	for _, f := range patch.Files {
		cleanPath := filepath.Clean(f.Path)
		if strings.Contains(cleanPath, "..") {
			return fmt.Errorf("path traversal not allowed: %s", f.Path)
		}

		src := filepath.Join(m.stagingPath, cleanPath)
		dst := filepath.Join(m.workspacePath, cleanPath)

		if _, err := os.Stat(src); os.IsNotExist(err) {
			return fmt.Errorf("source file not found in staging: %s", cleanPath)
		}

		dir := filepath.Dir(dst)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("promote failed on %s, aborting batch: %w", cleanPath, err)
		}
	}

	return nil
}

// PromoteAll moves all staged files to the live workspace atomically.
func (m *Manager) PromoteAll() error {
	patch := Patch{}
	err := filepath.Walk(m.stagingPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(m.stagingPath, path)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		patch.Files = append(patch.Files, PatchFile{
			Path:    filepath.ToSlash(relPath),
			Content: string(data),
		})
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to enumerate staging files: %w", err)
	}

	return m.PromoteBatch(patch)
}

func (m *Manager) Cleanup() error {
	return os.RemoveAll(m.stagingPath)
}

func (m *Manager) StagingPath() string {
	return m.stagingPath
}

func (m *Manager) WorkspacePath() string {
	return m.workspacePath
}
