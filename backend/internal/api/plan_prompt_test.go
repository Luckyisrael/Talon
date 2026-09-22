package api

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/promptsec"
)

// ── W3.2/W3.5 gates (PLAN_WORLDCLASS W3) ───────────────────────────────
//
// W3.2: the plan prompt is assembled per project — static head, project
// map, survey digest, AGENTS.md chain — through promptsec, whose section
// fingerprints prove the cache discipline (a change lands in exactly one
// section; the others' hashes hold, so the cached prefix survives). W3.5:
// the cheap-model explore survey runs before drafting only while the
// workspace fingerprint has moved since the last successful one; a failed
// survey degrades to no digest, is retried next turn, and never fails the
// turn. Flag-off: byte-identical to the static plan prompt, zero workspace
// or model traffic (pinned end-to-end for the wire by
// TestPlanModeSendsNoToolsToModel).

func TestPhaseW3PlanPromptMapAndAgents(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		t.Errorf("prompt assembly must not call the plan model (call %d)", call)
		return 200, textReply("")
	})
	// A convention only the AGENTS.md chain can put in the prompt.
	if err := os.WriteFile(filepath.Join(f.ws, "AGENTS.md"),
		[]byte("W3_PLAN_AGENTS_RULE: every plan must cite src/app.ts\n"), 0o644); err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}

	ctx := context.Background()
	sys := f.h.planSystemPrompt(ctx, f.pid)

	if !strings.HasPrefix(strings.TrimSpace(sys), strings.TrimSpace(f.h.planPrompt)) {
		t.Error("assembled prompt does not start with the static plan head")
	}
	// The map's own fixed header — the digest and the AGENTS rule both name
	// src/app.ts too, so the path alone would not prove the map section.
	if !strings.Contains(sys, "## Current project structure") {
		t.Error("project map section missing — W3.2 map injection did not run")
	}
	if !strings.Contains(sys, "W3_PLAN_AGENTS_RULE") {
		t.Error("instructions section missing — the AGENTS.md chain was not injected")
	}
	if f.surveyCount() == 0 {
		t.Error("no survey ran before drafting — W3.5 did not fire on a never-surveyed workspace")
	}

	// Flag off: byte-identical to the static prompt, and no survey traffic.
	baseline := f.surveyCount()
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "")
	if got := f.h.planSystemPrompt(ctx, f.pid); got != f.h.planPrompt {
		t.Error("flag-off plan prompt must be byte-identical to h.planPrompt")
	}
	if n := f.surveyCount(); n != baseline {
		t.Errorf("flag-off turn ran the survey (%d → %d)", baseline, n)
	}
}

func TestPhaseW3PlanPromptSectionLocality(t *testing.T) {
	h := NewPromptHandler(nil, nil, t.TempDir(), "", nil, nil, nil, nil)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "app.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatalf("fixture file: %v", err)
	}

	before := h.planPromptSections(ws, 0, "digest A").Fingerprints()

	// A survey change moves only project/survey.
	mid := h.planPromptSections(ws, 0, "digest B").Fingerprints()
	if changed := promptsec.Diff(before, mid); !reflect.DeepEqual(changed, []string{"project/survey"}) {
		t.Errorf("survey change moved %v, want exactly [project/survey]", changed)
	}

	// An AGENTS.md edit moves only instructions/chain: head and map hashes
	// hold, so the prefix a prompt cache still covers is intact — the eval
	// gate's "cache fingerprint changes only in appended sections".
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("rule\n"), 0o644); err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	after := h.planPromptSections(ws, 0, "digest B").Fingerprints()
	if changed := promptsec.Diff(mid, after); !reflect.DeepEqual(changed, []string{"instructions/chain"}) {
		t.Errorf("chain edit moved %v, want exactly [instructions/chain]", changed)
	}
}

func TestPhaseW3PlanSurveyWhenStale(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		t.Errorf("prompt assembly must not call the plan model (call %d)", call)
		return 200, textReply("")
	})
	ctx := context.Background()

	first := f.h.planSystemPrompt(ctx, f.pid)
	if n := f.surveyCount(); n != 1 {
		t.Fatalf("first plan turn: survey ran %d times, want 1", n)
	}
	if !strings.Contains(first, "W3_SURVEY_DIGEST") {
		t.Error("assembled prompt carries no survey digest")
	}

	// Same fingerprint: byte-identical prompt, no second survey.
	second := f.h.planSystemPrompt(ctx, f.pid)
	if second != first {
		t.Error("fresh fingerprint changed the assembled prompt — cache discipline broken")
	}
	if n := f.surveyCount(); n != 1 {
		t.Errorf("fresh fingerprint re-surveyed: %d calls, want 1", n)
	}

	// The tree changed (size differs → fingerprint moves): survey again.
	app := filepath.Join(f.ws, "src", "app.ts")
	body, err := os.ReadFile(app)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(app, append(body, []byte("// changed\n")...), 0o644); err != nil {
		t.Fatalf("mutate fixture: %v", err)
	}
	f.h.planSystemPrompt(ctx, f.pid)
	if n := f.surveyCount(); n != 2 {
		t.Errorf("stale fingerprint did not re-survey: %d calls, want 2", n)
	}
}

func TestPhaseW3PlanSurveyFailureDegrades(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		t.Errorf("prompt assembly must not call the plan model (call %d)", call)
		return 200, textReply("")
	})
	// The survey endpoint rejects: a fast-fail 400, the same non-retryable
	// shape the plan loop's provider-rejects gate uses.
	f.mu.Lock()
	f.surveyRespond = func(int, map[string]any) (int, map[string]any) {
		return 400, map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "invalid_request_error", "message": "survey model unavailable"},
		}
	}
	f.mu.Unlock()

	ctx := context.Background()
	sys := f.h.planSystemPrompt(ctx, f.pid)

	if strings.Contains(sys, "W3_SURVEY_DIGEST") {
		t.Error("failed survey injected a digest")
	}
	if !strings.HasPrefix(strings.TrimSpace(sys), strings.TrimSpace(f.h.planPrompt)) {
		t.Error("failed survey lost the static plan head")
	}
	if !strings.Contains(sys, "## Current project structure") {
		t.Error("failed survey lost the project map — assembly must degrade to head+map only")
	}
	if n := f.surveyCount(); n != 1 {
		t.Errorf("survey attempts = %d, want 1", n)
	}

	// Failure is not cached: the next plan turn tries again.
	f.h.planSystemPrompt(ctx, f.pid)
	if n := f.surveyCount(); n != 2 {
		t.Errorf("failed survey was cached: attempts = %d, want 2", n)
	}
}
