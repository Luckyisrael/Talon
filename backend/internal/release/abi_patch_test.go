package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAbiFilters(t *testing.T) {
	write := func(dir, name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("pins gradle.properties and normalizes stale ndk block", func(t *testing.T) {
		dir := t.TempDir()
		props := write(dir, "gradle.properties", "hermesEngine=true\nreactNativeArchitectures=armeabi-v7a,arm64-v8a,x86,x86_64\n")
		gradle := write(dir, "build.gradle", "android {\n  defaultConfig {\n    applicationId 'com.x'\n    ndk { abiFilters \"armeabi-v7a\", \"x86\" }\n  }\n}\n")
		if err := ensureAbiFilters(props, gradle); err != nil {
			t.Fatal(err)
		}
		if err := ensureAbiFilters(props, gradle); err != nil { // idempotent
			t.Fatal(err)
		}
		out, _ := os.ReadFile(props)
		if !strings.Contains(string(out), "reactNativeArchitectures=x86_64,arm64-v8a\n") {
			t.Fatalf("architectures not pinned:\n%s", out)
		}
		if strings.Contains(string(out), "armeabi") {
			t.Fatalf("stale architectures survived:\n%s", out)
		}
		g, _ := os.ReadFile(gradle)
		if strings.Contains(string(g), "armeabi") {
			t.Fatalf("stale ndk block survived:\n%s", g)
		}
		if strings.Count(string(g), "ndk {") != 1 {
			t.Fatalf("ndk block duplicated:\n%s", g)
		}
	})
	t.Run("appends when the property is absent", func(t *testing.T) {
		dir := t.TempDir()
		props := write(dir, "gradle.properties", "newArchEnabled=true\n")
		gradle := write(dir, "build.gradle", "android {\n}\n")
		if err := ensureAbiFilters(props, gradle); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(props)
		if !strings.Contains(string(out), "reactNativeArchitectures=x86_64,arm64-v8a") {
			t.Fatalf("architectures not appended:\n%s", out)
		}
	})
	t.Run("fails loudly when gradle.properties is unreadable", func(t *testing.T) {
		dir := t.TempDir()
		gradle := write(dir, "build.gradle", "android {\n}\n")
		if err := ensureAbiFilters(filepath.Join(dir, "missing.properties"), gradle); err == nil {
			t.Fatal("want error for missing gradle.properties")
		}
	})
}
