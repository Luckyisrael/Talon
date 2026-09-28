package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/planfile"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// newPlanArtifactFixture wires a project whose plan_json holds one screen and
// one feature, an orchestrator holding that store, a workspace, and a session
// with the REAL persistence hook attached (CreateSession's wiring, extracted
// as taskPersistence so the test exercises the same code path production
// runs — flips included).
func newPlanArtifactFixture(t *testing.T) (*Orchestrator, *SessionState, *store.Store, string, int64) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "artifact.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("artifact@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Artifact", "v1", "artifact", "com.local.artifact", "com.local.artifact", "artifact")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	plan := ai.AppPlan{
		Screens:  []ai.Screen{{Name: "Home", Description: "Landing"}},
		Features: []ai.PlanFeature{{Text: "Wire src/app.ts as entry point"}},
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if err := st.UpdateProjectPlan(project.ID, string(planJSON)); err != nil {
		t.Fatalf("UpdateProjectPlan: %v", err)
	}
	ws := t.TempDir()
	o := &Orchestrator{store: st}
	sess := NewSession(1, project.ID, ws, events.NewSessionEmitter(1, 128))
	sess.AttachTaskPersistence(o.taskPersistence(sess))
	return o, sess, st, ws, project.ID
}

func mustReadArtifact(t *testing.T, ws string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(ws, planfile.RelPath))
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	return string(raw)
}

// TestPlanArtifactSeedFlipRoundTrip: build start backfills the artifact from
// the plan and backlog, and a flip through the real persistence hook reaches
// both the store and the file (system→file), leaving a marker-valid document.
func TestPlanArtifactSeedFlipRoundTrip(t *testing.T) {
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "v1")
	o, sess, st, ws, pid := newPlanArtifactFixture(t)

	o.seedPlanTasks(sess, pid)
	content := mustReadArtifact(t, ws)
	if planfile.Edited(content) {
		t.Error("backfilled artifact fails its marker")
	}
	if !strings.Contains(content, "- [ ] Screen: Home — Landing") {
		t.Errorf("backfill is missing the screen row:\n%s", content)
	}
	if !strings.Contains(content, "- [ ] Feature: Wire src/app.ts as entry point") {
		t.Errorf("backfill is missing the feature row:\n%s", content)
	}

	sess.MarkPlanTask("plan:screen:home", "Screen: Home — Landing", "completed", "built it")
	content = mustReadArtifact(t, ws)
	if !strings.Contains(content, "- [x] Screen: Home — Landing") {
		t.Errorf("flip did not reach the artifact:\n%s", content)
	}
	if planfile.Edited(content) {
		t.Error("synced artifact fails its marker — a later import would misread it")
	}
	rows, err := st.LoadPlanTasks(pid)
	if err != nil {
		t.Fatalf("LoadPlanTasks: %v", err)
	}
	for _, r := range rows {
		if r.TaskID == "plan:screen:home" && r.Status != "completed" {
			t.Errorf("store row = %q, want completed", r.Status)
		}
	}
}

