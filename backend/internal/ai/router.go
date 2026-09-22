package ai

// W7 — the router: one watched settings document (routing, pricing, tier
// display, providers), a provider health registry with a circuit breaker,
// and the clients that fail over across providers. Everything here stays
// inert until TALONSTUDIO_ROUTER is truthy: with the flag off there is
// exactly one provider and today's single-endpoint behavior is untouched.

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// RouterEnabled reports whether the W7 multi-provider router is on. The
// truthy set matches the other feature flags: 1, true, on, v1.
func RouterEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_ROUTER"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// DefaultProviderName is what usage/record and the breaker call the client
// built from the environment (or from a "primary" entry in providers).
const DefaultProviderName = "primary"

// ProviderSpec is one entry of the settings document's providers array: an
// alternate endpoint the router can pin to and fail over to. Model is
// optional — an empty Model means "keep the model being asked", for
// gateways that proxy the same ids. Protocol empty means "keep the base
// client's protocol".
type ProviderSpec struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // anthropic | openai
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`
}

// settingsExtra is the non-routing half of the watched settings document.
// It rides the same file and the same mtime/last-good discipline as
// routing (W7.4 — one document, one reload path, no second config file).
type settingsExtra struct {
	Pricing   map[string][3]float64 `json:"pricing"`
	Providers []ProviderSpec        `json:"providers"`
	ModelTier string                `json:"model_tier"`
}

var (
	extraMu    sync.Mutex
	extraPath  string
	extraStamp time.Time
	extraDoc   settingsExtra
	extraOK    bool
)

// loadSettingsExtra reads the providers/pricing/model_tier sections of the
// one watched settings document. Same discipline as LoadRoutingPolicy: an
// unreadable or invalid document holds the last good extras rather than
// dropping configured providers mid-turn. Unknown fields (routing, legacy
// flat policies) are ignored by the decoder — one file, two decoders,
// neither disturbed by the other.
func loadSettingsExtra() settingsExtra {
	settingsMu.RLock()
	path := settingsFile
	settingsMu.RUnlock()
	if path == "" {
		return settingsExtra{}
	}
	fi, err := os.Stat(path)
	if err != nil {
		extraMu.Lock()
		defer extraMu.Unlock()
		if extraOK && extraPath == path {
			return extraDoc
		}
		return settingsExtra{}
	}
	extraMu.Lock()
	defer extraMu.Unlock()
	if extraOK && extraPath == path && !fi.ModTime().After(extraStamp) {
		return extraDoc
	}
	raw, rerr := os.ReadFile(path)
	var doc settingsExtra
	if rerr != nil || json.Unmarshal(raw, &doc) != nil {
		log.Printf("ai: settings document unreadable for providers/pricing; holding the last good extras: %v", rerr)
		if extraOK && extraPath == path {
			return extraDoc
		}
		return settingsExtra{}
	}
	extraPath, extraStamp, extraDoc, extraOK = path, fi.ModTime(), doc, true
	return doc
}

// LoadPricing prices tokens: the environment seed (ROUTING_PRICING_JSON)
// overlaid with the settings document's pricing section, which wins — the
// document is the operator's most recent word. {"model": [in, out, cached]}
// per million tokens, same shape as the env var. With the router off the
// document is never consulted: byte-identical to DefaultPricing.
func LoadPricing() map[string]ModelRates {
	pricing := DefaultPricing()
	if !RouterEnabled() {
		return pricing
	}
	for model, r := range loadSettingsExtra().Pricing {
		pricing[model] = ModelRates{Input: r[0], Output: r[1], Cached: r[2]}
	}
	return pricing
}

// TierOverride is the tier display value for Capabilities: the settings
// document's model_tier wins over AI_MODEL_TIER when the router is on
// (W7.4 — tier display lives in the one watched document). Flag off: the
// env value, exactly as before.
func TierOverride() string {
	if RouterEnabled() {
		if t := strings.TrimSpace(loadSettingsExtra().ModelTier); t != "" {
			return t
		}
	}
	return os.Getenv("AI_MODEL_TIER")
}

// LoadProviderSpecs returns the providers block, flag-gated: with the
// router off no specs are read and no second provider exists. Unnamed or
// endpoint-less entries are dropped — they could never be pinned or
// logged honestly.
func LoadProviderSpecs() []ProviderSpec {
	if !RouterEnabled() {
		return nil
	}
	specs := loadSettingsExtra().Providers
	out := make([]ProviderSpec, 0, len(specs))
	for _, s := range specs {
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.BaseURL) == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// LookupProvider finds one spec by name (case-sensitive: names are pin
// values in usage/record and breaker keys, not display labels).
func LookupProvider(name string) (ProviderSpec, bool) {
	for _, s := range LoadProviderSpecs() {
		if s.Name == name {
			return s, true
		}
	}
	return ProviderSpec{}, false
}

// ClientForProvider builds a client that serves spec on top of base:
// new endpoint/credentials/protocol, keeping base's http client, pacer,
// retry hook and (via clone) a consistent health snapshot. Unset spec
// pieces fall back to base so a partial spec cannot produce a client that
// cannot send. The borrowed lastErr is cleared — a failure at one endpoint
// is not evidence about another. Returns nil when base is nil or the spec
// has no endpoint.
func ClientForProvider(spec ProviderSpec, base *Client) *Client {
	endpoint := strings.TrimSpace(spec.BaseURL)
	if base == nil || endpoint == "" {
		return nil
	}
	c := base.clone()
	out := &c
	out.baseURL = strings.TrimSuffix(endpoint, "/")
	if k := strings.TrimSpace(spec.APIKey); k != "" {
		out.apiKey = k
	}
	if m := strings.TrimSpace(spec.Model); m != "" {
		out.model = m
	}
	if p := strings.TrimSpace(spec.Protocol); p != "" {
		out.protocol = Provider(p)
	}
	out.providerName = strings.TrimSpace(spec.Name)
	out.lastErr, out.lastErrAt = "", time.Time{}
	return out
}

// ForVision routes a vision call to policy.VisionModel when the router is
// on and vision_model is set. Flag off, or unset: the client is returned
// untouched and vision rides the chat model exactly as today.
func ForVision(c *Client) *Client {
	if c == nil || !RouterEnabled() {
		return c
	}
	if vm := strings.TrimSpace(LoadRoutingPolicy().VisionModel); vm != "" && vm != c.model {
		return c.WithModel(vm)
	}
	return c
}

// breakerState is the classic three-state circuit.
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// breakerThreshold/cooldown are vars so tests can walk the state machine
// without wall-clock waits. Threshold 1: by the time a failure escapes
// ChatWithTools the client already retried the request internally
// (connection errors and 429/50x get up to three attempts), so one
// observed round-level failure means the endpoint really is down.
var (
	breakerThreshold = 1
	breakerCooldown  = 45 * time.Second
)

type providerHealth struct {
	mu       sync.Mutex
	fails    int
	state    breakerState
	openedAt time.Time
}

var (
	healthMu    sync.Mutex
	healthNames = map[string]*providerHealth{}
)

func healthFor(name string) *providerHealth {
	healthMu.Lock()
	defer healthMu.Unlock()
	h := healthNames[name]
	if h == nil {
		h = &providerHealth{}
		healthNames[name] = h
	}
	return h
}

// NoteProviderFailure records a round-level failure against a provider:
// closed → open once the threshold trips, half-open probe → reopen with a
// fresh cooldown. Fed only from the orchestrator's loop when the router is
// on — vision failures and flag-off turns never touch the registry.
func NoteProviderFailure(name string) {
	if name == "" {
		return
	}
	h := healthFor(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fails++
	if h.state == breakerHalfOpen {
		h.state = breakerOpen
		h.openedAt = time.Now()
		return
	}
	if h.state == breakerClosed && h.fails >= breakerThreshold {
		h.state = breakerOpen
		h.openedAt = time.Now()
	}
}

// NoteProviderSuccess closes the circuit and clears the failure count —
// success is the only proof a breaker accepts, from either state.
func NoteProviderSuccess(name string) {
	if name == "" {
		return
	}
	h := healthFor(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fails = 0
	h.state = breakerClosed
}

// AllowProvider reports whether a request may go to this provider now.
// Open circuits past their cooldown flip to half-open and admit exactly
// one probe; the probe's outcome (NoteProvider*) decides the next state.
// Half-open admits nothing else until that probe reports.
func AllowProvider(name string) bool {
	if name == "" {
		return false
	}
	h := healthFor(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	switch h.state {
	case breakerClosed:
		return true
	case breakerOpen:
		if time.Since(h.openedAt) >= breakerCooldown {
			h.state = breakerHalfOpen
			return true
		}
		return false
	default: // breakerHalfOpen: a probe is already in flight
		return false
	}
}

// SelectStartProvider picks the provider a round should start on: the
// current one while its circuit allows (this call is itself the half-open
// probe when the cooldown has passed), otherwise the named fallback if
// that spec exists and its circuit allows. When nothing is provably
// healthy it returns the current provider anyway — the request and the
// error listener stay the honest last resort, and refusing to send at all
// would hide the outage behind a stall. Callers compare against the
// current name to detect a switch.
func SelectStartProvider(current, fallback string) string {
	if current == "" {
		current = DefaultProviderName
	}
	if AllowProvider(current) {
		return current
	}
	if fallback != "" && fallback != current {
		if _, ok := LookupProvider(fallback); ok && AllowProvider(fallback) {
			return fallback
		}
	}
	return current
}
