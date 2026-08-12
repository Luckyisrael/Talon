package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/seekersstudio/backend/internal/devclient"
)

type InstallPackage struct {
	WorkspacePath string
	Allowlist     *devclient.Allowlist
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

	// Install the package
	cmd := exec.CommandContext(ctx, "npm", "install", pkgSpec, "--save")
	cmd.Dir = t.WorkspacePath

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("install failed: %s\nOutput:\n%s", err.Error(), outputStr),
			IsError: true,
		}, err
	}

	result := fmt.Sprintf("Installed %s successfully", pkgSpec)

	if isAllowlisted {
		result += "\n\nThis package is already baked into the dev client — no native rebuild needed."
	} else if needsRebuild {
		result += "\n\n⚠️ This package has native code and is NOT in the baked allowlist. A native rebuild will be triggered automatically."
	}

	return ToolResult{Content: result}, nil
}

func (t *InstallPackage) checkNativeCode(pkgName string) bool {
	// Check if package has android/ folder in node_modules
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
