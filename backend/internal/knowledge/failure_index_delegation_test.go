package knowledge

import (
	"strings"
	"testing"
)

// W12: every structured DELEGATION_* refusal resolves to its known fix —
// and the cap/cooldown family must beat the generic throttle pattern, whose
// "back off and retry" advice is wrong mid-cooldown and against a spend
// cap (Lookup is first-match, so order is the assertion here too).
func TestLookupDelegationRefusals(t *testing.T) {
	idx := DefaultFailureIndex()
	cases := []struct {
		err, wantFix string
	}{
		{"DELEGATION_POOL: too many children are already running; wait for one to finish before delegating again",
			"poll list_children and keep working elsewhere; delegate again when a slot or budget frees"},
		{"DELEGATION_COOLDOWN: the provider rate-limited a recent child; fan-out is paused this round (child hit a provider rate limit)",
			"poll list_children and keep working elsewhere; delegate again when a slot or budget frees"},
		{"DELEGATION_SPEND: this turn's fan-out spend cap is reached (spent $0.6000 of $0.50 this turn; collect completed reports with list_children)",
			"poll list_children and keep working elsewhere; delegate again when a slot or budget frees"},
		{"DELEGATION_WALL: child exceeded its wall-clock budget (budget 10m0s)",
			"poll list_children and keep working elsewhere; delegate again when a slot or budget frees"},
		{"DELEGATION_DEPTH: children cannot spawn children (depth is capped at 1)",
			"do this piece yourself in this turn instead of delegating it"},
		{"DELEGATION_ISOLATION: mutating children are disabled in this session (TALONSTUDIO_CHILD_ISOLATION=readonly)",
			"finish the file work here, or run mutating children under TALONSTUDIO_CHILD_ISOLATION=worktree"},
	}
	for _, c := range cases {
		p, ok := Lookup(idx, "delegate", c.err)
		if !ok {
			t.Errorf("no pattern for %q", c.err)
			continue
		}
		if p.Fix != c.wantFix {
			t.Errorf("Lookup(%q).Fix = %q, want %q", c.err, p.Fix, c.wantFix)
		}
		if !strings.Contains(p.Reminder(), "known fix") {
			t.Errorf("Reminder lost its framing: %s", p.Reminder())
		}
	}
}
