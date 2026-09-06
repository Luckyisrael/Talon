// Package sdk is the single home for host SDK discovery: JDK, Android SDK,
// and apksigner. Three copies of this logic drifted across release,
// devclient, and tools (different candidate lists, different fallbacks) —
// the release and dev-client paths must pick the SAME SDK or builds and
// installs disagree about the toolchain. These are the most thorough
// variants of each search.
package sdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// JavaHome locates a JDK 17 installation, or "" when none is found.
func JavaHome() string {
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		return jh
	}
	candidates := []string{
		"/usr/lib/jvm/java-17-openjdk-amd64",
		"/usr/lib/jvm/java-17-openjdk",
		"/usr/lib/jvm/java-17",
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "bin", "java")); err == nil {
			return c
		}
	}
	if out, err := exec.Command("which", "java").Output(); err == nil {
		javaPath := strings.TrimSpace(string(out))
		if javaPath != "" {
			if resolved, rerr := filepath.EvalSymlinks(javaPath); rerr == nil {
				javaPath = resolved
			}
			return filepath.Dir(filepath.Dir(javaPath))
		}
	}
	return ""
}

// AndroidHome locates the Android SDK (verified by adb's presence), or ""
// when none is found.
func AndroidHome() string {
	if ah := os.Getenv("ANDROID_HOME"); ah != "" {
		return ah
	}
	if ah := os.Getenv("ANDROID_SDK_ROOT"); ah != "" {
		return ah
	}
	homeDir, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(homeDir, "Android", "Sdk"),
		"/opt/android-sdk",
		"/usr/local/android-sdk",
		filepath.Join(homeDir, "android-sdk"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "platform-tools", "adb")); err == nil {
			return c
		}
	}
	return ""
}

// Apksigner picks the newest installed build-tools apksigner.
func Apksigner(androidHome string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(androidHome, "build-tools"))
	if err != nil {
		return "", fmt.Errorf("no build-tools in %s", androidHome)
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no build-tools versions installed")
	}
	sort.Strings(versions)
	for i := len(versions) - 1; i >= 0; i-- {
		p := filepath.Join(androidHome, "build-tools", versions[i], "apksigner")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("apksigner not found in any build-tools version")
}
