package devclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NativeModule represents a single native module baked into the dev client.
type NativeModule struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	NativeLinking  bool   `json:"nativeLinking"`
	AndroidPackage string `json:"androidPackage,omitempty"`
}

// Allowlist is the versioned manifest of all native modules baked into a dev-client build.
type Allowlist struct {
	TemplateVersion string         `json:"templateVersion"`
	SDKVersion      string         `json:"sdkVersion"`
	BuiltAt         string         `json:"builtAt"`
	Modules         []NativeModule `json:"modules"`
}

// LoadAllowlist reads the native-module allowlist from the given path.
func LoadAllowlist(path string) (*Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}

	var al Allowlist
	if err := json.Unmarshal(data, &al); err != nil {
		return nil, fmt.Errorf("parse allowlist: %w", err)
	}

	return &al, nil
}

// SaveAllowlist writes the allowlist to the given path.
func SaveAllowlist(path string, al *Allowlist) error {
	data, err := json.MarshalIndent(al, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal allowlist: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create allowlist dir: %w", err)
	}

	return os.WriteFile(path, append(data, '\n'), 0644)
}

// IsAllowlisted returns true if the package name is in the allowlist.
func (al *Allowlist) IsAllowlisted(pkgName string) bool {
	for _, m := range al.Modules {
		if m.Name == pkgName {
			return true
		}
	}
	return false
}

// ModuleNames returns a sorted list of all allowlisted module names.
func (al *Allowlist) ModuleNames() []string {
	names := make([]string, len(al.Modules))
	for i, m := range al.Modules {
		names[i] = m.Name
	}
	return names
}

// AllowlistPath returns the path to the allowlist file for a given template version.
func AllowlistPath(templateDir, version string) string {
	return filepath.Join(templateDir, "dev-client", "v"+version, "native-modules.json")
}

// DevClientAPKPath returns the path to the pre-built dev-client APK for a given template version.
func DevClientAPKPath(templateDir, version string) string {
	return filepath.Join(templateDir, "dev-client", "v"+version, "app-debug.apk")
}

// NeedsRebuild checks if adding a new dependency requires a native rebuild.
// Returns (needsRebuild bool, reason string).
func (al *Allowlist) NeedsRebuild(newDeps map[string]string) (bool, string) {
	return al.NeedsRebuildInDir(".", newDeps)
}

// NeedsRebuildInDir checks if adding new dependencies requires a native rebuild,
// using the given directory as the base for file operations (goroutine-safe).
func (al *Allowlist) NeedsRebuildInDir(baseDir string, newDeps map[string]string) (bool, string) {
	for pkgName := range newDeps {
		if al.IsAllowlisted(pkgName) {
			continue
		}

		// Package is not in allowlist — check if it has native linking
		if hasNativeLinkingInDir(baseDir, pkgName) {
			return true, fmt.Sprintf("package %q has native linking and is not in the baked allowlist", pkgName)
		}
	}

	return false, ""
}

// IsJSOnlyChange checks if a set of changed files represents only JS/CSS changes
// with no native dependency modifications. If changedFiles is nil, it checks
// package.json and package-lock.json for native-only changes.
func (al *Allowlist) IsJSOnlyChange(workspacePath string, changedFiles []string) bool {
	if len(changedFiles) == 0 {
		// Check package.json for native module changes
		pkgJSONPath := filepath.Join(workspacePath, "package.json")
		pkgData, err := os.ReadFile(pkgJSONPath)
		if err != nil {
			return false
		}

		var pkg struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		if err := json.Unmarshal(pkgData, &pkg); err != nil {
			return false
		}

		// If any dependency has native linking and is newly added, it's not JS-only
		for pkgName := range pkg.Dependencies {
			if al.IsAllowlisted(pkgName) {
				continue
			}
			if hasNativeLinkingInDir(workspacePath, pkgName) {
				return false
			}
		}
		return true
	}

	// Check if any changed file is a native config file
	nativeFiles := []string{
		"android/", "ios/", "ios/Podfile",
		"react-native.config.js", "app.json", "expo-module.config.json",
	}
	for _, changed := range changedFiles {
		for _, native := range nativeFiles {
			if strings.HasPrefix(changed, native) || changed == native {
				return false
			}
		}
	}

	return true
}

// hasNativeLinking checks if a package in node_modules has native Android/iOS files.
// This is a structural check, not a keyword guess.
func hasNativeLinking(pkgName string) bool {
	return hasNativeLinkingInDir(".", pkgName)
}

// hasNativeLinkingInDir checks if a package has native linking, using baseDir for file operations.
func hasNativeLinkingInDir(baseDir, pkgName string) bool {
	// Check for common native indicators in node_modules
	candidates := []string{
		filepath.Join(baseDir, "node_modules", pkgName, "android", "build.gradle"),
		filepath.Join(baseDir, "node_modules", pkgName, "android", "build.gradle.kts"),
		filepath.Join(baseDir, "node_modules", pkgName, "ios", "Podfile"),
		filepath.Join(baseDir, "node_modules", pkgName, "react-native.config.js"),
		filepath.Join(baseDir, "node_modules", pkgName, "expo-modules.config.json"),
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return true
		}
	}

	// Check package.json for native module config
	pkgJSONPath := filepath.Join(baseDir, "node_modules", pkgName, "package.json")
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return false
	}

	var pkgJSON map[string]interface{}
	if err := json.Unmarshal(data, &pkgJSON); err != nil {
		return false
	}

	// Check for expo plugin config
	if expo, ok := pkgJSON["expo"].(map[string]interface{}); ok {
		if _, ok := expo["plugins"]; ok {
			return true
		}
	}

	// Check for react-native config
	if _, ok := pkgJSON["react-native"]; ok {
		return true
	}

	// Check for autolinking config
	if rnConfig, ok := pkgJSON["react-native-config"]; ok {
		if rnConfig != nil {
			return true
		}
	}

	return false
}

// FormatForPrompt returns a human-readable list of allowlisted modules for AI context.
func (al *Allowlist) FormatForPrompt() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Baked Native Modules (v%s):\n", al.TemplateVersion))
	sb.WriteString("These native modules are already available in the dev client. Do NOT add them to package.json:\n")
	for _, m := range al.Modules {
		sb.WriteString(fmt.Sprintf("- %s@%s\n", m.Name, m.Version))
	}
	return sb.String()
}
