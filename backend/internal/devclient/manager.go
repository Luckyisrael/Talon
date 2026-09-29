package devclient

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sdk "github.com/luckyisrael/talon/backend/internal/sdk"
)

// Manager handles dev-client lifecycle: installation, rebuild detection, and async rebuilds.
type Manager struct {
	templateDir string
	allowlist   *Allowlist
	rebuilds    map[int64]*RebuildState // sessionID -> rebuild state
	mu          sync.Mutex
}

// RebuildState tracks an in-progress background rebuild.
type RebuildState struct {
	SessionID   int64
	Status      string // "pending", "building", "success", "failed"
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       string
	LastGoodAPK string // path to the last-known-good APK
	NewAPK      string // path to the new APK (if success)
}

// NewManager creates a dev-client manager for the given template directory.
func NewManager(templateDir string) (*Manager, error) {
	verFile := filepath.Join(templateDir, ".template-version")
	data, err := os.ReadFile(verFile)
	if err != nil {
		return nil, fmt.Errorf("read template version: %w", err)
	}
	version := strings.TrimRight(string(data), "\r\n\t ")

	alPath := AllowlistPath(templateDir, version)
	al, err := LoadAllowlist(alPath)
	if err != nil {
		// Allowlist may not exist yet — create empty
		al = &Allowlist{
			TemplateVersion: version,
			Modules:         []NativeModule{},
		}
		log.Printf("WARNING: no allowlist at %s, using empty", alPath)
	}

	return &Manager{
		templateDir: templateDir,
		allowlist:   al,
		rebuilds:    make(map[int64]*RebuildState),
	}, nil
}

// Allowlist returns the current native-module allowlist.
func (m *Manager) Allowlist() *Allowlist {
	return m.allowlist
}

// DevClientAPKPath returns the path to the pre-built dev-client APK.
func (m *Manager) DevClientAPKPath() string {
	return DevClientAPKPath(m.templateDir, m.allowlist.TemplateVersion)
}

// InstallDevClient installs the pre-built dev-client APK into a container via ADB.
// Retries up to 5 times if the package manager service is not ready.
func (m *Manager) InstallDevClient(ctx context.Context, adbTarget string) error {
	apkPath := m.DevClientAPKPath()

	// Check if APK exists
	if _, err := os.Stat(apkPath); os.IsNotExist(err) {
		return fmt.Errorf("dev-client APK not found at %s — run build-dev-client.sh first", apkPath)
	}

	// Check if already installed on device
	if m.IsInstalledOnDevice(adbTarget) {
		log.Printf("dev-client already installed on %s", adbTarget)
		return nil
	}

	// Retry install (same pattern as wallet — wait for package service)
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			log.Printf("dev-client install: waiting for package service (attempt %d/5)", attempt+1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}

		// Check if package service is available
		checkCmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "shell", "service", "check", "package")
		checkOutput, _ := checkCmd.CombinedOutput()
		if strings.Contains(string(checkOutput), "not found") {
			continue
		}

		// Install via ADB
		cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "Can't find service: package") {
				continue // retry
			}
			if strings.Contains(string(output), "INSTALL_FAILED_INSUFFICIENT_STORAGE") {
				return fmt.Errorf("insufficient storage for dev-client APK (271MB)")
			}
			return fmt.Errorf("adb install failed: %s — %w", string(output), err)
		}

		log.Printf("dev-client installed on %s from %s", adbTarget, apkPath)
		return nil
	}

	return fmt.Errorf("dev-client install failed after 5 attempts: package service not ready")
}

// DevClientPackage is the Android package the template dev client builds —
// backend/template/app.json's expo.android.package, the applicationId `expo
// prebuild` bakes into the APK this manager serves and installs. It is
// exported because it is the one name every installed-device question asks
// about: IsInstalledOnDevice probes it with `pm list packages`, whose filter
// is a substring match, so a name that disagrees with the build answers
// empty for a client that IS installed — and InstallDevClient then
// re-installs the whole APK behind Metro's start on every session
// (FM-DEVd(ii): the literal once read com.talon.devclient while the build
// produced com.talon.app). TestInstalledCheckProbesTheTemplateBuild pins it
// to the file; never edit one without the other.
const DevClientPackage = "com.talon.devclient"

