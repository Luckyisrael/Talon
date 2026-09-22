package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// attachDoc writes a settings document and attaches it, cleaning up after
// the test so a leaked path cannot route a later test through this file.
func attachDoc(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal settings doc: %v", err)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write settings doc: %v", err)
	}
	AttachSettingsFile(path)
	t.Cleanup(func() { AttachSettingsFile("") })
	return path
}

// The W7 flag joins the existing truthy set: 1, true, on, v1 — and nothing
// that merely looks enthusiastic.
func TestRouterEnabledTruthSet(t *testing.T) {
	for _, on := range []string{"1", "true", "TRUE", "on", "v1", "V1", " true "} {
		t.Setenv("TALONSTUDIO_ROUTER", on)
		if !RouterEnabled() {
			t.Errorf("RouterEnabled(%q) = false, want true", on)
		}
	}
	for _, off := range []string{"", "0", "no", "false", "v2", "enabled"} {
		t.Setenv("TALONSTUDIO_ROUTER", off)
		if RouterEnabled() {
			t.Errorf("RouterEnabled(%q) = true, want false", off)
		}
	}
}

// The breaker walks closed → open → half-open probe → closed, refuses a
// second caller while a probe is in flight, and reopens when the probe
// fails. Threshold and cooldown are test-overridable vars on purpose —
// the state machine is the contract, not the wall clock.
func TestBreakerStateMachine(t *testing.T) {
	oldTh, oldCd := breakerThreshold, breakerCooldown
	t.Cleanup(func() { breakerThreshold, breakerCooldown = oldTh, oldCd })
	breakerThreshold, breakerCooldown = 2, 50*time.Millisecond

	const name = "unit-breaker-provider"
	if !AllowProvider(name) {
		t.Fatal("a fresh provider must be allowed")
	}
	NoteProviderFailure(name)
	if !AllowProvider(name) {
		t.Error("below the threshold the circuit must stay closed")
	}
	NoteProviderFailure(name) // trips at threshold 2
	if AllowProvider(name) {
		t.Error("an open circuit must refuse")
	}
	time.Sleep(60 * time.Millisecond)
	if !AllowProvider(name) {
		t.Error("cooldown must admit exactly one probe")
	}
	if AllowProvider(name) {
		t.Error("half-open must refuse everything until the probe reports")
	}
	NoteProviderFailure(name) // probe fails → reopen
	if AllowProvider(name) {
		t.Error("a failed probe must reopen the circuit")
	}
	time.Sleep(60 * time.Millisecond)
	if !AllowProvider(name) {
		t.Error("the next cooldown must admit a fresh probe")
	}
	NoteProviderSuccess(name)
	if !AllowProvider(name) {
		t.Error("a successful probe must close the circuit")
	}
	if !AllowProvider(name) {
		t.Error("a closed circuit stays allowed")
	}
}

// SelectStartProvider keeps the current provider while healthy, moves to
// the configured fallback while the current circuit is open, and never
// strands the round when nothing is provably healthy.
func TestSelectStartProviderSwitchesOnlyWhenOpen(t *testing.T) {
	oldTh, oldCd := breakerThreshold, breakerCooldown
	t.Cleanup(func() { breakerThreshold, breakerCooldown = oldTh, oldCd })
	breakerThreshold, breakerCooldown = 1, 45*time.Second

	attachDoc(t, map[string]any{
		"providers": []map[string]string{{
			"name": "alt", "base_url": "http://alt.example", "api_key": "k",
		}},
	})
	t.Setenv("TALONSTUDIO_ROUTER", "1")

	if got := SelectStartProvider(DefaultProviderName, "alt"); got != DefaultProviderName {
		t.Errorf("healthy primary must be chosen, got %q", got)
	}
	NoteProviderFailure(DefaultProviderName)
	if got := SelectStartProvider(DefaultProviderName, "alt"); got != "alt" {
		t.Errorf("open primary must switch to the fallback provider, got %q", got)
	}
	// Fallback missing or itself unhealthy: the round still goes out on the
	// current provider — refusing to send would hide the outage behind a stall.
	if got := SelectStartProvider(DefaultProviderName, "no-such-provider"); got != DefaultProviderName {
		t.Errorf("no usable fallback must keep the current provider, got %q", got)
	}
	NoteProviderSuccess(DefaultProviderName)
}

