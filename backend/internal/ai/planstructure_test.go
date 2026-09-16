package ai

import (
	"encoding/json"
	"testing"
)

// The plan's structure field has to survive the JSON round trip: the planner
// emits it, the API marshals it, the frontend renders it. A field that
// marshals to nothing is a plan card that never shows the prune list.
func TestAppPlanStructureRoundTrip(t *testing.T) {
	in := `{"message":"m","plan":{"screens":[{"name":"Auth","description":"d"}],"features":["f"],"navigation":"n","packages":[],"structure":{"keep":["constants/theme.ts"],"prune":["app/index.tsx (demo home — replaced by Onboarding)"],"write":["app/onboarding.tsx"],"folders":"app/ for routes"}}}`

	var resp struct {
		Message string  `json:"message"`
		Plan    *AppPlan `json:"plan"`
	}
	if err := json.Unmarshal([]byte(in), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Plan == nil {
		t.Fatal("plan missing")
	}
	if resp.Plan.Structure == nil {
		t.Fatal("structure missing — the plan card would never show keep/prune/write")
	}
	if len(resp.Plan.Structure.Prune) != 1 {
		t.Fatalf("prune entries: %d, want 1", len(resp.Plan.Structure.Prune))
	}
	if got := resp.Plan.Structure.Prune[0]; got != "app/index.tsx (demo home — replaced by Onboarding)" {
		t.Errorf("prune[0] = %q", got)
	}
	if len(resp.Plan.Structure.Keep) != 1 || len(resp.Plan.Structure.Write) != 1 {
		t.Errorf("keep=%d write=%d", len(resp.Plan.Structure.Keep), len(resp.Plan.Structure.Write))
	}

	// Re-marshal: the frontend's PlanData mirrors this shape.
	out, err := json.Marshal(resp.Plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again AppPlan
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if again.Structure == nil || len(again.Structure.Prune) != 1 {
		t.Fatalf("round trip lost structure: %+v", again.Structure)
	}
}

// A plan without a structure must still parse — older plans in the store
// predate the field, and a plan that is still open is null by design.
func TestAppPlanWithoutStructure(t *testing.T) {
	in := `{"screens":[],"features":[],"navigation":"","packages":[]}`
	var p AppPlan
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal legacy plan: %v", err)
	}
	if p.Structure != nil {
		t.Errorf("expected no structure on a legacy plan, got %+v", p.Structure)
	}
}
