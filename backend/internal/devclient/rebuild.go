package devclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// PackageChange represents a dependency change detected in a patch.
type PackageChange struct {
	Added   map[string]string // pkg -> version
	Removed map[string]string
}

// DetectPackageChanges parses a package.json diff and returns added/removed deps.
func DetectPackageChanges(workspacePath string, patchPaths []string) (*PackageChange, error) {
	// Check if package.json is in the patch
	hasPackageJSON := false
	for _, p := range patchPaths {
		if filepath.Base(p) == "package.json" {
			hasPackageJSON = true
			break
		}
	}

	if !hasPackageJSON {
		return nil, nil
	}

	// Read the staged package.json
	stagedPath := filepath.Join(workspacePath, ".staging", "package.json")
	origPath := filepath.Join(workspacePath, "package.json")

	stagedDeps, err := readDeps(stagedPath)
	if err != nil {
		// Try reading from workspace root (non-staged)
		stagedDeps, err = readDeps(origPath)
		if err != nil {
			return nil, fmt.Errorf("read staged package.json: %w", err)
		}
	}

	origDeps, err := readDeps(origPath)
	if err != nil {
		// No original — all deps are new
		return &PackageChange{Added: stagedDeps}, nil
	}

	// Diff
	change := &PackageChange{
		Added:   make(map[string]string),
		Removed: make(map[string]string),
	}

	for pkg, ver := range stagedDeps {
		if origVer, ok := origDeps[pkg]; !ok || origVer != ver {
			change.Added[pkg] = ver
		}
	}

	for pkg, ver := range origDeps {
		if _, ok := stagedDeps[pkg]; !ok {
			change.Removed[pkg] = ver
		}
	}

	if len(change.Added) == 0 && len(change.Removed) == 0 {
		return nil, nil
	}

	return change, nil
}

// readDeps reads all dependencies from a package.json file.
func readDeps(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return nil, err
	}

	deps := make(map[string]string)

	// Merge all dep sections
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies"} {
		if d, ok := pkgJSON[section].(map[string]interface{}); ok {
			for pkg, ver := range d {
				if v, ok := ver.(string); ok {
					deps[pkg] = v
				}
			}
		}
	}

	return deps, nil
}
