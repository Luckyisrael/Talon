package ai

import (
	"encoding/json"
	"log"
	"os"
	"strings"
)

// TurnKind is what a turn is trying to do. Routing happens per turn — never
// per API call — because switching models mid-turn risks incoherence: one
// model plans in an idiom another would not have chosen.
type TurnKind string

const (
	TurnPlan      TurnKind = "plan"
	TurnBuild     TurnKind = "build"
	TurnSmallEdit TurnKind = "small_edit"
	TurnHeal      TurnKind = "heal"
)

// Policy maps turn kinds to tiers and tiers to models. It loads from
// ROUTING_POLICY_JSON, falling back to compiled defaults plus AI_MODEL /
// AI_MODEL_CHEAP — so reassigning which model serves a tier is a
// configuration change, never a code change. Tier constants live in
// capabilities.go; the override there still wins for display.
type Policy struct {
	PlanTier      Tier            `json:"plan_tier"`
	BuildTier     Tier            `json:"build_tier"`
	SmallEditTier Tier            `json:"small_edit_tier"`
	HealTier      Tier            `json:"heal_tier"`
	Models        map[Tier]string `json:"models"`
	// SmallEditMaxChars bounds the cheap path: a short follow-up prompt that
	// is not a heal retry. Longer prompts need full context and get it.
	SmallEditMaxChars int `json:"small_edit_max_chars"`
	// FallbackModel is tried once when the routed model errors. Same endpoint,
	// different model — cross-provider failover without a second provider is
	// untestable dead code, so the fallback stays on the configured endpoint.
	FallbackModel string `json:"fallback_model"`
}

func DefaultPolicy() Policy {
	heavy := strings.TrimSpace(os.Getenv("AI_MODEL"))
	cheap := strings.TrimSpace(os.Getenv("AI_MODEL_CHEAP"))
	if cheap == "" {
		cheap = heavy
	}
	return Policy{
		// Plans decide everything downstream: a bad plan wastes ten times the
		// tokens it saves. Never cheap out here.
		PlanTier: TierStrong,
		// Fresh builds need full reasoning.
		BuildTier: TierStrong,
		// Short follow-ups usually restate nearby context.
		SmallEditTier: TierMedium,
		// A failing build is no place to gamble: escalate, don't economise.
		HealTier:          TierStrong,
		Models:            map[Tier]string{TierStrong: heavy, TierMedium: cheap, TierWeak: cheap},
		SmallEditMaxChars: 200,
		FallbackModel:     heavy,
	}
}

// LoadRoutingPolicy returns the configured policy: ROUTING_POLICY_JSON merged
// over the defaults, so a partial JSON overrides only what it names.
func LoadRoutingPolicy() Policy {
	pol := DefaultPolicy()
	raw := strings.TrimSpace(os.Getenv("ROUTING_POLICY_JSON"))
	if raw == "" {
		return pol
	}
	var over Policy
	if err := json.Unmarshal([]byte(raw), &over); err != nil {
		log.Printf("ai: ROUTING_POLICY_JSON invalid, using defaults: %v", err)
		return pol
	}
	if over.PlanTier != "" {
		pol.PlanTier = over.PlanTier
	}
	if over.BuildTier != "" {
		pol.BuildTier = over.BuildTier
	}
	if over.SmallEditTier != "" {
		pol.SmallEditTier = over.SmallEditTier
	}
	if over.HealTier != "" {
		pol.HealTier = over.HealTier
	}
	for tier, model := range over.Models {
		if strings.TrimSpace(model) != "" {
			pol.Models[tier] = model
		}
	}
	if over.SmallEditMaxChars > 0 {
		pol.SmallEditMaxChars = over.SmallEditMaxChars
	}
	if strings.TrimSpace(over.FallbackModel) != "" {
		pol.FallbackModel = over.FallbackModel
	}
	return pol
}

// ClassifyTurn decides what kind of turn this is from inspectable inputs —
// mode, prompt length, and whether the loop is already in repair. The reason
// string is logged with the routing decision so the mapping stays tunable
// from real data instead of guesswork.
func ClassifyTurn(mode string, promptLen, verifyAttempts int, pol Policy) (TurnKind, Tier, string) {
	if strings.ToLower(strings.TrimSpace(mode)) == "plan" {
		return TurnPlan, pol.PlanTier, "plan mode"
	}
	if verifyAttempts > 0 {
		return TurnHeal, pol.HealTier, "in repair after failed verification"
	}
	if promptLen > 0 && promptLen <= pol.SmallEditMaxChars {
		return TurnSmallEdit, pol.SmallEditTier, "short follow-up prompt"
	}
	return TurnBuild, pol.BuildTier, "full build"
}

// ModelFor resolves the model string for a tier. Empty means unconfigured —
// callers fall back to the client's own model rather than failing.
func (p Policy) ModelFor(tier Tier) string {
	if m := strings.TrimSpace(p.Models[tier]); m != "" {
		return m
	}
	return strings.TrimSpace(os.Getenv("AI_MODEL"))
}

// Model rates in USD per million tokens: input, output, cache-read. Only
// entries with real published pricing belong here — an unknown model costs 0
// and logs once, because an invented price is worse than an honest zero.
// Extra entries arrive via ROUTING_PRICING_JSON: {"model": [in, out, cached]}.
type ModelRates struct {
	Input, Output, Cached float64
}

func DefaultPricing() map[string]ModelRates {
	out := map[string]ModelRates{
		// Free-tier routing target: genuinely $0.
		"minimax/minimax-m3:free": {0, 0, 0},
	}
	raw := strings.TrimSpace(os.Getenv("ROUTING_PRICING_JSON"))
	if raw == "" {
		return out
	}
	var over map[string][3]float64
	if err := json.Unmarshal([]byte(raw), &over); err != nil {
		log.Printf("ai: ROUTING_PRICING_JSON invalid, using defaults: %v", err)
		return out
	}
	for model, r := range over {
		out[model] = ModelRates{Input: r[0], Output: r[1], Cached: r[2]}
	}
	return out
}

// CostUSD prices one turn's token usage for the model that served it. Unknown
// models cost 0 — the caller logs the miss once per process so a missing
// price is visible instead of silently free.
func CostUSD(model string, in, out, cached int, pricing map[string]ModelRates) float64 {
	rates, ok := pricing[model]
	if !ok {
		logUnknownPrice(model)
		return 0
	}
	return (float64(in)*rates.Input + float64(out)*rates.Output + float64(cached)*rates.Cached) / 1e6
}

var loggedUnknownPrices = map[string]bool{}

func logUnknownPrice(model string) {
	if loggedUnknownPrices[model] {
		return
	}
	loggedUnknownPrices[model] = true
	log.Printf("ai: no pricing for model %q — turn cost recorded as 0; add ROUTING_PRICING_JSON to price it", model)
}
