package ai

import (
	"testing"
)

func testPolicy() Policy {
	return Policy{
		PlanTier:          TierStrong,
		BuildTier:         TierStrong,
		SmallEditTier:     TierMedium,
		HealTier:          TierStrong,
		Models:            map[Tier]string{TierStrong: "heavy", TierMedium: "cheap", TierWeak: "cheap"},
		SmallEditMaxChars: 200,
		FallbackModel:     "heavy",
	}
}

func TestClassifyTurn(t *testing.T) {
	pol := testPolicy()
	cases := []struct {
		mode, reason string
		promptLen    int
		heals        int
		kind         TurnKind
		tier         Tier
	}{
		{"plan", "", 1200, 0, TurnPlan, TierStrong},
		{"build", "", 5000, 0, TurnBuild, TierStrong},
		{"build", "", 80, 0, TurnSmallEdit, TierMedium},
		{"build", "", 80, 2, TurnHeal, TierStrong},
		{"build", "", 5000, 1, TurnHeal, TierStrong},
		{"build", "", 200, 0, TurnSmallEdit, TierMedium},
		{"build", "", 201, 0, TurnBuild, TierStrong},
		{"", "", 50, 0, TurnSmallEdit, TierMedium},
	}
	for _, c := range cases {
		kind, tier, reason := ClassifyTurn(c.mode, c.promptLen, c.heals, pol)
		if kind != c.kind || tier != c.tier || reason == "" {
			t.Errorf("ClassifyTurn(%q,%d,%d) = (%v,%v,%q), want (%v,%v,non-empty)",
				c.mode, c.promptLen, c.heals, kind, tier, reason, c.kind, c.tier)
		}
		if got := pol.ModelFor(tier); got == "" {
			t.Errorf("ModelFor(%v) empty", tier)
		}
	}
}

func TestPolicyMerge(t *testing.T) {
	t.Setenv("AI_MODEL", "heavy-default")
	t.Setenv("AI_MODEL_CHEAP", "cheap-default")
	t.Setenv("ROUTING_POLICY_JSON", `{"small_edit_tier":"weak","models":{"weak":"cheapest"},"small_edit_max_chars":100}`)
	pol := LoadRoutingPolicy()
	if pol.SmallEditTier != TierWeak {
		t.Errorf("override not applied: %v", pol.SmallEditTier)
	}
	if pol.BuildTier != TierStrong {
		t.Errorf("default lost: %v", pol.BuildTier)
	}
	if pol.Models[TierWeak] != "cheapest" {
		t.Errorf("model override not applied: %v", pol.Models)
	}
	if pol.Models[TierStrong] != "heavy-default" {
		t.Errorf("env default lost: %v", pol.Models)
	}
	if pol.SmallEditMaxChars != 100 {
		t.Errorf("max chars not applied: %d", pol.SmallEditMaxChars)
	}
}

func TestPolicyInvalidJSONFallsBack(t *testing.T) {
	t.Setenv("ROUTING_POLICY_JSON", "{broken")
	pol := LoadRoutingPolicy()
	if pol.BuildTier != TierStrong || pol.SmallEditMaxChars != 200 {
		t.Errorf("invalid JSON should yield defaults: %+v", pol)
	}
}

func TestCostUSD(t *testing.T) {
	pricing := map[string]ModelRates{
		"m": {Input: 3, Output: 15, Cached: 0.3},
	}
	// 1M in + 1M out + 1M cached = 3 + 15 + 0.3
	if got := CostUSD("m", 1000000, 1000000, 1000000, pricing); got != 18.3 {
		t.Errorf("got %v, want 18.3", got)
	}
	if got := CostUSD("unknown-model", 1000000, 1000000, 1000000, pricing); got != 0 {
		t.Errorf("unknown model should cost 0, got %v", got)
	}
	if got := CostUSD("minimax/minimax-m3:free", 999999999, 999999999, 999999999, DefaultPricing()); got != 0 {
		t.Errorf("free tier should cost 0, got %v", got)
	}
}

func TestWithModel(t *testing.T) {
	c := &Client{protocol: ProtocolOpenAI, baseURL: "https://x", apiKey: "k", model: "heavy"}
	sub := c.WithModel("cheap")
	if sub.Model() != "cheap" {
		t.Errorf("sub model = %q", sub.Model())
	}
	if c.Model() != "heavy" {
		t.Errorf("original mutated to %q", c.Model())
	}
}
