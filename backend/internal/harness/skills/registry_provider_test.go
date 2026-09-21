package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
)

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fixtureRegistry serves an index, one manifest, and two skills. flip maps
// paths to alternate bodies so tests can break hashes on demand.
func fixtureRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	skillA := "---\nname: web3-connect\ndescription: Connect a wallet.\nversion: \"1.0.0\"\nautoAttach: true\ntriggers: [import:foo]\n---\n\n# Connect\n"
	skillB := "---\nname: web3-balance\ndescription: Show a balance.\nversion: \"1.0.0\"\n---\n\n# Balance\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins",
			"version":  1,
			"base":     "http://" + r.Host,
			"plugins": []map[string]any{{
				"name": "web3-components", "version": "1.0.0",
				"path":        "plugins/web3-components",
				"description": "Wallet bits.", "license": "MIT",
			}},
		})
	})
	mux.HandleFunc("/plugins/web3-components/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name": "web3-components", "version": "1.0.0",
			"skills":  []string{"skills/web3-connect/SKILL.md", "skills/web3-balance/SKILL.md"},
			"license": "MIT",
			"files": map[string]string{
				"skills/web3-connect/SKILL.md": hashOf([]byte(skillA)),
				"skills/web3-balance/SKILL.md": hashOf([]byte(skillB)),
			},
		})
	})
	mux.HandleFunc("/plugins/web3-components/skills/web3-connect/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(skillA))
	})
	mux.HandleFunc("/plugins/web3-components/skills/web3-balance/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(skillB))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func providerFor(t *testing.T, srv *httptest.Server, pins map[string]string) RegistryProvider {
	t.Helper()
	return RegistryProvider{
		IndexURL: srv.URL + "/registry.json",
		Pins:     pins,
		CacheDir: t.TempDir(),
	}
}

// A pinned plugin resolves to verified bodies, and remote auto-attach plus
// triggers are stripped even though the file claims them: listing only,
// bodies through read_skill.
func TestRegistryResolvesPinnedPlugin(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, map[string]string{"web3-components": "1.0.0"})
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("skills = %+v, want 2", got)
	}
	byName := map[string]knowledge.Skill{}
	for _, s := range got {
		byName[s.Name] = s
	}
	connect, ok := byName["web3-connect"]
	if !ok || !strings.Contains(connect.Body, "# Connect") {
		t.Errorf("web3-connect missing or wrong body: %+v", byName)
	}
	if connect.AutoAttach {
		t.Error("remote autoAttach must be stripped")
	}
	if len(connect.Triggers) != 0 {
		t.Errorf("remote triggers must be stripped, got %v", connect.Triggers)
	}
	if _, ok := byName["web3-balance"]; !ok {
		t.Error("web3-balance missing")
	}
}

// A version the registry moved past fails closed: no partial skill set.
func TestRegistryVersionMismatchSkips(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, map[string]string{"web3-components": "9.9.9"})
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("skills = %+v, want none on version mismatch", got)
	}
}

// A body that fails its manifest hash fails the whole plugin, loudly.
func TestRegistryHashMismatchFailsPlugin(t *testing.T) {
	skillA := "---\nname: web3-connect\ndescription: Connect a wallet.\nversion: \"1.0.0\"\n---\n\n# Connect\n"
	tampered := "---\nname: web3-connect\ndescription: Tampered.\nversion: \"1.0.0\"\n---\n\n# Evil\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins", "version": 1, "base": "http://" + r.Host,
			"plugins": []map[string]any{{
				"name": "web3-components", "version": "1.0.0",
				"path": "plugins/web3-components", "description": "Wallet bits.", "license": "MIT",
			}},
		})
	})
	mux.HandleFunc("/plugins/web3-components/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		// Manifest pins the ORIGINAL bytes; the server hands over tampered ones.
		json.NewEncoder(w).Encode(map[string]any{
			"name": "web3-components", "version": "1.0.0",
			"skills":  []string{"skills/web3-connect/SKILL.md"},
			"license": "MIT",
			"files":   map[string]string{"skills/web3-connect/SKILL.md": hashOf([]byte(skillA))},
		})
	})
	mux.HandleFunc("/plugins/web3-components/skills/web3-connect/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(tampered))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := providerFor(t, srv, map[string]string{"web3-components": "1.0.0"})
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("hash-mismatched plugin served %d skills, want none", len(got))
	}
}

// Unknown plugin names and empty pins resolve to nothing, never an error.
func TestRegistryUnknownPluginSkips(t *testing.T) {
	srv := fixtureRegistry(t)
	p := providerFor(t, srv, map[string]string{"nope": "1.0.0"})
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("skills = %+v, want none", got)
	}
	empty := providerFor(t, srv, nil)
	got, err = empty.Load()
	if err != nil || len(got) != 0 {
		t.Errorf("empty pins: %v %+v, want nothing", err, got)
	}
}

// A dead index serves the verified cache: offline builds keep working.
func TestRegistryCacheFallback(t *testing.T) {
	dir := t.TempDir()
	srv := fixtureRegistry(t)
	warm := RegistryProvider{IndexURL: srv.URL + "/registry.json", Pins: map[string]string{"web3-components": "1.0.0"}, CacheDir: dir}
	if _, err := warm.Load(); err != nil {
		t.Fatalf("warm load: %v", err)
	}
	srv.Close() // index and files go dark
	cold := RegistryProvider{IndexURL: srv.URL + "/registry.json", Pins: map[string]string{"web3-components": "1.0.0"}, CacheDir: dir}
	got, err := cold.Load()
	if err != nil {
		t.Fatalf("cached load: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("cached skills = %+v, want 2", got)
	}
}

// Malformed pin segments are skipped, valid ones survive.
func TestParsePluginPins(t *testing.T) {
	got := ParsePluginPins("web3-components@1.0.0, broken, @1.0, name@, prediction-markets@0.3.1")
	if len(got) != 2 || got["web3-components"] != "1.0.0" || got["prediction-markets"] != "0.3.1" {
		t.Errorf("pins = %v", got)
	}
}

// Live verification against the real public directory. Opt-in only
// (TALON_LIVE_VERIFY=1): it needs network and the published repo, so CI
// never runs it. Run it after every directory-side change.
func TestLivePluginDirectory(t *testing.T) {
	if os.Getenv("TALON_LIVE_VERIFY") == "" {
		t.Skip("set TALON_LIVE_VERIFY=1 to verify against the live directory")
	}
	p := RegistryProvider{Pins: map[string]string{"talon-mobile-ui": "1.0.0"}, CacheDir: t.TempDir()}
	got, err := p.Load()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range got {
		names[s.Name] = true
		if s.AutoAttach {
			t.Errorf("remote skill %s must not auto-attach", s.Name)
		}
	}
	for _, want := range []string{"wallet-connect", "sol-balance", "sol-send"} {
		if !names[want] {
			t.Errorf("live directory missing skill %s (got %v)", want, names)
		}
	}
}
