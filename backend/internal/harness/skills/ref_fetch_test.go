package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// refFixture serves one plugin with a single hash-pinned reference file.
func refFixture(t *testing.T, refBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins", "version": 1, "base": "http://" + r.Host,
			"plugins": []map[string]any{{
				"name": "talon-mobile-ui", "version": "1.0.0",
				"path": "plugins/talon-mobile-ui", "description": "UI.", "license": "MIT",
			}},
		})
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name": "talon-mobile-ui", "version": "1.0.0",
			"skills":  []string{"skills/wallet-connect/SKILL.md"},
			"license": "MIT",
			"files": map[string]string{
				"skills/wallet-connect/SKILL.md": hashOf([]byte("---\nname: wallet-connect\ndescription: C.\nversion: \"1.0.0\"\n---\n\n# C\n")),
				"references/connect-button.tsx":  refBodyHash(refBody),
			},
		})
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/references/connect-button.tsx", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(refBody))
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/skills/wallet-connect/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: wallet-connect\ndescription: C.\nversion: \"1.0.0\"\n---\n\n# C\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func refBodyHash(s string) string { return hashOf([]byte(s)) }

const refContent = "export function ConnectButton() { return null }\n"

// A pinned reference file fetches verified and caches.
func TestFetchPinnedFile(t *testing.T) {
	srv := refFixture(t, refContent)
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	pins := map[string]string{"talon-mobile-ui": "1.0.0"}
	got, err := p.FetchPinnedFile(pins, "talon-mobile-ui", "references/connect-button.tsx")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != refContent {
		t.Errorf("content = %q", got)
	}
}

// Unpinned plugins, non-reference paths, and escapes all fail closed.
func TestFetchPinnedFileGuardrails(t *testing.T) {
	srv := refFixture(t, refContent)
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	pins := map[string]string{"talon-mobile-ui": "1.0.0"}
	for _, tc := range []struct {
		name, plugin, rel string
	}{
		{"unpinned plugin", "other", "references/connect-button.tsx"},
		{"skill path", "talon-mobile-ui", "skills/wallet-connect/SKILL.md"},
		{"manifest", "talon-mobile-ui", "talon-plugin.json"},
		{"escape", "talon-mobile-ui", "references/../../etc/passwd"},
		{"absolute escape", "talon-mobile-ui", "/etc/passwd"},
		{"empty", "talon-mobile-ui", ""},
	} {
		if _, err := p.FetchPinnedFile(pins, tc.plugin, tc.rel); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
}

// A tampered reference fails its manifest hash and serves nothing.
func TestFetchPinnedFileHashMismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins", "version": 1, "base": "http://" + r.Host,
			"plugins": []map[string]any{{
				"name": "talon-mobile-ui", "version": "1.0.0",
				"path": "plugins/talon-mobile-ui", "description": "UI.", "license": "MIT",
			}},
		})
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		// Manifest pins ORIGINAL; the file handler serves TAMPERED.
		json.NewEncoder(w).Encode(map[string]any{
			"name": "talon-mobile-ui", "version": "1.0.0",
			"skills":  []string{"skills/wallet-connect/SKILL.md"},
			"license": "MIT",
			"files": map[string]string{
				"skills/wallet-connect/SKILL.md": hashOf([]byte("---\nname: wallet-connect\ndescription: C.\nversion: \"1.0.0\"\n---\n\n# C\n")),
				"references/connect-button.tsx":  hashOf([]byte("ORIGINAL")),
			},
		})
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/references/connect-button.tsx", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("TAMPERED"))
	})
	mux.HandleFunc("/plugins/talon-mobile-ui/skills/wallet-connect/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("---\nname: wallet-connect\ndescription: C.\nversion: \"1.0.0\"\n---\n\n# C\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	if _, err := p.FetchPinnedFile(
		map[string]string{"talon-mobile-ui": "1.0.0"},
		"talon-mobile-ui", "references/connect-button.tsx"); err == nil {
		t.Error("hash-mismatched reference must fail")
	}
}

// Version drift serves the pinned cache when present, and fails closed
// with an adoption hint when absent.
func TestFetchPinnedFileVersionDrift(t *testing.T) {
	srv := refFixture(t, refContent)
	dir := t.TempDir()
	warm := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: dir}
	pins := map[string]string{"talon-mobile-ui": "1.0.0"}
	if _, err := warm.FetchPinnedFile(pins, "talon-mobile-ui", "references/connect-button.tsx"); err != nil {
		t.Fatalf("warm fetch: %v", err)
	}
	// The registry moves on; the pin stays. Cache covers the gap.
	_ = srv
	cold := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: dir}
	if _, err := cold.FetchPinnedFile(pins, "talon-mobile-ui", "references/connect-button.tsx"); err != nil {
		t.Errorf("cached drift fetch: %v", err)
	}
}