// IsInstalledOnDevice checks if the dev-client is already installed on the device.
func (m *Manager) IsInstalledOnDevice(adbTarget string) bool {
	cmd := exec.Command("adb", "-s", adbTarget, "shell",
		"pm", "list", "packages", DevClientPackage)
	output, err := cmd.CombinedOutput()
	return err == nil && len(output) > 0
}

// CheckRebuildNeeded evaluates whether new dependencies require a native rebuild.
// It does NOT use os.Chdir (not goroutine-safe). Instead, it passes the workspace
// path to the allowlist checker which uses absolute paths for all file operations.
func (m *Manager) CheckRebuildNeeded(workspacePath string, newDeps map[string]string) (bool, string) {
	return m.allowlist.NeedsRebuildInDir(workspacePath, newDeps)
}

// StartRebuild launches an async native rebuild for a session.
func (m *Manager) StartRebuild(ctx context.Context, sessionID int64, workspacePath, adbTarget string) *RebuildState {
	m.mu.Lock()
	defer m.mu.Unlock()

	lastGoodAPK := m.DevClientAPKPath()

	state := &RebuildState{
		SessionID:   sessionID,
		Status:      "pending",
		StartedAt:   time.Now(),
		LastGoodAPK: lastGoodAPK,
	}
	m.rebuilds[sessionID] = state

	go m.runRebuild(ctx, sessionID, workspacePath, adbTarget)

	return state
}

// GetRebuildState returns the current rebuild state for a session.
func (m *Manager) GetRebuildState(sessionID int64) *RebuildState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rebuilds[sessionID]
}

// ClearRebuild removes the rebuild state for a session.
func (m *Manager) ClearRebuild(sessionID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rebuilds, sessionID)
}

func (m *Manager) runRebuild(ctx context.Context, sessionID int64, workspacePath, adbTarget string) {
	m.mu.Lock()
	state := m.rebuilds[sessionID]
	state.Status = "building"
	m.mu.Unlock()

	log.Printf("session %d: starting background native rebuild", sessionID)

	// Find Java and Android SDK
	javaHome := sdk.JavaHome()
	androidHome := sdk.AndroidHome()
	if javaHome == "" || androidHome == "" {
		m.finishRebuild(sessionID, "", "Java JDK 17 or Android SDK not found")
		return
	}

	// Set up host-side cache directories for Gradle and npm
	homeDir, _ := os.UserHomeDir()
	gradleCacheDir := filepath.Join(homeDir, ".gradle", "caches")
	npmCacheDir := filepath.Join(homeDir, ".npm")
	os.MkdirAll(gradleCacheDir, 0755)
	os.MkdirAll(npmCacheDir, 0755)

	// Find project-local caches
	projectGradleCache := filepath.Join(workspacePath, "android", ".gradle")
	projectNpmCache := filepath.Join(workspacePath, "node_modules", ".cache")

	env := append(os.Environ(),
		"JAVA_HOME="+javaHome,
		"ANDROID_HOME="+androidHome,
		"PATH="+javaHome+"/bin:"+androidHome+"/platform-tools:"+os.Getenv("PATH"),
		// Gradle caching
		"GRADLE_USER_HOME="+filepath.Join(homeDir, ".gradle"),
		"GRADLE_CACHE="+gradleCacheDir,
		// npm caching
		"npm_config_cache="+npmCacheDir,
	)

	// Clean build artifacts for incremental builds (preserve caches)
	if _, err := os.Stat(projectGradleCache); err == nil {
		// Keep gradle cache, only clean build outputs
		buildDir := filepath.Join(workspacePath, "android", "app", "build")
		os.RemoveAll(buildDir)
	}
	if _, err := os.Stat(projectNpmCache); err == nil {
		// Keep npm cache, only clean metro cache
		metroCache := filepath.Join(workspacePath, "node_modules", ".cache", "metro-*")
		_ = metroCache // metro cache cleanup handled by metro itself
	}

	// Step 1: expo prebuild (incremental — preserves android/ if already exists)
	prebuildCmd := exec.CommandContext(ctx, "npx", "expo", "prebuild", "-p", "android", "--no-install")
	prebuildCmd.Dir = workspacePath
	prebuildCmd.Env = env
	if output, err := prebuildCmd.CombinedOutput(); err != nil {
		m.finishRebuild(sessionID, "", fmt.Sprintf("expo prebuild failed: %s — %s", err.Error(), string(output)))
		return
	}

	// Step 2: gradle assembleDebug (uses cached dependencies)
	androidDir := filepath.Join(workspacePath, "android")
	gradleCmd := exec.CommandContext(ctx, "./gradlew", "assembleDebug", "--build-cache", "--daemon")
	gradleCmd.Dir = androidDir
	gradleCmd.Env = env
	if output, err := gradleCmd.CombinedOutput(); err != nil {
		m.finishRebuild(sessionID, "", fmt.Sprintf("gradle build failed: %s — %s", err.Error(), string(output)))
		return
	}

	// Find the built APK
	apkPath := findAPK(androidDir)
	if apkPath == "" {
		m.finishRebuild(sessionID, "", "gradle build succeeded but APK not found")
		return
	}

	// Step 3: install on device
	cmd := exec.CommandContext(ctx, "adb", "-s", adbTarget, "install", "-r", apkPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		m.finishRebuild(sessionID, "", fmt.Sprintf("adb install failed: %s — %s", err.Error(), string(output)))
		return
	}

	m.finishRebuild(sessionID, apkPath, "")
	log.Printf("session %d: background rebuild completed successfully", sessionID)
}

