package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type InstallPackage struct {
	WorkspacePath string
}

func (t *InstallPackage) Name() string { return "install_package" }

func (t *InstallPackage) Description() string {
	return "Install an npm package. Only allows packages from the allowlist (MWA, Seed Vault, common Expo modules). Detects if native rebuild is needed."
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

var allowedPackages = map[string]bool{
	"@solana-mobile/mobile-wallet-adapter-protocol":      true,
	"@solana-mobile/mobile-wallet-adapter-protocol-web3js": true,
	"@solana-mobile/seed-vault-sdk":                     true,
	"@solana/web3.js":                                   true,
	"@solana/spl-token":                                 true,
	"@react-navigation/native":                          true,
	"@react-navigation/stack":                           true,
	"@react-navigation/bottom-tabs":                     true,
	"expo":                                              true,
	"expo-router":                                       true,
	"expo-status-bar":                                   true,
	"expo-constants":                                    true,
	"expo-linking":                                      true,
	"expo-secure-store":                                 true,
	"react-native":                                      true,
	"react-native-safe-area-context":                    true,
	"react-native-screens":                              true,
	"react-native-gesture-handler":                      true,
	"react-native-reanimated":                           true,
	"react-native-get-random-values":                    true,
	"@craftzdog/react-native-buffer":                    true,
	"react-native-quick-base64":                         true,
	"@react-native-async-storage/async-storage":         true,
	"react-native-crypto":                               true,
	"react-native-url-polyfill":                         true,
}

// nativePackages lists packages that require native rebuild
var nativePackages = map[string]bool{
	"@solana-mobile/mobile-wallet-adapter-protocol":      true,
	"@solana-mobile/mobile-wallet-adapter-protocol-web3js": true,
	"@solana-mobile/seed-vault-sdk":                     true,
	"react-native-gesture-handler":                      true,
	"react-native-reanimated":                           true,
	"react-native-safe-area-context":                    true,
	"react-native-screens":                              true,
	"react-native-quick-crypto":                         true,
	"react-native-svg":                                  true,
	"react-native-webview":                              true,
}

func (t *InstallPackage) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a InstallPackageArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	pkgName := a.Name
	if idx := strings.Index(pkgName, "@"); idx > 0 {
		pkgName = pkgName[idx+1:]
	}
	if idx := strings.Index(pkgName, "/"); idx > 0 {
		pkgName = pkgName[:idx]
	}

	fullName := a.Name
	if !strings.Contains(a.Name, "/") {
		fullName = "@" + pkgName + "/" + pkgName
	}

	if !allowedPackages[fullName] && !allowedPackages[a.Name] {
		return ToolResult{
			Content: fmt.Sprintf("package not allowed: %s. Allowed packages: MWA, Seed Vault, common Expo modules", a.Name),
			IsError: true,
		}, nil
	}

	pkgSpec := a.Name
	if a.Version != "" {
		pkgSpec = a.Name + "@" + a.Version
	}

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

	// Check if package has native code
	needsRebuild := t.checkNativeCode(a.Name)

	result := fmt.Sprintf("Installed %s successfully", pkgSpec)
	if needsRebuild {
		result += "\n\n⚠️ This package has native code. Run `expo prebuild -p android` to regenerate the android/ directory, then rebuild the APK."
	}

	return ToolResult{Content: result}, nil
}

func (t *InstallPackage) checkNativeCode(pkgName string) bool {
	// Check if package is in the known native list
	if nativePackages[pkgName] {
		return true
	}

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
