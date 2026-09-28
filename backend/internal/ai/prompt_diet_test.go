package ai

import "testing"

// FM-B2a (L21) — written before the diet (TEST_RULES §3). L5's budget:
// persona = SystemPromptBuild + platform allowlist + template digest ≤
// 4 500 tok ≈ 18 000 B at the ledger's own 4 B/tok estimate. Run-4's
// prompt-sec measured persona = 22 168 B and the compiled const = 15 469 B,
// so allowlist+digest = 6 699 B → the base must stay ≤ 11 301 B. (Budgets
// are measured on the COMPILED string — source length counts the
// backtick-concatenation scaffolding and overstates the const by ~800 B.)
// Red before the fix: 15 469 B — the design bar, navigation semantics,
// and device protocol sections all rode the always-on prefix while the
// skills that carry them (mobile-craft, expo-router-nav) sat behind
// read_skill, and no skill carried the device protocol at all (that half
// is knowledge's TestDeviceSessionSkillCarriesProtocol).
func TestSystemPromptBuildDietBudget(t *testing.T) {
	const baseBudget = 11301 // 18 000 (4500 tok × 4 B) − 6 699 (Run-4: persona 22 168 − const 15 469)

	if got := len(SystemPromptBuild); got > baseBudget {
		t.Errorf("SystemPromptBuild = %d bytes, budget %d — persona exceeds 4500 tok; "+
			"duplicated detail belongs behind read_skill (FM-B2a)", got, baseBudget)
	}
}