func (m *Manager) finishRebuild(sessionID int64, newAPK, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.rebuilds[sessionID]
	if !ok {
		return
	}

	now := time.Now()
	state.CompletedAt = &now
	state.NewAPK = newAPK

	if errMsg != "" {
		state.Status = "failed"
		state.Error = errMsg
		log.Printf("session %d: rebuild failed: %s", sessionID, errMsg)
	} else {
		state.Status = "success"
		log.Printf("session %d: rebuild succeeded, new APK: %s", sessionID, newAPK)
	}
}

// findJavaHome locates Java JDK 17 on the system.
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
		if filepath.Ext(path) == ".apk" {
			found = path
			return filepath.SkipDir
		}
		return nil
	})

	return found
}

// GenerateAllowlistFromBuild scans a built android project and generates an allowlist.
func GenerateAllowlistFromBuild(templateDir, androidDir, sdkVersion string) (*Allowlist, error) {
	verFile := filepath.Join(templateDir, ".template-version")
	data, err := os.ReadFile(verFile)
	if err != nil {
		return nil, fmt.Errorf("read template version: %w", err)
	}
	version := string(data)

	// Parse android/app/build.gradle for native module imports
	var modules []NativeModule

	gradlePath := filepath.Join(androidDir, "app", "build.gradle")
	if _, err := os.Stat(gradlePath); err == nil {
		gradleData, err := os.ReadFile(gradlePath)
		if err == nil {
			// Simple scan for implementation lines
			lines := splitLines(string(gradleData))
			for _, line := range lines {
				line = trimSpace(line)
				if len(line) > 0 && line[0] == '#' {
					continue
				}
				// Look for native module patterns
				if containsAny(line, "implementation", "api ") {
					modules = append(modules, NativeModule{
						Name:          extractPackageFromGradle(line),
						NativeLinking: true,
					})
				}
			}
		}
	}

	al := &Allowlist{
		TemplateVersion: version,
		SDKVersion:      sdkVersion,
		BuiltAt:         time.Now().UTC().Format(time.RFC3339),
		Modules:         modules,
	}

	return al, nil
}

// splitLines splits a string into lines.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// trimSpace trims whitespace from a string.
func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// containsAny checks if s contains any of the given substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(sub) <= len(s) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// extractPackageFromGradle extracts a package name from a gradle line.
func extractPackageFromGradle(line string) string {
	// Simple heuristic: find content between quotes or after ':'
	inQuote := false
	start := -1
	for i := 0; i < len(line); i++ {
		if line[i] == '\'' || line[i] == '"' {
			if inQuote {
				return line[start:i]
			}
			inQuote = true
			start = i + 1
		}
	}

	// Try after colon
	for i := 0; i < len(line); i++ {
		if line[i] == ':' {
			end := i + 1
			for end < len(line) && line[end] != ' ' && line[end] != '\t' {
				end++
			}
			return line[i+1 : end]
		}
	}

	return line
}
