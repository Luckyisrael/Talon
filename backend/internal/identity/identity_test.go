package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// shippedApp is a package+scheme pair that a studio-shipped app already owns,
// read out of that app's own app.json so the reserved set can never be a
// second literal drifting away from the files.
type shippedApp struct {
	name   string
	pkg    string
	scheme string
}

func shippedApps(t *testing.T) []shippedApp {
	t.Helper()

	paths := []struct{ name, path string }{
		{"the Talon mobile app", filepath.Join("..", "..", "..", "talonmobileapp", "app.json")},
		{"the prebuilt dev client", filepath.Join("..", "..", "template", "app.json")},
	}

	var out []shippedApp
	for _, p := range paths {
		raw, err := os.ReadFile(p.path)
		if err != nil {
			t.Fatalf("read %s: %v", p.path, err)
		}
		var cfg struct {
			Expo struct {
				Scheme  string `json:"scheme"`
				Android struct {
					Package string `json:"package"`
				} `json:"android"`
			} `json:"expo"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("parse %s: %v", p.path, err)
		}
		if cfg.Expo.Android.Package == "" || cfg.Expo.Scheme == "" {
			t.Fatalf("%s declares package=%q scheme=%q — both are needed to reserve", p.path, cfg.Expo.Android.Package, cfg.Expo.Scheme)
		}
		out = append(out, shippedApp{name: p.name, pkg: cfg.Expo.Android.Package, scheme: cfg.Expo.Scheme})
	}
	return out
}

// FM-DEVd(iii) — written before the fix (TEST_RULES §3). Project identity is
// derived from the user's project name with no reserved set: a project named
// "App" is handed `com.talon.app` (the studio's own app) and one named
// "Talon" the studio's `talon://` scheme. `run_boot` installs the project's
// APK with `adb install -r`, which evicts whatever currently holds that
// package, and a claimed scheme redirects the studio's own deep links.
//
// Red before L33: `Generate("App")` → `com.talon.app`, `Generate("Talon")`
// → scheme `talon`; neither is refused by EnsureUnique.
func TestProjectIdentityRefusesStudioIdentity(t *testing.T) {
	shipped := shippedApps(t)
	// Collisions against *other projects* are the checkers' job; this is
	// about identities no other project can ever hold either.
	neverCollides := func(string) bool { return false }

	for _, name := range []string{"App", "Devclient", "Talon", "Talon Studio"} {
		got := EnsureUnique(Generate(name), neverCollides, neverCollides)
		for _, s := range shipped {
			if got.AndroidPackage == s.pkg {
				t.Errorf("FM-DEVd(iii): a project named %q was handed the package %q, which %s already owns — installing it evicts that app from the device", name, got.AndroidPackage, s.name)
			}
			if got.Scheme == s.scheme {
				t.Errorf("FM-DEVd(iii): a project named %q was handed the scheme %q, which %s already owns — its deep links would resolve to that app instead", name, got.Scheme, s.name)
			}
		}
	}
}

// FM-DEVd(iv) — written before the fix (TEST_RULES §3). renderAppJSON writes
// the project's package only into an `expo.android` object that already
// exists; when the template has none, the rendered app.json carries no
// package of its own and `expo prebuild` falls back to whatever default it
// finds — after L33 that default could be the dev client's own identity.
//
// Red before L33: a template with no `expo.android` block renders an
// app.json whose expo.android.package is absent, not the project's.
func TestRenderedAppJSONCarriesTheProjectPackageNotTheTemplates(t *testing.T) {
	tmpl := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpl, "app.json"),
		[]byte(`{"expo":{"name":"T","slug":"t","scheme":"talonapp"}}`), 0o644); err != nil {
		t.Fatalf("write template app.json: %v", err)
	}
	upper := t.TempDir()

	id := Generate("Counter App")
	if err := renderAppJSON(upper, id, tmpl); err != nil {
		t.Fatalf("renderAppJSON: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(upper, "app.json"))
	if err != nil {
		t.Fatalf("read rendered app.json: %v", err)
	}
	var cfg struct {
		Expo struct {
			Android struct {
				Package string `json:"package"`
			} `json:"android"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse rendered app.json: %v", err)
	}
	if cfg.Expo.Android.Package != id.AndroidPackage {
		t.Fatalf("FM-DEVd(iv): rendered expo.android.package = %q, want the project's own %q — the template carried no android block to write into", cfg.Expo.Android.Package, id.AndroidPackage)
	}
}