// The providers block exists only under the router flag, and a partial
// spec falls back to the base client's credentials instead of producing a
// client that cannot send. Borrowed health never crosses providers.
func TestProvidersAndClientForProvider(t *testing.T) {
	attachDoc(t, map[string]any{
		"routing": map[string]any{"fallback_provider": "env-fb"},
		"providers": []map[string]string{{
			"name": "secondary", "base_url": "http://secondary.example/v2",
			"api_key": "sec-key", "model": "sec-model", "protocol": "openai",
		}},
	})
	t.Setenv("ROUTING_POLICY_JSON", `{"fallback_provider":"env-fb"}`)

	t.Setenv("TALONSTUDIO_ROUTER", "0")
	if specs := LoadProviderSpecs(); len(specs) != 0 {
		t.Errorf("flag off must read no specs, got %v", specs)
	}
	if _, ok := LookupProvider("secondary"); ok {
		t.Error("flag off must not resolve providers")
	}
	// The extras-only routing path: an extras document with env routing
	// still serves env routing — the four-section doc needs no routing
	// section to be valid.
	if pol := LoadRoutingPolicy(); pol.FallbackProvider != "env-fb" {
		t.Errorf("env routing must survive an extras-carrying doc, got %q", pol.FallbackProvider)
	}

	t.Setenv("TALONSTUDIO_ROUTER", "1")
	specs := LoadProviderSpecs()
	if len(specs) != 1 || specs[0].Name != "secondary" {
		t.Fatalf("expected the secondary spec, got %+v", specs)
	}
	spec, ok := LookupProvider("secondary")
	if !ok {
		t.Fatal("LookupProvider missed the configured spec")
	}
	if pol := LoadRoutingPolicy(); pol.FallbackProvider != "env-fb" {
		t.Errorf("flag on: env routing must survive an extras doc, got %q", pol.FallbackProvider)
	}

	base := &Client{
		protocol:     ProtocolAnthropic,
		baseURL:      "http://primary.example",
		apiKey:       "primary-key",
		model:        "chat-model",
		providerName: DefaultProviderName,
		lastErr:      "fresh failure at the old endpoint",
		lastErrAt:    time.Now(),
	}
	c := ClientForProvider(spec, base)
	if c == nil {
		t.Fatal("expected a client, got nil")
	}
	if c.baseURL != "http://secondary.example/v2" || c.apiKey != "sec-key" ||
		c.model != "sec-model" || c.protocol != ProtocolOpenAI || c.providerName != "secondary" {
		t.Errorf("provider client misconfigured: %+v", c)
	}
	if c.lastErr != "" || !c.lastErrAt.IsZero() {
		t.Errorf("borrowed health must not cross providers: %q", c.lastErr)
	}
	// Partial spec: keep the base's key, model, protocol — only move the
	// endpoint, and pin the name honestly.
	partial := ProviderSpec{Name: "p2", BaseURL: "http://p2.example"}
	c2 := ClientForProvider(partial, base)
	if c2 == nil {
		t.Fatal("expected a partial-spec client, got nil")
	}
	if c2.apiKey != "primary-key" || c2.model != "chat-model" ||
		c2.protocol != ProtocolAnthropic || c2.providerName != "p2" {
		t.Errorf("partial spec must inherit the base: %+v", c2)
	}
	if ClientForProvider(ProviderSpec{Name: "no-endpoint"}, base) != nil {
		t.Error("a spec without an endpoint must not produce a client")
	}
	if ClientForProvider(spec, nil) != nil {
		t.Error("a nil base must not produce a client")
	}
}

