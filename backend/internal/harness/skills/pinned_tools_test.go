package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// toolsRegistry serves "talon-codegen" (one good tool, plus declarations
// that must skip) and, when withSecond, "talon-utils" (a good tool) — the
// second plugin proves one bad plugin never removes the good one's tools.
func toolsRegistry(t *testing.T, withSecond bool, tamperCodegen bool) *httptest.Server {
	t.Helper()
	codegenScript := "console.log(process.argv)\n"
	manifest := map[string]any{
		"name": "talon-codegen", "version": "1.0.0",
		"description": "codegen", "entry": "index.js", "skills": []string{},
		"tools": []map[string]any{
			{
				"name": "gen_api_stub", "description": "generate an API stub",
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"model": map[string]any{"type": "string"}},
					"required":   []string{"model"},
				},
				"command": []string{"node", "scripts/gen.js"},
			},
			// All three below must skip: mutation, bad name, no command.
			{"name": "publish", "mutating": true, "command": []string{"node", "scripts/pub.js"}},
			{"name": "bad name", "command": []string{"node", "x.js"}},
			{"name": "nocmd"},
		},
		"license": "MIT",
		"files": map[string]string{
			"scripts/gen.js": hashOf([]byte(codegenScript)),
			"scripts/pub.js": hashOf([]byte("// pub\n")),
			"index.js":       hashOf([]byte("// entry\n")),
		},
	}
	if tamperCodegen {
		// Manifest pins ORIGINAL; the file handler serves TAMPERED.
		manifest["files"].(map[string]string)["scripts/gen.js"] = hashOf([]byte("ORIGINAL"))
	}
	utilsManifest := map[string]any{
		"name": "talon-utils", "version": "2.1.0", "description": "utils",
		"skills": []string{},
		"tools": []map[string]any{
			{"name": "lint_fix", "description": "auto-fix lint", "command": []string{"node", "fix.js"}},
		},
		"license": "MIT",
		"files":   map[string]string{"fix.js": hashOf([]byte("// fix\n"))},
	}

	plugins := []map[string]any{
		{"name": "talon-codegen", "version": "1.0.0", "path": "plugins/talon-codegen", "license": "MIT"},
	}
	if withSecond {
		plugins = append(plugins,
			map[string]any{"name": "talon-utils", "version": "2.1.0", "path": "plugins/talon-utils", "license": "MIT"})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins", "version": 1, "base": "http://" + r.Host,
			"plugins": plugins,
		})
	})
	mux.HandleFunc("/plugins/talon-codegen/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(manifest)
	})
	mux.HandleFunc("/plugins/talon-codegen/scripts/gen.js", func(w http.ResponseWriter, r *http.Request) {
		if tamperCodegen {
			w.Write([]byte("TAMPERED"))
			return
		}
		w.Write([]byte(codegenScript))
	})
	mux.HandleFunc("/plugins/talon-codegen/scripts/pub.js", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("// pub\n"))
	})
	mux.HandleFunc("/plugins/talon-codegen/index.js", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("// entry\n"))
	})
	if withSecond {
		mux.HandleFunc("/plugins/talon-utils/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(utilsManifest)
		})
		mux.HandleFunc("/plugins/talon-utils/fix.js", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("// fix\n"))
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Pinned workspaces register declared tools; everything malformed skips.
// This is the W11 gate clause: "plugin tool registers only for pinned
// workspaces."
func TestPinnedToolsRegisterOnlyReadDeclarations(t *testing.T) {
	srv := toolsRegistry(t, false, false)
	cache := t.TempDir()
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: cache}

	// No pins: honest absence, no I/O failure.
	got, err := p.PinnedTools(nil)
	if err != nil || got != nil {
		t.Fatalf("no pins must yield nothing: %v, %v", got, err)
	}
	// No cache dir: nowhere to materialize an executable — absent too.
	nocache := RegistryProvider{IndexURL: srv.URL + "/registry.json"}
	got, err = nocache.PinnedTools(map[string]string{"talon-codegen": "1.0.0"})
	if err != nil || got != nil {
		t.Fatalf("no cache dir must yield nothing: %v, %v", got, err)
	}

	pins := map[string]string{"talon-codegen": "1.0.0"}
	got, err = p.PinnedTools(pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly the one read-only tool, got %+v", got)
	}
	d := got[0]
	if d.Plugin != "talon-codegen" || d.Name != "gen_api_stub" {
		t.Errorf("decl = %+v", d)
	}
	wantDir := filepath.Join(cache, "talon-codegen", "1.0.0")
	if d.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", d.Dir, wantDir)
	}
	if len(d.Command) != 2 || d.Command[0] != "node" {
		t.Errorf("Command = %v", d.Command)
	}
	if !json.Valid(d.InputSchema) {
		t.Errorf("InputSchema not valid JSON: %s", d.InputSchema)
	}
	// Every hash-pinned file materialized into the run dir — the exec
	// boundary depends on it.
	body, err := os.ReadFile(filepath.Join(wantDir, "scripts", "gen.js"))
	if err != nil {
		t.Fatalf("script not materialized: %v", err)
	}
	if string(body) != "console.log(process.argv)\n" {
		t.Errorf("script body = %q", body)
	}
}

// A hash-mismatched plugin fails closed — its tools absent — while the
// other pinned plugin's tools still register.
func TestPinnedToolsTamperFailsPluginClosedOnly(t *testing.T) {
	srv := toolsRegistry(t, true, true)
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: t.TempDir()}
	got, err := p.PinnedTools(map[string]string{
		"talon-codegen": "1.0.0",
		"talon-utils":   "2.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		if d.Plugin == "talon-codegen" {
			t.Errorf("tampered plugin's tools must be absent, got %+v", d)
		}
	}
	if len(got) != 1 || got[0].Plugin != "talon-utils" || got[0].Name != "lint_fix" {
		t.Errorf("good plugin's tools must survive: %+v", got)
	}
}

// After a warm cache, an unreachable registry serves verified declarations
// from cache (LoadForPins' offline posture, applied to tools).
func TestPinnedToolsServedFromCacheWhenIndexUnreachable(t *testing.T) {
	srv := toolsRegistry(t, false, false)
	cache := t.TempDir()
	pins := map[string]string{"talon-codegen": "1.0.0"}
	p := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: cache}
	warm, err := p.PinnedTools(pins)
	if err != nil || len(warm) != 1 {
		t.Fatalf("warm pass: %+v, %v", warm, err)
	}
	srv.Close() // registry goes away entirely

	cold := RegistryProvider{IndexURL: srv.URL + "/registry.json", CacheDir: cache}
	got, err := cold.PinnedTools(pins)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "gen_api_stub" {
		t.Errorf("cache-only pass must serve the declaration, got %+v", got)
	}
}
