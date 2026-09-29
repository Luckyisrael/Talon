package devclient

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"unicode/utf16"
)

// ManifestDeclares reports whether an APK's AndroidManifest.xml contains the
// given value — for a scheme or a package, the AXML string pool stores it
// UTF-16LE, which is what this matches (measured against the shipped build:
// `com.talon.app` appears 8 times as UTF-16LE and 0 times as UTF-8).
//
// It exists so a caller can ask the ARTIFACT what it declares instead of
// asking a source file. L33 taught that lesson with the package name: source
// and artifact had already drifted apart silently once, and every installed
// question answered wrongly because of it. FM-DEVf needs the same guarantee
// one level up — the scheme the server hands a phone in dev_launcher_link is
// only real if the client we ship actually registers it, because an
// ACTION_VIEW carrying a scheme no filter declares matches nothing at all
// and the tap that produced it does nothing.
func ManifestDeclares(apk, value string) (bool, error) {
	manifest, err := apkManifest(apk)
	if err != nil {
		return false, err
	}
	return bytes.Contains(manifest, utf16le(value)), nil
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