// Pricing rides the one watched document on top of the env seed, doc
// wins — and only when the router is on.
func TestLoadPricingDocOverlay(t *testing.T) {
	attachDoc(t, map[string]any{
		"pricing": map[string][3]float64{
			"doc-model": {9, 8, 7},
			"env-model": {4, 4, 4},
		},
	})
	t.Setenv("ROUTING_PRICING_JSON", `{"env-model":[1,2,0]}`)

	t.Setenv("TALONSTUDIO_ROUTER", "0")
	p := LoadPricing()
	if _, ok := p["doc-model"]; ok {
		t.Error("flag off must never read document pricing")
	}
	if p["env-model"] != (ModelRates{Input: 1, Output: 2}) {
		t.Errorf("flag off must be the env seed, got %+v", p["env-model"])
	}

	t.Setenv("TALONSTUDIO_ROUTER", "1")
	p = LoadPricing()
	if p["doc-model"] != (ModelRates{Input: 9, Output: 8, Cached: 7}) {
		t.Errorf("document pricing must land, got %+v", p["doc-model"])
	}
	if p["env-model"] != (ModelRates{Input: 4, Output: 4, Cached: 4}) {
		t.Errorf("the document is the operator's most recent word, got %+v", p["env-model"])
	}
}

// Tier display: model_tier in the document wins with the router on; the
// env value is the whole answer with it off.
func TestTierOverridePrefersDocumentOnlyWhenRouted(t *testing.T) {
	attachDoc(t, map[string]any{"model_tier": "strong"})
	t.Setenv("AI_MODEL_TIER", "weak")

	t.Setenv("TALONSTUDIO_ROUTER", "0")
	if got := TierOverride(); got != "weak" {
		t.Errorf("flag off must be the env value, got %q", got)
	}
	t.Setenv("TALONSTUDIO_ROUTER", "1")
	if got := TierOverride(); got != "strong" {
		t.Errorf("flag on must prefer the document, got %q", got)
	}
}

// ForVision hands the chat client back untouched off-flag or when no
// vision_model is set; with the router on and one configured, it clones a
// client pointed at that model.
func TestForVision(t *testing.T) {
	base := &Client{model: "chat-model", providerName: DefaultProviderName}
	t.Setenv("ROUTING_POLICY_JSON", `{"vision_model":"vision-model"}`)

	t.Setenv("TALONSTUDIO_ROUTER", "0")
	if got := ForVision(base); got != base {
		t.Errorf("flag off must return the identical client, got model %q", got.model)
	}

	t.Setenv("TALONSTUDIO_ROUTER", "1")
	got := ForVision(base)
	if got == base {
		t.Fatal("vision_model configured: expected a cloned client")
	}
	if got.model != "vision-model" {
		t.Errorf("expected vision-model, got %q", got.model)
	}
	if base.model != "chat-model" {
		t.Errorf("the base client must be untouched, got %q", base.model)
	}

	t.Setenv("ROUTING_POLICY_JSON", "")
	if got := ForVision(base); got != base {
		t.Errorf("no vision_model: expect the chat client back, got model %q", got.model)
	}
}

// The three W7 routing fields merge over a partial policy and never clear
// what a partial document does not name.
func TestApplyOverrideMergesRouterFields(t *testing.T) {
	base := DefaultPolicy()
	got := applyOverride(base, Policy{
		VisionModel:      "vision-model",
		FallbackProvider: "secondary",
		MaxTurnCostUSD:   0.25,
	})
	if got.VisionModel != "vision-model" || got.FallbackProvider != "secondary" || got.MaxTurnCostUSD != 0.25 {
		t.Errorf("router fields did not merge: %+v", got)
	}
	if got.BuildTier != base.BuildTier || got.SmallEditMaxChars != base.SmallEditMaxChars {
		t.Errorf("untouched fields must be preserved: %+v", got)
	}
	kept := applyOverride(got, Policy{})
	if kept.VisionModel != "vision-model" || kept.FallbackProvider != "secondary" || kept.MaxTurnCostUSD != 0.25 {
		t.Errorf("an empty override must not clear router fields: %+v", kept)
	}
	if policyNonEmpty(Policy{MaxTurnCostUSD: 0.5}) != true {
		t.Error("a cost cap alone is a non-empty policy")
	}
	if policyNonEmpty(Policy{}) != false {
		t.Error("the zero policy is empty")
	}
}
