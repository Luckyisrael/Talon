package ai

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// H12: the settings document flips routing on the next LoadRoutingPolicy
// without a restart; a malformed file holds the last good copy; the env
// path is untouched when no file is attached.
func TestRoutingSettingsHotReload(t *testing.T) {
	t.Setenv("AI_MODEL", "heavy-model")
	t.Setenv("AI_MODEL_CHEAP", "cheap-model")
	t.Setenv("ROUTING_POLICY_JSON", "")

	path := filepath.Join(t.TempDir(), "settings.json")
	AttachSettingsFile(path)
	t.Cleanup(func() { AttachSettingsFile("") })

	// No file yet: env defaults serve.
	if pol := LoadRoutingPolicy(); pol.FallbackModel != "heavy-model" {
		t.Fatalf("env policy not serving before a file exists: %+v", pol)
	}

	// The operator writes a settings file (the routing section).
	doc := `{"routing":{"fallback_model":"edited-fallback","models":{"strong":"edited-heavy"}}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	// mtime granularity: force the stamp to move.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	pol := LoadRoutingPolicy()
	if pol.FallbackModel != "edited-fallback" {
		t.Fatalf("fault: settings edit not picked up without restart: %+v", pol)
	}
	if pol.Models[TierStrong] != "edited-heavy" {
		t.Errorf("fault: tier mapping not applied: %+v", pol.Models)
	}

	// The file goes malformed: the last good copy keeps serving.
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	future2 := time.Now().Add(4 * time.Second)
	_ = os.Chtimes(path, future2, future2)
	pol = LoadRoutingPolicy()
	if pol.FallbackModel != "edited-fallback" {
		t.Fatalf("fault: malformed file dropped routing to %q", pol.FallbackModel)
	}
}
