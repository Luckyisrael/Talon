package ai

import (
	"strings"
	"testing"
)

func TestClassifyTier(t *testing.T) {
	// An explicit override wins over anything the id would imply.
	t.Run("override wins", func(t *testing.T) {
		cases := map[string]Tier{
			"strong": TierStrong,
			"weak":   TierWeak,
			"medium": TierMedium,
			" WEAK ": TierWeak, // case-insensitive and trimmed
		}
		for override, want := range cases {
			// A frontier model id, so only the override can produce the result.
			if got := classifyTier("claude-opus-4", override); got != want {
				t.Errorf("override %q: got %s, want %s", override, got, want)
			}
		}
	})

	t.Run("without override always medium — no model-name heuristics", func(t *testing.T) {
		for _, m := range []string{
			"gpt-4o-mini",
			"claude-3-5-haiku-20241022",
			"claude-opus-4-20250514",
			"gpt-4o",
			"llama-3.1-70b",
			"deepseek-v3",
			"", "some-internal-model", "custom-tuned-v2",
			"nex-agi/nex-n2.5-pro:free",
		} {
			if got := classifyTier(m, ""); got != TierMedium {
				t.Errorf("classifyTier(%q, \"\") = %s, want medium (harness owns tier)", m, got)
			}
		}
	})
}

func TestWorkingDiscipline(t *testing.T) {
	weak := (Capabilities{Tier: TierWeak}).WorkingDiscipline()
	medium := (Capabilities{Tier: TierMedium}).WorkingDiscipline()
	strong := (Capabilities{Tier: TierStrong}).WorkingDiscipline()

	// Every tier now gets a block, strong included: even a capable model cannot
	// know from training that this stack is newer than that training. The block is
	// terse, but it is there.
	for name, block := range map[string]string{"weak": weak, "medium": medium, "strong": strong} {
		if block == "" {
			t.Errorf("%s tier should add a discipline block", name)
		}
		// All three steer toward the behaviors the deterministic layers depend on:
		// consult the skill, and typecheck.
		for _, want := range []string{"read_skill", "run_typecheck"} {
			if !strings.Contains(block, want) {
				t.Errorf("%s discipline block should mention %q, got %q", name, want, block)
			}
		}
	}

	// The zero value (no explicit tier) degrades to the cautious middle block, the
	// same way an unknown model id and a nil client both resolve to medium — never
	// to nothing, and never to the terse strong block.
	if got := (Capabilities{}).WorkingDiscipline(); got != medium {
		t.Errorf("zero-value capabilities should fall back to the medium block, got %q", got)
	}

	// The weak block is the fuller scaffold: it says more than either lighter block.
	if len(weak) <= len(medium) {
		t.Errorf("weak discipline (%d chars) should be more explicit than medium (%d chars)", len(weak), len(medium))
	}
	if len(weak) <= len(strong) {
		t.Errorf("weak discipline (%d chars) should be more explicit than strong (%d chars)", len(weak), len(strong))
	}
	// Every tier keeps the same core workflow — the strong block just states it more
	// tersely, it does not drop it. Read-before-edit, planning and small steps are
	// part of that workflow, so the strong block must carry them too: the
	// deterministic layers depend on the model reading a file before it edits it
	// whatever tier drives the build.
	for _, want := range []string{"read_file", "Plan", "small"} {
		if !strings.Contains(strong, want) {
			t.Errorf("strong discipline should keep the core workflow and mention %q, got %q", want, strong)
		}
	}
}

// Capabilities on a nil client must not panic and reports the neutral tier — some
// tests construct orchestration without a live client.
func TestNilClientCapabilities(t *testing.T) {
	var c *Client
	if got := c.Capabilities(); got.Tier != TierMedium {
		t.Errorf("nil client should report medium tier, got %s", got.Tier)
	}
}
