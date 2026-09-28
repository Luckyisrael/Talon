package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// L15 plan contract — FM-C1a, written before the implementation
// (TEST_RULES §3):
//
//  1. Legacy plans (plain-string features) load byte-identically — the
//     shaped form is additive; one unmarshal path accepts both.
//  2. Shaped features carry text/files/acceptance through a marshal
//     round trip, so a plan saved and reloaded keeps its contract.
//  3. Malformed declared files are rejected at the door WITH the
//     offending feature named — never accepted envelope-only (RESEARCH
//     P0.2), never silently repaired.
//  4. A feature without files stays legal: model-only marking is the
//     honest legacy path, not an error.
func TestPlanFeatureAcceptsLegacyAndShaped(t *testing.T) {
	legacy := `{"screens": [{"name": "Send", "description": "d"}],
		"features": ["connect wallet via MWA", "view recent transactions"]}`
	var p AppPlan
	if err := json.Unmarshal([]byte(legacy), &p); err != nil {
		t.Fatalf("legacy plan must load unchanged: %v", err)
	}
	if len(p.Features) != 2 || p.Features[0].Text != "connect wallet via MWA" {
		t.Fatalf("legacy features must parse to text-only entries, got %+v", p.Features)
	}
	if p.Features[0].Files != nil || p.Features[0].Acceptance != "" {
		t.Errorf("legacy entries must carry no files/acceptance, got %+v", p.Features[0])
	}

	shaped := `{"features": [{"text": "send SOL",
		"files": ["app/send.tsx", "src/wallet.ts"],
		"acceptance": "send flow compiles and shows the confirm sheet"}]}`
	var q AppPlan
	if err := json.Unmarshal([]byte(shaped), &q); err != nil {
		t.Fatalf("shaped plan must load: %v", err)
	}
	f := q.Features[0]
	if f.Text != "send SOL" || len(f.Files) != 2 ||
		!strings.Contains(f.Acceptance, "confirm sheet") {
		t.Fatalf("shaped feature must parse fully, got %+v", f)
	}

	// Round trip: what the door persists is what the next load accepts.
	out, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again AppPlan
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("reloaded plan must load: %v", err)
	}
	if len(again.Features) != 1 || len(again.Features[0].Files) != 2 ||
		again.Features[0].Files[0] != "app/send.tsx" {
		t.Fatalf("round trip must keep the contract, got %+v", again.Features)
	}
}

func TestPlanRejectsMalformedFeatureFiles(t *testing.T) {
	malformed := []struct{ name, file string }{
		{"absolute path", "/etc/passwd"},
		{"parent traversal", "../secrets.ts"},
		{"node_modules", "node_modules/react/index.js"},
		{"glob", "app/*.tsx"},
	}
	for _, tc := range malformed {
		p := AppPlan{Features: []PlanFeature{{Text: "send SOL", Files: []string{tc.file}}}}
		err := p.Validate()
		if err == nil {
			t.Errorf("%s: the plan door must reject file %q, but it passed", tc.name, tc.file)
			continue
		}
		if !strings.Contains(err.Error(), "send SOL") {
			t.Errorf("%s: rejection must name the offending feature, got %q", tc.name, err)
		}
	}

	// Validation goes through an addressable value (pointer receiver).
	validate := func(p AppPlan) error { return p.Validate() }

	if err := validate(AppPlan{Features: []PlanFeature{
		{Text: "send SOL", Files: []string{"app/send.tsx", "src/wallet.ts"}},
	}}); err != nil {
		t.Errorf("valid declared files must pass, got %v", err)
	}
	if err := validate(AppPlan{Features: []PlanFeature{{Text: "legacy capability"}}}); err != nil {
		t.Errorf("a feature without files stays legal (model-only marking), got %v", err)
	}
	if err := validate(AppPlan{Features: []PlanFeature{{Text: "   "}}}); err == nil {
		t.Error("an empty feature text must be rejected, not seeded as a nameless task")
	}
}
