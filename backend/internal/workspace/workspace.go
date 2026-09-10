package workspace

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Manager provisions one directory per project.
//
// This used to describe itself as an OverlayFS manager with a read-only template
// lower layer and a writable `upper/`, but nothing ever mounted an overlay — the
// fallback copy was the only code path. What it left behind was worse than a
// plain directory: the agent's tools wrote to the project root while the file API
// read from `upper/`, so generated code was invisible in the IDE, and the config
// files existed twice and drifted apart.
//
// So there is one directory. node_modules is a symlink into the shared template,
// which is what makes provisioning instant.
type Manager struct {
	templateDir   string
	workspacesDir string

	mu       sync.Mutex
	projects map[int64]string
}

// configFiles are copied out of the template so TypeScript can resolve types,
// Expo can read the app config, and the native build can find its entry point.
//
// index.js is the app entry: package.json declares `"main": "./index.js"` and
// the gradle build resolves that file before anything else compiles. Leaving it
// out of this list made every generated workspace build-fail with
// `Cannot convert '' to File` in build.gradle, because the entry never existed.
var configFiles = []string{
	"package.json", "tsconfig.json", "app.json",
	"babel.config.js", "metro.config.js", "index.js",
	"polyfill.js", "eslint.config.js",
	// AGENTS.md ships from the template so every workspace begins with the
	// platform conventions in editable form — the agent reads it as its
	// instructions chain (H6), and the user can edit it per project.
	"AGENTS.md",
}

// templateDirs are directories copied recursively from the template into the
// workspace. The app/ directory carries the expo-router scaffolding (_layout.tsx,
// tabs, etc.) that the agent builds on instead of starting from scratch.
var templateDirs = []string{"app", "assets", "components", "constants", "features", "utils"}

func New(templateDir, workspacesDir string) (*Manager, error) {
	nmDir := filepath.Join(templateDir, "node_modules")
	if _, err := os.Stat(nmDir); err != nil {
		return nil, fmt.Errorf("the template has no node_modules at %s — run build-template.sh first", nmDir)
	}

	if _, err := os.Stat(filepath.Join(templateDir, ".template-version")); err != nil {
		log.Printf("WARNING: the template has no .template-version file")
	}

	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("could not create %s: %w", workspacesDir, err)
	}

	return &Manager{
		templateDir:   templateDir,
		workspacesDir: workspacesDir,
		projects:      make(map[int64]string),
	}, nil
}

func (m *Manager) TemplateDir() string { return m.templateDir }

// Path returns a project's workspace directory. This is the single location the
// agent writes to, the typechecker runs in, and the file API reads from.
func (m *Manager) Path(projectID int64) string {
	return filepath.Join(m.workspacesDir, strconv.FormatInt(projectID, 10))
}

// Create provisions a project's workspace. It is destructive: an existing
// workspace for the same id is removed first.
func (m *Manager) Create(projectID int64) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.Path(projectID)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("could not clear the old workspace: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create the workspace: %w", err)
	}

	if err := m.provision(dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	m.projects[projectID] = dir
	log.Printf("workspace %d: ready at %s", projectID, dir)
	return dir, nil
}

// provision links the shared dependencies and copies the config files a new
// project needs to typecheck and run.
func (m *Manager) provision(dir string) error {
	srcNM := filepath.Join(m.templateDir, "node_modules")
	dstNM := filepath.Join(dir, "node_modules")
	os.Remove(dstNM)
	if err := os.Symlink(srcNM, dstNM); err != nil {
		return fmt.Errorf("could not link the shared node_modules: %w", err)
	}

	for _, name := range configFiles {
		src := filepath.Join(m.templateDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			// Not every template carries every optional config.
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return fmt.Errorf("could not copy %s: %w", name, err)
		}
	}

	// Copy template directories (app/, assets/, constants/) so the agent starts
	// with working scaffolding instead of an empty app/ directory. assets/ is
	// load-bearing: app.json references its icons and a prebuild fails without
	// them.
	for _, dirname := range templateDirs {
		srcDir := filepath.Join(m.templateDir, dirname)
		if _, err := os.Stat(srcDir); err != nil {
			continue // optional
		}
		if err := copyDir(srcDir, filepath.Join(dir, dirname)); err != nil {
			return fmt.Errorf("could not copy %s/: %w", dirname, err)
		}
	}

	return nil
}

// copyDir recursively copies src into dst, creating directories as needed.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Destroy removes a project's workspace.
func (m *Manager) Destroy(projectID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.projects, projectID)
	if err := os.RemoveAll(m.Path(projectID)); err != nil {
		log.Printf("workspace %d: could not remove: %v", projectID, err)
		return
	}
	log.Printf("workspace %d: removed", projectID)
}

// TemplateVersion reads the version the template was built at.
func (m *Manager) TemplateVersion() string {
	data, err := os.ReadFile(filepath.Join(m.templateDir, ".template-version"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}
