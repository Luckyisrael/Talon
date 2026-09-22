package embed

import "testing"

// TestFromEnvUnsetIsNil is the dormancy contract: no URL, no provider, no
// vector layer — every nil caller (sweeps, search fusion) skips by design,
// and the deployment stays byte-identical to today's.
func TestFromEnvUnsetIsNil(t *testing.T) {
	t.Setenv(EnvURL, "")
	t.Setenv(EnvKey, "")
	t.Setenv(EnvModel, "")
	if FromEnv() != nil {
		t.Fatal("unconfigured embed env must yield nil, not a provider")
	}
}

// TestFromEnvDefaults: URL is the only required variable; surrounding
// whitespace trims (pasted env values carry it), the model defaults, and
// Name() reflects the model for timeline/debug display.
func TestFromEnvDefaults(t *testing.T) {
	t.Setenv(EnvURL, "  https://api.example.com/v1/embeddings  ")
	t.Setenv(EnvKey, "  sk-abc  ")
	t.Setenv(EnvModel, "")
	p := FromEnv()
	if p == nil {
		t.Fatal("URL alone must configure the default provider")
	}
	api, ok := p.(*API)
	if !ok {
		t.Fatalf("FromEnv returned %T, want *API", p)
	}
	if api.Endpoint != "https://api.example.com/v1/embeddings" {
		t.Errorf("Endpoint = %q (whitespace not trimmed)", api.Endpoint)
	}
	if api.APIKey != "sk-abc" {
		t.Errorf("APIKey = %q (whitespace not trimmed)", api.APIKey)
	}
	if api.Model != DefaultModel {
		t.Errorf("Model = %q, want default %q", api.Model, DefaultModel)
	}
	if want := "api:" + DefaultModel; p.Name() != want {
		t.Errorf("Name() = %q, want %q", p.Name(), want)
	}
}

// TestFromEnvModelOverride: a local or alternate endpoint picks its model.
func TestFromEnvModelOverride(t *testing.T) {
	t.Setenv(EnvURL, "http://localhost:9999/embed")
	t.Setenv(EnvModel, "bge-m3")
	p := FromEnv()
	if p == nil {
		t.Fatal("expected a provider")
	}
	if p.Name() != "api:bge-m3" {
		t.Errorf("Name() = %q, want api:bge-m3", p.Name())
	}
}
