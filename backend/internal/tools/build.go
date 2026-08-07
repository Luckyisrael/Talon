package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type BuildAPK struct {
	WorkspacePath string
	ADBClient     *ADBRunner
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
			}
		}
	}`)
}

type BuildAPKArgs struct {
	Install bool `json:"install"`
}

type ADBRunner struct {
	WslDistro string
	Target    string
}

func (a *ADBRunner) Run(command string) (string, error) {
	args := []string{"-d", a.WslDistro, "-e", "adb", "-s", a.Target, "shell", command}
	out, err := exec.Command("wsl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (a *ADBRunner) InstallAPK(apkPath string) (string, error) {
	args := []string{"-d", a.WslDistro, "-e", "adb", "-s", a.Target, "install", "-r", apkPath}
	out, err := exec.Command("wsl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (t *BuildAPK) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a BuildAPKArgs
	if err := json.Unmarshal(args, &a); err != nil {
		a.Install = true
	}

	if a.Install {
		a.Install = true
	}

	// Find Java
	javaHome := findJavaHome()
	if javaHome == "" {
		return ToolResult{
			Content: "Java JDK 17 not found. Install JDK 17 and set JAVA_HOME.",
			IsError: true,
		}, nil
	}

	// Find Android SDK
	androidHome := findAndroidHome()
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

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (t *BuildAPK) runGradleBuild(ctx context.Context, javaHome, androidHome string) (string, string, error) {
	androidDir := filepath.Join(t.WorkspacePath, "android")

	// Check if gradlew exists
	gradlew := filepath.Join(androidDir, "gradlew")
	if _, err := os.Stat(gradlew); os.IsNotExist(err) {
		return "", "", fmt.Errorf("gradlew not found at %s — run expo prebuild first", gradlew)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", "gradlew.bat", "assembleDebug")
	} else {
		cmd = exec.CommandContext(ctx, "./gradlew", "assembleDebug")
	}
	cmd.Dir = androidDir
	cmd.Env = append(os.Environ(),
		"JAVA_HOME="+javaHome,
		"ANDROID_HOME="+androidHome,
		"PATH="+javaHome+"/bin:"+androidHome+"/platform-tools:"+os.Getenv("PATH"),
	)

	output, err := cmd.CombinedOutput()

	// Find the APK
	apkPath := findAPK(androidDir)
	return apkPath, string(output), err
}

func (t *BuildAPK) installAPK(apkPath string) (string, error) {
	// Use Windows adb directly
	androidHome := findAndroidHome()
	adbPath := filepath.Join(androidHome, "platform-tools", "adb.exe")

	cmd := exec.Command(adbPath, "-s", "localhost:5555", "install", "-r", apkPath)
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func findJavaHome() string {
	// Check JAVA_HOME env
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		return jh
	}

	// Common Windows locations
	candidates := []string{
		`C:\Program Files\Microsoft\jdk-17.0.16.8-hotspot`,
		`C:\Program Files\Java\jdk-17`,
		`C:\Program Files\Eclipse Adoptium\jdk-17`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "bin", "java.exe")); err == nil {
			return c
		}
	}

	// Try where java
	if out, err := exec.Command("where", "java.exe").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			return filepath.Dir(filepath.Dir(strings.TrimSpace(lines[0])))
		}
	}

	return ""
}

func findAndroidHome() string {
	if ah := os.Getenv("ANDROID_HOME"); ah != "" {
		return ah
	}
	if ah := os.Getenv("ANDROID_SDK_ROOT"); ah != "" {
		return ah
	}

	// Common Windows location
	candidates := []string{
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk"),
		`C:\Users\` + os.Getenv("USERNAME") + `\AppData\Local\Android\Sdk`,
		`C:\Android\sdk`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "platform-tools", "adb.exe")); err == nil {
			return c
		}
	}

	return ""
}

func findAPK(androidDir string) string {
	buildDir := filepath.Join(androidDir, "app", "build", "outputs", "apk", "debug")
	apkPath := filepath.Join(buildDir, "app-debug.apk")
	if _, err := os.Stat(apkPath); err == nil {
		return apkPath
	}

	// Walk to find any .apk
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
