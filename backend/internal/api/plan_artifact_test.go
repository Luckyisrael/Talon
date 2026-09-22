package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/planfile"
)

// TestPhaseW3PlanArtifactWrittenAndStable is W3.4's eval gate: the settled
// plan lands in the workspace as .talon/plan.md, passes its own marker on a
// re-read (byte-stable render), carries a checkpoint of its pre-image — and
// with the flag dark, savePlan writes no file at all.
func TestPhaseW3PlanArtifactWrittenAndStable(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		return 200, textReply(planEnvelope(t, "Persist src/app.ts as entry point"))
	})
	f.runPlan(t, nil)

	artifact := filepath.Join(f.ws, ".talon", "plan.md")
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("plan artifact not written: %v", err)
	}
	content := string(raw)

	if planfile.Edited(content) {
		t.Error("written artifact fails its own marker — not re-readable as stable")
	}
	for _, want := range []string{
		"# Plan",
		"## Checklist",
		"Screen: Home — Landing",
		"Feature: Persist src/app.ts as entry point",
		"## Navigation\nstack",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("artifact is missing %q", want)
		}
	}
	statuses := planfile.ParseStatuses(content)
	if len(statuses) == 0 {
		t.Fatal("artifact parsed to no checkbox states")
	}
	for desc, st := range statuses {
		if st != "pending" {
			t.Errorf("fresh artifact row %q = %q, want pending", desc, st)
		}
	}

	// Re-save the same plan: the render is deterministic, so the file must be
	// byte-identical — "written and re-read stable" across two saves.
	var resp ai.PlanResponse
	if err := json.Unmarshal([]byte(planEnvelope(t, "Persist src/app.ts as entry point")), &resp); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	f.h.savePlan(f.pid, 0, &resp)
	raw2, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("re-read artifact: %v", err)
	}
	if string(raw2) != content {
		t.Error("re-saving the same plan changed the artifact — not byte-stable")
	}

	// The first save checkpointed the pre-image: the file did not exist, and
	// RestoreCheckpoint must be able to undo creation exactly.
	cps, err := f.st.ListCheckpoints(f.pid, 10)
	if err != nil {
		t.Fatalf("ListCheckpoints: %v", err)
	}
	found := false
	for _, cp := range cps {
		for _, file := range cp.Files {
			if file.Path == planfile.RelPath && !file.Existed {
				found = true
			}
		}
	}
	if !found {
		t.Error("no checkpoint holds the artifact's pre-image (absent file)")
	}

	// Flag dark: savePlan writes the store and nothing else.
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "")
	if err := os.Remove(artifact); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	f.h.savePlan(f.pid, 0, &resp)
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Error("flag-off savePlan wrote the artifact")
	}
}
