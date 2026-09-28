package orchestrator

import (
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L15 feature marking truth — FM-C1b, written before the implementation
// (TEST_RULES §3):
//
//  1. PlanTaskRows carries each shaped feature's declared files (the
//     seed is the only path from plan JSON to the checklist, so a row
//     without files means the contract never reaches the workspace).
//  2. A feature with declared files is marked complete only when EVERY
//     declared file was written this run — the screens' honest bar,
//     called on the same verified-success path.
//  3. Partial writes leave the feature pending: marking on a subset is
//     the false completion the E2E audit exists to catch.
//  4. A feature without files is never heuristic-marked (model-only,
//     exactly as today).
//  5. The audit flips a completed feature back to pending when a
//     declared file does not exist, naming the file; with all files
//     present it stays completed; file-less tasks are untouched.
func TestPlanTaskRowsCarryFeatureFiles(t *testing.T) {
	plan := &ai.AppPlan{
		Screens: []ai.Screen{{Name: "Send", Description: "send flow"}},
		Features: []ai.PlanFeature{
			{Text: "send SOL", Files: []string{"app/send.tsx", "src/wallet.ts"},
				Acceptance: "confirm sheet shows"},
			{Text: "legacy capability"},
		},
	}
	rows := PlanTaskRows(plan)
	if len(rows) != 3 {
		t.Fatalf("want screen + 2 feature rows, got %d", len(rows))
	}
	var found, legacy bool
	for _, r := range rows {
		switch r.ID {
		case "plan:feature:0":
			found = true
			if len(r.Files) != 2 || r.Files[0] != "app/send.tsx" {
				t.Errorf("shaped feature row must carry its files, got %+v", r.Files)
			}
		case "plan:feature:1":
			legacy = true
			if len(r.Files) != 0 {
				t.Errorf("file-less feature must seed no files, got %+v", r.Files)
			}
		}
	}
	if !found || !legacy {
		t.Fatalf("both feature rows must exist (found=%v legacy=%v)", found, legacy)
	}
}

func TestMarkFeatureTasksFromFiles(t *testing.T) {
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	sess.AddTask("plan:feature:0", "Feature: send SOL", "app/send.tsx", "src/wallet.ts")
	sess.AddTask("plan:feature:1", "Feature: half written", "app/half.tsx")
	sess.AddTask("plan:feature:2", "Feature: legacy capability")

	// Partial write: one of two declared files touched → stays pending.
	o.markScreenTasksFromFiles(sess, []string{"app/send.tsx"})
	if st := taskByID(t, sess, "plan:feature:0").Status; st != "pending" {
		t.Errorf("partial write must leave the feature pending, got %q", st)
	}

	// All declared files touched → completed, with the honest note.
	o.markScreenTasksFromFiles(sess, []string{"app/send.tsx", "src/wallet.ts", "app/half.tsx", "app/other.tsx"})
	if got := taskByID(t, sess, "plan:feature:0"); got.Status != "completed" {
		t.Errorf("all declared files written → completed, got %q (%s)", got.Status, got.Result)
	} else if !strings.Contains(got.Result, "compiles") {
		t.Errorf("the mark must record the verified bar, got %q", got.Result)
	}
	// Single-file feature touched → completed too.
	if got := taskByID(t, sess, "plan:feature:1"); got.Status != "completed" {
		t.Errorf("single declared file written → completed, got %q", got.Status)
	}
	// File-less feature never heuristic-marked.
	if got := taskByID(t, sess, "plan:feature:2"); got.Status != "pending" {
		t.Errorf("feature without declared files must stay model-only, got %q", got.Status)
	}
}

func TestAuditFlipsFeatureWithMissingDeclaredFile(t *testing.T) {
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	sess.AddTask("plan:feature:0", "Feature: send SOL", "app/send.tsx", "src/wallet.ts")
	sess.AddTask("plan:feature:1", "Feature: intact", "app/intact.tsx")
	sess.AddTask("plan:feature:2", "Feature: legacy capability")
	for _, id := range []string{"plan:feature:0", "plan:feature:1", "plan:feature:2"} {
		sess.MarkPlanTask(id, taskByID(t, sess, id).Description, "completed", "model said so")
	}

	exists := map[string]bool{
		"app/send.tsx":   true,
		"src/wallet.ts":  false, // the lie: completed without this file
		"app/intact.tsx": true,
	}
	opts := RunOptions{Exists: func(p string) bool { return exists[p] }}
	o.auditScreenTasks(sess, opts)

	flipped := taskByID(t, sess, "plan:feature:0")
	if flipped.Status != "pending" {
		t.Fatalf("missing declared file must flip the feature back to pending, got %q", flipped.Status)
	}
	if !strings.Contains(flipped.Result, "src/wallet.ts") {
		t.Errorf("the flip must name the missing file, got %q", flipped.Result)
	}
	if got := taskByID(t, sess, "plan:feature:1"); got.Status != "completed" {
		t.Errorf("all declared files present → stays completed, got %q", got.Status)
	}
	if got := taskByID(t, sess, "plan:feature:2"); got.Status != "completed" {
		t.Errorf("file-less completed feature must be untouched (nothing to verify), got %q", got.Status)
	}
}

func taskByID(t *testing.T, sess *SessionState, id string) Task {
	t.Helper()
	for _, task := range sess.GetTasks() {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("no task %q", id)
	return Task{}
}
