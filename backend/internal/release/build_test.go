package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)


func TestPatchApplicationID(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "build.gradle")
	before := "android {\n    namespace 'com.old.app'\n    defaultConfig {\n        applicationId 'com.old.app'\n    }\n}\n"
	if err := os.WriteFile(p, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := patchApplicationID(p, "com.new.app"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), "namespace 'com.new.app'") ||
		!strings.Contains(string(after), "applicationId 'com.new.app'") {
		t.Errorf("both lines should be pinned:\n%s", after)
	}
	if strings.Contains(string(after), "com.old.app") {
		t.Errorf("no trace of the old package should remain:\n%s", after)
	}
}

func TestPatchApplicationIDRefusesUnknown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "build.gradle")
	_ = os.WriteFile(p, []byte("// no identity here\n"), 0o644)
	if err := patchApplicationID(p, "com.new.app"); err == nil {
		t.Error("missing lines must fail loudly, not guess")
	}
	if err := patchApplicationID(filepath.Join(dir, "missing.gradle"), "com.new.app"); err == nil {
		t.Error("missing file must fail")
	}
}
