package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureAbiFilters(t *testing.T) {
	t.Run("inserts when absent", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "build.gradle")
		os.WriteFile(p, []byte(`
    defaultConfig {
        applicationId 'com.talon.app'
        versionCode 1
    }
`), 0o644)
		if err := ensureAbiFilters(p); err != nil {
			t.Fatal(err)
		}
		out, _ := os.ReadFile(p)
		s := string(out)
		if !strings.Contains(s, `abiFilters "x86_64", "arm64-v8a"`) || !strings.Contains(s, "ndk {") {
			t.Fatalf("filters not inserted:\n%s", s)
		}
		// defaultConfig structure preserved.
		if strings.Count(s, "defaultConfig {") != 1 {
			t.Fatalf("defaultConfig count changed:\n%s", s)
		}
	})
	t.Run("idempotent and normalizes existing", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "build.gradle")
		os.WriteFile(p, []byte(`
    defaultConfig {
        applicationId 'com.talon.app'
        ndk { abiFilters "armeabi-v7a", "x86" }
    }
`), 0o644)
		if err := ensureAbiFilters(p); err != nil {
			t.Fatal(err)
		}
		if err := ensureAbiFilters(p); err != nil { // second run: no-op
			t.Fatal(err)
		}
		out, _ := os.ReadFile(p)
		if strings.Contains(string(out), "armeabi") {
			t.Fatalf("stale ABIs survived:\n%s", string(out))
		}
		if strings.Count(string(out), "ndk {") != 1 {
			t.Fatalf("duplicated ndk block:\n%s", string(out))
		}
	})
	t.Run("fails loudly without applicationId", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "build.gradle")
		os.WriteFile(p, []byte("android {\n}\n"), 0o644)
		if err := ensureAbiFilters(p); err == nil {
			t.Fatal("want error for tree without applicationId")
		}
	})
}
