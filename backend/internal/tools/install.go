package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/devclient"
)

// InstallPackage installs an npm package into the shared template, not the
// workspace.
//
// The workspace's node_modules is a symlink into the shared template directory
// — that is what makes provisioning instant and what makes "preinstalled"
// true. Running npm install inside the workspace is therefore destructive:
// npm replaces the symlink with a real directory holding only the newly
// installed package, and the workspace instantly loses every preinstalled
// dependency (every build after that dies with "Cannot find module"). So the
// tool installs into the template directory, where the symlink resolves it for
// this workspace and every other one, and the template's package.json + lock
// stay the single source of truth.
type InstallPackage struct {
	WorkspacePath string
	// TemplateDir is the shared template root the workspace's node_modules
	// links to. Empty disables the template install path and the availability
	// check (a caller without a template falls back to the old behavior).
	TemplateDir string
	Allowlist   *devclient.Allowlist
	// Output, when set, receives the install command's stdout/stderr as it runs.
	Output io.Writer
}

func (t *InstallPackage) Name() string { return "install_package" }

func (t *InstallPackage) Description() string {
	return "Install an npm package. Checks against the baked dev-client allowlist. Detects if native rebuild is needed."
}

func (t *InstallPackage) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Package name (e.g., '@solana-mobile/mobile-wallet-adapter-protocol-web3js')"
			},
			"version": {
				"type": "string",
				"description": "Optional version specifier (e.g., '^1.0.0')"
			}
		},
		"required": ["name"]
	}`)
}

type InstallPackageArgs struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (t *InstallPackage) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a InstallPackageArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	pkg := packageRoot(strings.TrimSpace(a.Name))
	if pkg == "" {
		return errResult("provide a package name, e.g. expo-linear-gradient"), nil
	}

	// The install directory: the shared template when there is one, else the
	// workspace itself (tests and compositions without a shared template).
	installDir := t.WorkspacePath
	if t.TemplateDir != "" {
		if _, err := os.Stat(filepath.Join(t.TemplateDir, "package.json")); err == nil {
			installDir = t.TemplateDir
		}
	}

	// Before reaching for the network, make sure the package is not already
	// available. A package already on disk — declared in the template, or
	// present as a transitive — needs no install, and installing it would
	// churn the lock and could force a native rebuild for nothing. The model
	// is told to import it directly; this is the "check the template first"
	// discipline.
	if installDir == t.TemplateDir {
		if reason, already := whyAvailable(t.TemplateDir, pkg); already {
			return ToolResult{
				Content: fmt.Sprintf(
					"`%s` is already available in this project%s — do not install it. Import it directly: "+
						"`import { ... } from '%s'`. If you were unsure of its API, call inspect_package with the name. "+
						"Do not retry this install with a version specifier or an alias.",
					pkg, reason, pkg),
				IsError: true,
			}, nil
		}
	}

	pkgSpec := a.Name
	if a.Version != "" {
		pkgSpec = a.Name + "@" + a.Version
	}

	// Check if package is in the baked allowlist
	isAllowlisted := false
	if t.Allowlist != nil {
		isAllowlisted = t.Allowlist.IsAllowlisted(a.Name)
	}

	// Check if package has native code
	needsRebuild := false
	if t.Allowlist != nil {
		// Use the allowlist's structural check (goroutine-safe)
		_, reason := t.Allowlist.NeedsRebuildInDir(t.WorkspacePath, map[string]string{a.Name: "*"})
		if reason != "" {
			needsRebuild = true
		}
	} else {
		// Fallback to filesystem check
		needsRebuild = t.checkNativeCode(a.Name)
	}

	// Install the package into the shared template (not the workspace): see
	// the type comment for why installing inside the workspace destroys the
	// preinstalled dependency tree.
	cmd := exec.CommandContext(ctx, "npm", "install", pkgSpec, "--save")
	cmd.Dir = installDir

	output, err := runCaptured(cmd, t.Output)
	outputStr := string(output)

	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("install failed: %s\nOutput:\n%s", err.Error(), outputStr),
			IsError: true,
		}, err
	}

	where := "the shared template (visible to every project)"
	if installDir == t.WorkspacePath {
		where = "the workspace"
	}
	result := fmt.Sprintf("Installed %s into %s", pkgSpec, where)

	if isAllowlisted {
		result += "\n\nThis package is already baked into the dev client — no native rebuild needed."
	} else if needsRebuild {
		result += "\n\n⚠️ This package has native code and is NOT in the baked allowlist. A native rebuild will be triggered automatically."
	}

	return ToolResult{Content: result}, nil
}

// whyAvailable reports whether pkg is already usable from the shared template
// and, when it is, where it comes from: a declared dependency (the preinstalled
// set the model is told about) or a transitive already on disk (present but not
// listed — importing it is still safe, the module resolves).
//
// The check is on disk, not on a cached list: the template is shared and a
// previous install in this process already changed it, so a stale cache would
// either re-install what is there or block a genuinely missing package.
func whyAvailable(templateDir, pkg string) (reason string, ok bool) {
	pkgDir := filepath.Join(templateDir, "node_modules", pkg)
	if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err != nil {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(templateDir, "package.json"))
	if err == nil {
		var pj struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(raw, &pj) == nil {
			if _, d := pj.Dependencies[pkg]; d {
				return " (it is a preinstalled template dependency)", true
			}
			if _, d := pj.DevDependencies[pkg]; d {
				return " (it is a preinstalled template dependency)", true
			}
		}
	}
	return " (it is already installed in the shared template)", true
}

func (t *InstallPackage) checkNativeCode(pkgName string) bool {	// Check if package has android/ folder in node_modules
	androidPath := filepath.Join(t.WorkspacePath, "node_modules", pkgName, "android")
	if _, err := os.Stat(androidPath); err == nil {
		return true
	}

	// Check if package has ios/ folder
	iosPath := filepath.Join(t.WorkspacePath, "node_modules", pkgName, "ios")
	if _, err := os.Stat(iosPath); err == nil {
		return true
	}

	return false
}
