package devclient

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

const (
	templateDir     = "../../template"
	templateAppJSON = templateDir + "/app.json"
	mobileAppJSON   = "../../../talonmobileapp/app.json"
)

// expoAndroidPackage reads expo.android.package — the value `expo prebuild`
// turns into the installed applicationId. Read from the file itself, never
// from a list that could drift away from it.
func expoAndroidPackage(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var cfg struct {
		Expo struct {
			Android struct {
				Package string `json:"package"`
			} `json:"android"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if cfg.Expo.Android.Package == "" {
		t.Fatalf("%s declares no expo.android.package — nothing to compare against", path)
	}
	return cfg.Expo.Android.Package
}

// FM-DEVd(i) — written before the fix (TEST_RULES §3). The studio ships two
// apps to the same phone: the Talon mobile app the user builds from, and the
// dev client the QR opens. Android permits one installed holder per package
// name — the second install evicts the first or fails
// INSTALL_FAILED_UPDATE_INCOMPATIBLE — so both cannot be `com.talon.app`.
//
// Red before L33 (observed): both app.json files read `com.talon.app`.
func TestDevClientPackageIsItsOwnNotTheStudioApps(t *testing.T) {
	mobile := expoAndroidPackage(t, mobileAppJSON)
	devClient := expoAndroidPackage(t, templateAppJSON)

	if devClient == mobile {
		t.Fatalf("FM-DEVd(i): the dev client and the Talon mobile app both claim package %q — a phone can install only one holder of a package name, so the app the QR opens and the app the user builds from cannot coexist", mobile)
	}
}

// FM-DEVd(ii) — written before the fix (TEST_RULES §3). IsInstalledOnDevice
// asks `pm list packages <filter>`; the filter is a substring match, so the
// name it probes must be the name the template build installs. A second,
// unchecked literal is exactly how the check came to probe
// `com.talon.devclient` while the build produced `com.talon.app`: the check
// was always false, and InstallDevClient re-installed the whole APK behind
// Metro's start on every session.
//
// Red before L33: compile failure — `DevClientPackage undefined`, the same
// shape L17/L23/L24 shipped their red as (the constant the check must probe
// does not exist yet).
func TestInstalledCheckProbesTheTemplateBuild(t *testing.T) {
	built := expoAndroidPackage(t, templateAppJSON)
	if DevClientPackage != built {
		t.Fatalf("FM-DEVd(ii): the installed-check probes %q but the template builds %q — pm list packages answers empty for a client that IS installed, so every session start reinstalls it", DevClientPackage, built)
	}
}

// FM-DEVd(ii), artifact half — the check is honest only if the APK on disk
// declares what the source says it does. The package name lives in the APK's
// binary AndroidManifest.xml as UTF-16LE (measured on the shipped build: 8
// occurrences of `com.talon.app` in that encoding, 0 in UTF-8, 0 of
// `com.talon.devclient`), so a byte match reads the artifact without an
// Android SDK.
//
// MEANT TO STAY RED until build-dev-client.sh is re-run: the artifact, not
// only the source, has to carry the new package. Skip-if-absent, like
// TestDevClientAPKRealBuildRangeProbe.
func TestShippedDevClientAPKDeclaresItsOwnPackage(t *testing.T) {
	rawVersion, err := os.ReadFile(filepath.Join(templateDir, ".template-version"))
	if err != nil {
		t.Skipf("no template version at %s: %v", templateDir, err)
	}
	apk := DevClientAPKPath(templateDir, strings.TrimSpace(string(rawVersion)))

	manifest, err := apkManifest(apk)
	if err != nil {
		t.Skipf("dev-client APK not built (%s) — run build-dev-client.sh first: %v", apk, err)
	}

	want := utf16le(DevClientPackage)
	if bytes.Contains(manifest, want) {
		return
	}
	mobile := expoAndroidPackage(t, mobileAppJSON)
	t.Fatalf("FM-DEVd(ii): the shipped dev-client APK does not declare %q (%d matches in AndroidManifest.xml); the studio mobile app's %q is in it %d times — the artifact still carries the conflicting package, so re-run build-dev-client.sh",
		DevClientPackage, bytes.Count(manifest, want),
		mobile, bytes.Count(manifest, utf16le(mobile)))
}

// apkManifest returns AndroidManifest.xml straight out of the APK zip.
func apkManifest(apk string) ([]byte, error) {
	zr, err := zip.OpenReader(apk)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != "AndroidManifest.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()

		raw, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		if len(raw) < 8 {
			return nil, fmt.Errorf("AndroidManifest.xml in %s is only %d bytes", apk, len(raw))
		}
		if raw[0] != 0x03 || raw[1] != 0x00 {
			return nil, fmt.Errorf("AndroidManifest.xml in %s is not binary AXML (head %x)", apk, raw[:8])
		}
		return raw, nil
	}
	return nil, fmt.Errorf("no AndroidManifest.xml inside %s", apk)
}

// utf16le encodes s the way the AXML string pool stores it: UTF-16, little
// endian, no BOM. A UTF-8 match finds nothing — measured against the shipped
// build (0 hits for `com.talon.app`, 8 for its UTF-16LE form).
func utf16le(s string) []byte {
	encoded := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(encoded)*2)
	for _, u := range encoded {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}
