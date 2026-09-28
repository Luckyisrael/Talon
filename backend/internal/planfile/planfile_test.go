package planfile

import (
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// fixture is the artifact's contract shape: one plan, its derived rows, and a
// status map keyed by description (rows join the file on Description, never on
// ID — see the package doc).
func fixture() (*ai.AppPlan, []Row, map[string]string) {
	plan := &ai.AppPlan{
		Screens:    []ai.Screen{{Name: "Home", Description: "Landing"}},
		Features:   []ai.PlanFeature{{Text: "Wire src/app.ts as entry point"}},
		Navigation: "stack",
		Packages:   []string{"expo-router"},
	}
	rows := []Row{
		{ID: "plan:screen:home", Description: "Screen: Home — Landing"},
		{ID: "plan:feature:0", Description: "Feature: Wire src/app.ts as entry point"},
	}
	return plan, rows, map[string]string{}
}

// TestRenderReadRoundTrip is the eval gate's "plan file written and re-read
// stable": a render is byte-deterministic, passes its own marker, round-trips
// its checkbox states through ParseStatuses, and a no-op Sync leaves every
// byte in place.
func TestRenderReadRoundTrip(t *testing.T) {
	plan, rows, statuses := fixture()
	statuses["Screen: Home — Landing"] = "completed"
	statuses["Feature: Wire src/app.ts as entry point"] = "failed"

	content := Render(plan, rows, statuses)
	if Edited(content) {
		t.Error("a fresh render fails its own marker — hash and body disagree")
	}
	if Render(plan, rows, statuses) != content {
		t.Error("render is not byte-deterministic — same inputs must give the same file")
	}
	if Sync(content, statuses) != content {
		t.Error("a no-op sync changed bytes — writes would churn on every turn")
	}

	got := ParseStatuses(content)
	if got["Screen: Home — Landing"] != "completed" {
		t.Errorf("completed round-trip = %q", got["Screen: Home — Landing"])
	}
	if got["Feature: Wire src/app.ts as entry point"] != "failed" {
		t.Errorf("failed round-trip = %q", got["Feature: Wire src/app.ts as entry point"])
	}
	if len(got) != 2 {
		t.Errorf("parsed %d statuses, want exactly the 2 rendered rows: %v", len(got), got)
	}

	// Plan-only detail sections ride along for the human reader.
	for _, want := range []string{"# Plan", "## Checklist", "## Navigation\nstack", "- expo-router"} {
		if !strings.Contains(content, want) {
			t.Errorf("render is missing %q", want)
		}
	}
}

// TestEditedDetectsUserChange pins the direction signal: any body change — a
// toggled box, an appended note, or a stripped marker — reads as edited.
func TestEditedDetectsUserChange(t *testing.T) {
	plan, rows, statuses := fixture()
	content := Render(plan, rows, statuses)

	if Edited(content) {
		t.Error("untouched render reported as edited")
	}
	if toggled := strings.Replace(content, "- [ ] Feature:", "- [x] Feature:", 1); !Edited(toggled) {
		t.Error("a toggled checkbox was not detected as an edit")
	}
	if appended := content + "\nMy note.\n"; !Edited(appended) {
		t.Error("an appended note was not detected as an edit")
	}
	if !Edited(stripMarker(content)) {
		t.Error("a missing marker must count as edited — a hand-written file is the user's")
	}
}

// TestParseStatusesVocabulary: the four talon markers import; anything else
// (unknown markers, plain list items) is the user's own list and is skipped.
func TestParseStatusesVocabulary(t *testing.T) {
	got := ParseStatuses("## Checklist\n- [ ] pending one\n- [x] done one\n- [~] underway\n- [!] blocked\n- [?] not ours\n- no box at all\n")
	want := map[string]string{
		"pending one": "pending",
		"done one":    "completed",
		"underway":    "in_progress",
		"blocked":     "failed",
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestSyncPreservesUserText is the two-way contract's system→file half: a flip
// rewrites only markers of known rows, keeps the user's prose and their own
// items byte-for-byte, and re-seals (adopts) their text under a fresh marker.
func TestSyncPreservesUserText(t *testing.T) {
	plan, rows, statuses := fixture()
	content := Render(plan, rows, statuses)

	// The user edits: unchecks the completed row, appends a note and an item
	// of their own under the checklist.
	user := strings.Replace(content, "- [ ] Screen:", "- [x] Screen:", 1)
	user = strings.Replace(user, "## Checklist\n", "## Checklist\n- [ ] My own extra item\n", 1)
	user += "\nRemember: keep the icon simple.\n"
	if !Edited(user) {
		t.Fatal("the user's edit did not register as edited")
	}

	// The system's statuses: neither row has been flipped — the [x] under the
	// screen row was the user's toggle, and Sync must write pending back.
	statuses["Screen: Home — Landing"] = "pending"
	statuses["Feature: Wire src/app.ts as entry point"] = "pending"
	synced := Sync(user, statuses)

	if !strings.Contains(synced, "- [ ] My own extra item") {
		t.Error("sync dropped the user's own checklist item")
	}
	if !strings.Contains(synced, "Remember: keep the icon simple.") {
		t.Error("sync dropped the user's prose")
	}
	if !strings.Contains(synced, "- [ ] Screen: Home — Landing") {
		t.Error("sync did not write the system's status back over the user's toggle")
	}
	if Edited(synced) {
		t.Error("sync did not re-seal — the file would re-import forever")
	}
	if st := ParseStatuses(synced)["Screen: Home — Landing"]; st != "pending" {
		t.Errorf("synced status = %q, want pending", st)
	}
	// The user's own item carried a talon marker but names no row, so Sync
	// left its line byte-identical (and only the row-joining import at seed
	// could ever read it back — see reconcilePlanArtifact).
	if !strings.Contains(synced, "- [ ] My own extra item") {
		t.Error("sync rewrote the user's own item")
	}
}

// TestWriteNoChurn: creation writes, identical rewrites report no change,
// and a missing file reads as absent without error.
func TestWriteNoChurn(t *testing.T) {
	ws := t.TempDir()
	if _, existed, err := Read(ws); err != nil || existed {
		t.Fatalf("fresh workspace read: existed=%v err=%v", existed, err)
	}

	plan, rows, statuses := fixture()
	content := Render(plan, rows, statuses)

	changed, err := Write(ws, content)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	got, existed, err := Read(ws)
	if err != nil || !existed || got != content {
		t.Fatalf("re-read: existed=%v equal=%v err=%v", existed, got == content, err)
	}
	if changed, err := Write(ws, content); err != nil || changed {
		t.Errorf("identical rewrite: changed=%v err=%v — must report no change", changed, err)
	}
}

// TestEnabled pins the flag surface: the artifact ships dark, same switch as
// the rest of W3.
func TestEnabled(t *testing.T) {
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "")
	if Enabled() {
		t.Error("unset flag must be off")
	}
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "v1")
	if !Enabled() {
		t.Error("TALONSTUDIO_PLAN_TOOLS=v1 must be on")
	}
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "0")
	if Enabled() {
		t.Error(`"0" must be off`)
	}
}
