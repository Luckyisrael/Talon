package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/luckyisrael/talon/backend/internal/sdk"
)

type BuildAPK struct {
	WorkspacePath string
	ADBClient     *ADBRunner
	ADBTarget     string
	Allowlist     AllowlistInterface
	// Output, when set, receives the build's stdout/stderr as it runs (expo
	// prebuild, gradle, adb install) so the frontend can show the build live.
	Output io.Writer
}

// AllowlistInterface provides access to the native-module allowlist without importing devclient.
type AllowlistInterface interface {
	IsJSOnlyChange(workspacePath string, changedFiles []string) bool
	IsAllowlisted(pkgName string) bool
}

func (t *BuildAPK) Name() string { return "build_apk" }

func (t *BuildAPK) Description() string {
	return "Build an Android APK from the Expo/React Native project. Runs expo prebuild, gradle assembleDebug, and installs on device."
}

func (t *BuildAPK) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"install": {
				"type": "boolean",
				"description": "Whether to install the APK on the connected device after building (default: true)"
			},
			"force": {
				"type": "boolean",
				"description": "Force a full native build even if only JS files changed (default: false)"
			}
		}
	}`)
}

type BuildAPKArgs struct {
	Install bool `json:"install"`
	Force   bool `json:"force"`
}

type ADBRunner struct {
	Target string
}

func (a *ADBRunner) Run(command string) (string, error) {
	args := []string{"-s", a.Target, "shell", command}
	out, err := exec.Command("adb", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (a *ADBRunner) InstallAPK(apkPath string) (string, error) {
	args := []string{"-s", a.Target, "install", "-r", apkPath}
	out, err := exec.Command("adb", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ClearLogcat wipes the device log buffer. Boot-gated flows clear before
// launch so the tail they read afterwards contains only this run —
// yesterday's crash must not triage today's boot.
func (a *ADBRunner) ClearLogcat() error {
	args := []string{"-s", a.Target, "logcat", "-c"}
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("logcat -c failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// DumpLogcat returns the last lines of the device log in `-v time` format
// (timestamps are what boot-gap detection reads). filter scopes to one tag
// when non-empty.
func (a *ADBRunner) DumpLogcat(filter string, lines int) (string, error) {
	args := []string{"-s", a.Target, "logcat", "-d", "-v", "time", "-t", strconv.Itoa(lines)}
	if filter != "" {
		args = append(args, filter)
	}
	out, err := exec.Command("adb", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("logcat dump failed: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *BuildAPK) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a BuildAPKArgs
	if err := json.Unmarshal(args, &a); err != nil {
		a.Install = true
	}

	// Check if this is a JS-only change (no native deps changed)
	if t.Allowlist != nil && !a.Force && t.Allowlist.IsJSOnlyChange(t.WorkspacePath, nil) {
		return ToolResult{
			Content: "⚠️ Guardrail: This build appears to be JS-only (no native dependency changes). " +
				"Consider using hot reload instead of a full native build to save time. " +
				"Force a full build by re-running with force=true if native changes were made.",
			IsError: false,
		}, nil
	}

	// Find Java
	javaHome := sdk.JavaHome()
	if javaHome == "" {
		return ToolResult{
			Content: "Java JDK 17 not found. Install JDK 17 and set JAVA_HOME.",
			IsError: true,
		}, nil
	}

	// Find Android SDK
	androidHome := sdk.AndroidHome()
	if androidHome == "" {
		return ToolResult{
			Content: "Android SDK not found. Install Android SDK and set ANDROID_HOME.",
			IsError: true,
		}, nil
	}

	// Step 1: expo prebuild
	prebuildResult, err := t.runExpoPrebuild(ctx, javaHome, androidHome)
	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("expo prebuild failed: %s\n%s", err.Error(), prebuildResult),
			IsError: true,
		}, err
	}

	// Step 2: gradle assembleDebug
	apkPath, gradleResult, err := t.runGradleBuild(ctx, javaHome, androidHome)
	if err != nil {
		return ToolResult{
			Content: fmt.Sprintf("gradle build failed: %s\n%s", err.Error(), gradleResult),
			IsError: true,
		}, err
	}

	result := fmt.Sprintf("APK built successfully: %s", apkPath)

	// Step 3: install on device
	if a.Install {
		installResult, err := t.installAPK(apkPath)
		if err != nil {
			result += fmt.Sprintf("\n\nAPK built but install failed: %s\n%s", err.Error(), installResult)
		} else {
			result += fmt.Sprintf("\n\nInstalled on device: %s", installResult)
		}
	}

	return ToolResult{Content: result}, nil
}

func (t *BuildAPK) runExpoPrebuild(ctx context.Context, javaHome, androidHome string) (string, error) {
	cmd := exec.CommandContext(ctx, "npx", "expo", "prebuild", "-p", "android", "--no-install")
	cmd.Dir = t.WorkspacePath
	cmd.Env = append(os.Environ(),
		"JAVA_HOME="+javaHome,
		"ANDROID_HOME="+androidHome,
		"PATH="+javaHome+"/bin:"+androidHome+"/platform-tools:"+os.Getenv("PATH"),
	)

	output, err := runCaptured(cmd, t.Output)
	return string(output), err
}

func (t *BuildAPK) runGradleBuild(ctx context.Context, javaHome, androidHome string) (string, string, error) {
	androidDir := filepath.Join(t.WorkspacePath, "android")

	gradlew := filepath.Join(androidDir, "gradlew")
	if _, err := os.Stat(gradlew); os.IsNotExist(err) {
		return "", "", fmt.Errorf("gradlew not found at %s — run expo prebuild first", gradlew)
	}

	// Native Linux: always use ./gradlew (no Windows cmd /c gradlew.bat path)
	cmd := exec.CommandContext(ctx, "./gradlew", "assembleDebug")
	cmd.Dir = androidDir
	cmd.Env = append(os.Environ(),
		"JAVA_HOME="+javaHome,
		"ANDROID_HOME="+androidHome,
		"PATH="+javaHome+"/bin:"+androidHome+"/platform-tools:"+os.Getenv("PATH"),
	)

	output, err := runCaptured(cmd, t.Output)

	apkPath := findAPK(androidDir)
	return apkPath, string(output), err
}

func (t *BuildAPK) installAPK(apkPath string) (string, error) {
	target := t.ADBTarget
	if target == "" {
		target = "localhost:5555"
	}
	cmd := exec.Command("adb", "-s", target, "install", "-r", apkPath)
	output, err := runCaptured(cmd, t.Output)
	return strings.TrimSpace(string(output)), err
}

func findAPK(androidDir string) string {
	buildDir := filepath.Join(androidDir, "app", "build", "outputs", "apk", "debug")
	apkPath := filepath.Join(buildDir, "app-debug.apk")
	if _, err := os.Stat(apkPath); err == nil {
		return apkPath
	}

	var found string
	filepath.Walk(androidDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".apk") {
			found = path
			return filepath.SkipDir
		}
		return nil
	})

	return found
}
