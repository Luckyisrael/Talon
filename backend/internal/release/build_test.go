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

// FM-FPRa: the signature gate must compare the certificate's bytes, never
// its spelling. apksigner prints bare lowercase hex, keytool prints
// colon-delimited uppercase — same 32 bytes. The live pair below is
// verbatim from export job 4's verify-signature failure (08:02:31), the
// first run to reach it.
//
// RED pre-fix: sameFingerprint strips spaces/case but not colons, so the
// correct signature is accused of mismatch. GREEN: format equates,
// different/truncated/empty fingerprints stay refused.
func TestSameFingerprintComparesBytesNotSpelling(t *testing.T) {
	apksignerStyle := "a6cedcdae06bffce6ef94b29b6893522517291f2b6c83d020420f3127cac5584"
	keytoolStyle := "A6:CE:DC:DA:E0:6B:FF:CE:6E:F9:4B:29:B6:89:35:22:51:72:91:F2:B6:C8:3D:02:04:20:F3:12:7C:AC:55:84"
	if !sameFingerprint(apksignerStyle, keytoolStyle) {
		t.Fatal("FM-FPRa: the SAME certificate spelled two ways must equate — pre-fix this is export job 4's verify-signature failure: apksigner's bare hex vs keytool's colon-delimited digest")
	}
	if !sameFingerprint(keytoolStyle, apksignerStyle) {
		t.Error("comparison must be symmetric")
	}
	if sameFingerprint(apksignerStyle, apksignerStyle[:63]) {
		t.Error("a truncated fingerprint must not pass")
	}
	other := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if sameFingerprint(apksignerStyle, other) {
		t.Error("a different certificate must not pass")
	}
	if sameFingerprint("", "") {
		t.Error("two empty fingerprints must never pass")
	}
}
