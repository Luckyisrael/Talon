package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// W2b: the build loop enriches the system prompt once, then reuses that exact
// string every round and only rebuilds it when the ADRs or memory feeding it
// changed. That keeps the prompt byte-for-byte identical round-over-round when no
// context was written, so the provider's prompt cache holds instead of being
// busted by a freshly-built (if identical) string each round.
//
// Two properties make the reuse sound, and these tests pin both:
//
//  1. enrichPromptWithContext is deterministic — same session state in, the same
//     bytes out. So skipping a rebuild can never diverge from what a rebuild would
//     have produced; the reuse is purely a cost saving, not a behaviour change.
//  2. len(GetADRs()) / len(GetMemory()) is a faithful change signal — it moves
//     exactly when the enriched prompt's content moves, including once the capped
//     tail starts sliding, so the loop's guard never skips a rebuild it owed.

func TestEnrichPromptIsDeterministic(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)
	sess.LoadADRs([]store.ArchitectureDecision{
		{Decision: "Use MWA for signing", Outcome: "adopted", Context: "the Talon holds the keys"},
	})
	sess.LoadMemory([]store.ProjectMemory{
		{EntryType: "screen", Content: "Home lists recent transfers"},
	})

	const base = "SYSTEM PROMPT BASE"
	first := o.enrichPromptWithContext(sess, base)
	for i := 0; i < 5; i++ {
		if again := o.enrichPromptWithContext(sess, base); again != first {
			t.Fatalf("enriched prompt changed across calls with no state change (call %d)\n--- first ---\n%s\n--- again ---\n%s", i, first, again)
		}
	}
}

func TestEnrichPromptChangesWhenMemoryGrows(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	const base = "SYSTEM"
	before := o.enrichPromptWithContext(sess, base)
	beforeCount := len(sess.GetMemory())

	sess.LoadMemory([]store.ProjectMemory{{EntryType: "screen", Content: "a brand new screen exists"}})

	after := o.enrichPromptWithContext(sess, base)
	afterCount := len(sess.GetMemory())

	if afterCount == beforeCount {
		t.Fatalf("memory count did not move (%d); the loop's guard would never notice the write", afterCount)
	}
	if after == before {
		t.Fatal("enriched prompt did not change after memory grew; the count signal and the prompt content have diverged")
	}
	if !strings.Contains(after, "a brand new screen exists") {
		t.Errorf("the new memory is not reflected in the enriched prompt:\n%s", after)
	}
}

// Once ADRs exceed the display cap (maxADRs = 12) the enriched prompt shows only
// the most recent 12 — but the raw count keeps climbing, and each new ADR slides
// the shown window, changing which decisions appear. len(GetADRs()) climbs in
// lockstep, so the loop's guard still fires on every write. A guard that keyed on
// "how many are shown" instead would freeze at 12 and silently stop refreshing
// the prompt after the 12th decision.
func TestEnrichPromptCountSignalSurvivesTheCap(t *testing.T) {
	o := New(nil, nil, loadKnowledge(t))
	sess := newTestSession(t)

	mk := func(n int) []store.ArchitectureDecision {
		adrs := make([]store.ArchitectureDecision, n)
		for i := range adrs {
			adrs[i] = store.ArchitectureDecision{
				Decision: fmt.Sprintf("decision %d", i),
				Outcome:  "adopted",
			}
		}
		return adrs
	}

	sess.LoadADRs(mk(13))
	promptA := o.enrichPromptWithContext(sess, "S")
	countA := len(sess.GetADRs())

	sess.LoadADRs(mk(14)) // one more decision; the oldest shown ADR should fall off the window
	promptB := o.enrichPromptWithContext(sess, "S")
	countB := len(sess.GetADRs())

	if countB == countA {
		t.Fatalf("raw ADR count stalled at %d past the cap; the guard would miss further writes", countA)
	}
	if promptA == promptB {
		t.Fatal("enriched prompt did not change when a 14th ADR pushed the shown window past the 12-decision cap")
	}
}
