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

	t.Run("small-model markers classify weak", func(t *testing.T) {
		for _, m := range []string{
			"gpt-4o-mini", // contains gpt-4o too — small marker must win
			"claude-3-5-haiku-20241022",
			"llama-3.1-8b-instruct",
			"gemini-1.5-flash",
			"phi-3-medium",
			"mistral-7b-instruct",
		} {
			if got := classifyTier(m, ""); got != TierWeak {
				t.Errorf("classifyTier(%q) = %s, want weak", m, got)
			}
		}
	})

	t.Run("frontier markers classify strong", func(t *testing.T) {
		for _, m := range []string{
			"claude-opus-4-20250514",
			"claude-3-5-sonnet-20241022",
			"gpt-4o",
			"gpt-4.1",
			"o3-mini-high", // "mini" — this is intentionally weak, see below
			"llama-3.1-70b",
			"deepseek-v3",
		} {
			got := classifyTier(m, "")
			// o3-mini-high contains "mini" and is deliberately weak: a "mini"
			// variant is the small one whatever the family.
			if strings.Contains(m, "mini") {
				if got != TierWeak {
					t.Errorf("classifyTier(%q) = %s, want weak (mini variant)", m, got)
				}
				continue
			}
			if got != TierStrong {
				t.Errorf("classifyTier(%q) = %s, want strong", m, got)
			}
		}
	})

	t.Run("unknown model degrades to medium", func(t *testing.T) {
		for _, m := range []string{"", "some-internal-model", "custom-tuned-v2"} {
			if got := classifyTier(m, ""); got != TierMedium {
				t.Errorf("classifyTier(%q) = %s, want medium", m, got)
			}
		}
	})
}

func TestWorkingDiscipline(t *testing.T) {
	// A strong model is trusted with the base prompt — no scaffolding.
	if got := (Capabilities{Tier: TierStrong}).WorkingDiscipline(); got != "" {
		t.Errorf("strong tier should add no discipline block, got %q", got)
	}
	// The zero value (no client) must also add nothing.
	if got := (Capabilities{}).WorkingDiscipline(); got != "" {
		t.Errorf("zero-value capabilities should add nothing, got %q", got)
	}

	weak := (Capabilities{Tier: TierWeak}).WorkingDiscipline()
	medium := (Capabilities{Tier: TierMedium}).WorkingDiscipline()

	// Both non-strong tiers steer the model toward the behaviors the deterministic
	// layers depend on: consult the skill, and typecheck.
	for name, block := range map[string]string{"weak": weak, "medium": medium} {
		if block == "" {
			t.Errorf("%s tier should add a discipline block", name)
		}
		for _, want := range []string{"read_skill", "run_typecheck"} {
			if !strings.Contains(block, want) {
				t.Errorf("%s discipline block should mention %q, got %q", name, want, block)
			}
		}
	}

	// The weak block is the fuller scaffold: it says more than the medium one.
	if len(weak) <= len(medium) {
		t.Errorf("weak discipline (%d chars) should be more explicit than medium (%d chars)", len(weak), len(medium))
	}
	// Read-before-write is the weak-only reinforcement.
	if !strings.Contains(weak, "read_file") {
		t.Errorf("weak discipline should reinforce read-before-write, got %q", weak)
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
