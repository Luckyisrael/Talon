package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/sdk"
)

// FM-SIGNa: a secret shared between two readers through one file is
// consumed by the first. apksigner reads each `file:` password path as a
// single shared stream, so passing one passFile to both --ks-pass and
// --key-pass exhausts it on the keystore read and the key read hits
// "end of file reached" — the failure that killed live export job 3
// (07:26:32) after a 44-minute build, and the first time this codebase's
// life ever reached phase: sign.
//
// The fixture is built from the manifest with aapt (the same SDK the
// production path requires) — no binary APK in git. RED pre-fix:
// signAPK returns the byte-for-byte live error. GREEN: the signed
// artifact verifies and names this keystore's DN.
func TestSignAPKGivesEachReaderItsOwnPasswordFile(t *testing.T) {
	androidHome := sdk.AndroidHome()
	if androidHome == "" {
		t.Skip("Android SDK not available")
	}
	apksigner, err := sdk.Apksigner(androidHome)
	if err != nil {
		t.Skipf("apksigner not available: %v", err)
	}
	aapt := filepath.Join(filepath.Dir(apksigner), "aapt")
	if _, err := os.Stat(aapt); err != nil {
		t.Skip("aapt not available next to apksigner")
	}
	if _, err := os.Stat("/usr/bin/keytool"); err != nil {
		t.Skip("keytool not available")
	}
	jars, _ := filepath.Glob(filepath.Join(androidHome, "platforms", "android-*", "android.jar"))
	if len(jars) == 0 {
		t.Skip("no android.jar installed")
	}

	dir := t.TempDir()
	manifest := filepath.Join(dir, "AndroidManifest.xml")
	manifestXML := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android" package="com.talon.signprobe" android:versionCode="1" android:versionName="1.0">
  <uses-sdk android:minSdkVersion="26" android:targetSdkVersion="34"/>
  <application android:label="signprobe"/>
</manifest>
`
	if err := os.WriteFile(manifest, []byte(manifestXML), 0o644); err != nil {
		t.Fatal(err)
	}
	unsigned := filepath.Join(dir, "unsigned.apk")
	if out, err := exec.Command(aapt, "package", "-f", "-M", manifest, "-F", unsigned, "-I", jars[0]).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}

	// A real keystore through the production Ensure path.
	s := testStore(t)
	pid := testProject(t, s, "sign@test.com", "proj-sign", "com.talon.signprobe")
	k := &Keystore{Store: s, Cipher: testCipher(t), Dir: dir}
	rec, err := k.Ensure(context.Background(), pid)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	password, err := k.Password(rec)
	if err != nil || password == "" {
		t.Fatalf("stored password unusable: %v", err)
	}

	passesBefore := countPassFiles(t)
	b := &Builder{ExportsDir: filepath.Join(dir, "exports")}
	signed, err := b.signAPK(context.Background(), apksigner, unsigned, rec.Path, rec.Alias, password, pid)
	if err != nil {
		t.Fatalf("FM-SIGNa: signAPK must hand EACH password reader its own file — apksigner consumes a shared file on the keystore read, so the key read hits EOF. Pre-fix this fails exactly like live export job 3: %v", err)
	}
	if countPassFiles(t) != passesBefore {
		t.Errorf("signAPK left release-pass-* temp files behind (before=%d after=%d)", passesBefore, countPassFiles(t))
	}

	out, err := exec.Command(apksigner, "verify", "--print-certs", signed).CombinedOutput()
	if err != nil {
		t.Fatalf("signed artifact does not verify: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "CN=Talon Project") {
		t.Errorf("verify output does not name the project keystore's DN: %s", out)
	}
}

func countPassFiles(t *testing.T) int {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "release-pass-*"))
	return len(matches)
}
