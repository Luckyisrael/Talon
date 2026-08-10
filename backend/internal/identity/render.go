package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RenderConfigs writes the project's identity values into the workspace's
// config files (app.json, package.json). This is a deterministic Go-side
// operation — never an LLM tool call.
func RenderConfigs(upperDir string, id ProjectIdentity, templateDir string) error {
	if err := renderAppJSON(upperDir, id, templateDir); err != nil {
		return fmt.Errorf("render app.json: %w", err)
	}
	if err := renderPackageJSON(upperDir, id, templateDir); err != nil {
		return fmt.Errorf("render package.json: %w", err)
	}
	return nil
}

// renderAppJSON writes identity values into app.json.
func renderAppJSON(upperDir string, id ProjectIdentity, templateDir string) error {
	appJSONPath := filepath.Join(upperDir, "app.json")

	templatePath := filepath.Join(templateDir, "app.json")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		// Fallback: try to construct from known structure
		data = []byte(`{"expo":{"name":"","slug":"","version":"1.0.0","orientation":"portrait","icon":"./assets/icon.png","scheme":"","userInterfaceStyle":"automatic","ios":{"supportsTablet":true},"android":{"adaptiveIcon":{"foregroundImage":"./assets/adaptive-icon.png","backgroundColor":"#0a0a0a"},"package":""},"web":{"bundler":"metro","output":"static","favicon":"./assets/favicon.png"},"plugins":["expo-font","expo-router",["expo-splash-screen",{"backgroundColor":"#0a0a0a","dark":{"backgroundColor":"#000000","image":"./assets/splash-icon.png"},"image":"./assets/splash-icon.png","imageWidth":200,"resizeMode":"contain"}],"expo-web-browser"],"experiments":{"typedRoutes":true},"extra":{"router":{},"eas":{}}}}`)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse app.json: %w", err)
	}

	// Navigate to expo object
	expo, ok := config["expo"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("app.json missing expo key")
	}

	// Set identity fields
	expo["name"] = id.Name
	expo["slug"] = id.Slug
	expo["scheme"] = id.Scheme

	// Set android package
	if android, ok := expo["android"].(map[string]interface{}); ok {
		android["package"] = id.AndroidPackage
	}

	// Write rendered app.json to upper layer
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal app.json: %w", err)
	}

	return os.WriteFile(appJSONPath, append(out, '\n'), 0644)
}

// renderPackageJSON writes identity values into package.json.
func renderPackageJSON(upperDir string, id ProjectIdentity, templateDir string) error {
	pkgPath := filepath.Join(upperDir, "package.json")

	templatePath := filepath.Join(templateDir, "package.json")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read template package.json: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse package.json: %w", err)
	}

	// Set identity fields only — never touch dependencies, scripts, etc.
	config["name"] = id.Slug
	config["displayName"] = id.Name

	// Write rendered package.json to upper layer
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal package.json: %w", err)
	}

	return os.WriteFile(pkgPath, append(out, '\n'), 0644)
}

// BlockIdentityFields checks if a write targets a protected config file.
// All writes to app.json and package.json are blocked — these files are
// rendered once at project creation and must never be modified by the AI.
// Any change to these files would risk dropping identity fields (name, slug,
// scheme, package) even if the new content doesn't explicitly mention them.
func BlockIdentityFields(path, content string) error {
	base := filepath.Base(path)

	if base == "app.json" || base == "package.json" {
		return fmt.Errorf("writes to %s are blocked — identity is rendered at project creation and cannot be modified by AI", path)
	}

	return nil
}