// TestPlanArtifactUserCheckImportsAtSeed is the file→system half: the user
// checks a box (and writes a note) by hand; the next build start imports the
// state into the backlog and restores it into the new session, while their
// text — and their ownership of the file — survives untouched.
func TestPlanArtifactUserCheckImportsAtSeed(t *testing.T) {
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "v1")
	o, _, st, ws, pid := newPlanArtifactFixture(t)

	// The first build must seed into the FIXTURE workspace — newTestSession
	// binds its own t.TempDir, which would write the artifact somewhere else.
	sess1 := NewSession(1, pid, ws, events.NewSessionEmitter(1, 128))
	o.seedPlanTasks(sess1, pid) // first build creates the file

	artifact := filepath.Join(ws, planfile.RelPath)
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	edited := strings.Replace(string(raw),
		"- [ ] Feature: Wire src/app.ts as entry point",
		"- [x] Feature: Wire src/app.ts as entry point", 1)
	if edited == string(raw) {
		t.Fatal("fixture: feature line not found to edit")
	}
	edited += "\nKeep the icon simple.\n"
	if err := os.WriteFile(artifact, []byte(edited), 0o644); err != nil {
		t.Fatalf("write user edit: %v", err)
	}

	// Next build start: a fresh session seeds from the import.
	sess2 := NewSession(2, pid, ws, events.NewSessionEmitter(2, 128))
	sess2.AttachTaskPersistence(o.taskPersistence(sess2))
	o.seedPlanTasks(sess2, pid)

	rows, err := st.LoadPlanTasks(pid)
	if err != nil {
		t.Fatalf("LoadPlanTasks: %v", err)
	}
	sawCompleted := false
	for _, r := range rows {
		if r.TaskID == "plan:feature:0" {
			sawCompleted = r.Status == "completed"
			if !sawCompleted {
				t.Errorf("imported store row = %q, want completed (the unchecked protection must not win)", r.Status)
			}
		}
	}
	if !sawCompleted {
		t.Fatal("feature row missing from the store after seed")
	}
	restored := false
	for _, task := range sess2.GetTasks() {
		if task.ID == "plan:feature:0" {
			restored = task.Status == "completed"
		}
	}
	if !restored {
		t.Error("the new session did not restore the imported completion")
	}

	after, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("re-read artifact: %v", err)
	}
	if !strings.Contains(string(after), "Keep the icon simple.") {
		t.Error("the import boundary rewrote the user's note")
	}
	if !planfile.Edited(string(after)) {
		t.Error("import re-sealed the user's file — their edit should stand until the next flip")
	}
}

// TestPlanArtifactMidTurnImportOnFlip: a user check made WHILE a turn is
// running must not be flattened by the flip's own sync — every row except the
// one that just flipped imports first, then the file re-seals.
func TestPlanArtifactMidTurnImportOnFlip(t *testing.T) {
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "v1")
	o, sess, st, ws, pid := newPlanArtifactFixture(t)
	o.seedPlanTasks(sess, pid)

	// Mid-turn, the user checks the feature box by hand.
	artifact := filepath.Join(ws, planfile.RelPath)
	raw, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	edited := strings.Replace(string(raw),
		"- [ ] Feature: Wire src/app.ts as entry point",
		"- [x] Feature: Wire src/app.ts as entry point", 1)
	if err := os.WriteFile(artifact, []byte(edited), 0o644); err != nil {
		t.Fatalf("write user edit: %v", err)
	}

	// A flip on a DIFFERENT task triggers the sync: it must keep the user's
	// check (import it) and write its own line.
	sess.MarkPlanTask("plan:screen:home", "Screen: Home — Landing", "completed", "built it")

	content := mustReadArtifact(t, ws)
	if !strings.Contains(content, "- [x] Screen: Home — Landing") {
		t.Errorf("the flipped row did not reach the file:\n%s", content)
	}
	if !strings.Contains(content, "- [x] Feature: Wire src/app.ts as entry point") {
		t.Errorf("the flip flattened the user's mid-turn check:\n%s", content)
	}
	if planfile.Edited(content) {
		t.Error("sync did not re-seal after adopting the user's edit")
	}
	rows, err := st.LoadPlanTasks(pid)
	if err != nil {
		t.Fatalf("LoadPlanTasks: %v", err)
	}
	for _, r := range rows {
		if r.TaskID == "plan:feature:0" && r.Status != "completed" {
			t.Errorf("user's check was not imported into the store: %q", r.Status)
		}
	}
}

// TestPlanArtifactFlagOff: dark means no file is ever created or touched —
// seeding and flips behave exactly as before W3.4.
func TestPlanArtifactFlagOff(t *testing.T) {
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "")
	o, sess, _, ws, pid := newPlanArtifactFixture(t)

	o.seedPlanTasks(sess, pid)
	sess.MarkPlanTask("plan:screen:home", "Screen: Home — Landing", "completed", "built it")

	if _, err := os.Stat(filepath.Join(ws, planfile.RelPath)); !os.IsNotExist(err) {
		t.Errorf("flag-off created the artifact (stat err=%v)", err)
	}
}
