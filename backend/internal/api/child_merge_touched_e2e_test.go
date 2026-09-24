package api

// Child-merge → parent-journal law (the child-journal backlog row 42 left
// open, closed here).
//
// A mutating child's diff lands at dispose through git apply — never the
// file tools — so the parent turn's journal and touched set never saw the
// child's files. A parent turn whose only writes came from children then
// finished files_touched == [] (done-no-changes: the verify ladder skipped
// over child-written files), the prompt row and done payload under-reported
// what the turn changed, and undo could never revert a child's work.
//
// Fix under test: at dispose, BEFORE the patch applies, the runner
// enumerates the merge-back's file list (worktree.ChangedFiles) and marks
// it into the parent turn's FS (MarkChanged — pre-images captured
// pre-merge, so undo reverts child work like any parent write); MergeBack
// then reports what actually landed, and what never landed is withdrawn
// (UnmarkChanged). The touched set feeding unionTouched on the prompt row
// is the same closure NewAgentTurn already closes over — no loop change.
//
// TEST_RULES rule 1 — the failure modes this file enumerates, each pinned
// by one subtest:
//
// F1. Mutating child creates alpha.txt and edits shared.txt line 2; the
//     merge succeeds. Pre-fix the parent journal is empty → the turn's
//     union misses both files → verify skipped / stale row.
//     [child_merge_joins_parent_touched]
//
// F2. Same-line conflict (the parent changed line 2 after the child
//     checked out): apply refuses, child result errored. The staged paths
//     must be withdrawn — files_touched must not claim a merge that never
//     landed — and the workspace keeps the parent's version.
//     [failed_merge_touches_nothing]        (polarity: over-marking)
//
// F3. A successful child that wrote nothing marks nothing — the union
//     must not manufacture entries.
//     [no_change_child_marks_nothing]       (polarity)
//
// F4. The parent wrote shared.txt this turn BEFORE the child's conflicting
//     change: the parent's own touch and its ORIGINAL pre-image survive
//     the child's failed merge (unmark must not strip a file-tool write).
//     [parent_touch_survives_failed_merge]  (polarity)
//
// F5. A child that wrote files then failed is discarded: neither the
//     workspace nor the parent journal sees its partial work.
//     [discarded_child_marks_nothing]       (polarity)
//
// F6. Two mutating children run CONCURRENTLY (the real pool: the
//     merge-backs and their journal marks happen on pool goroutines while
//     tool lanes run in parallel): both children's files and both edits to
//     the shared file must join the parent touched set — and the race
//     detector gates that the shared journal is safe under that overlap.
//     Pre-fix the set is empty.     [parallel_children_join_touched]
//
// F1 also asserts unionTouched(nil, parentFS.Touched()) — the exact view
// NewAgentTurn closes over — carries both files, and that the pre-images
// are the PRE-merge state (alpha.txt Existed=false → undo deletes it;
// shared.txt journals "original line 2" → undo reverts the child's edit).
//
// TEST_RULES rule 2 — artifact: every subtest records into
// e2e/artifacts/child-merge-touched-e2e.json (touched set, union view,
// result error), t.Log'd on save.
//
// Staged RED capture (post-plumbing, pre-behavior — the parentFS seam and
// fixture param exist so RED is an assertion failure, not a build error;
// from backend/):
//
//	go test ./internal/api -count=1 -v -timeout 120s -run TestChildMergeBackJoinsParentTouchedSet
//
// GREEN gate: the same command passes post-fix; F1 and F6 fail pre-fix,
// F2–F5 pass both ways (they pin the fix from over-reaching).
//
// Determinism basis: no provider, no HTTP — the real childBuildFor, the
// real worktree overlay and real git, with the child loop stubbed to real
// file work (the W12 phase harness). git required (skipped when absent).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

type childMergeArtifact struct {
	Test        string           `json:"test"`
	GeneratedAt string           `json:"generated_at"`
	Subtests    []map[string]any `json:"subtests"`
}

func TestChildMergeBackJoinsParentTouchedSet(t *testing.T) {
	art := &childMergeArtifact{Test: "TestChildMergeBackJoinsParentTouchedSet"}
	save := func() {
		art.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.MarshalIndent(art, "", "  ")
		if err != nil {
			t.Logf("artifact marshal: %v", err)
			return
		}
		path := filepath.Join("..", "..", "..", "e2e", "artifacts", "child-merge-touched-e2e.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Logf("artifact dir: %v", err)
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Logf("artifact write: %v", err)
			return
		}
		t.Logf("child-merge artifact: %s", path)
	}
	t.Cleanup(save)
	record := func(name string, fields map[string]any) {
		fields["name"] = name
		art.Subtests = append(art.Subtests, fields)
	}

	has := func(hay []string, needle string) bool {
		for _, h := range hay {
			if h == needle {
				return true
			}
		}
		return false
	}
	journalOf := func(parentFS *tools.FS, path string) (tools.FileSnapshot, bool) {
		for _, s := range parentFS.Journal() {
			if s.Path == path {
				return s, true
			}
		}
		return tools.FileSnapshot{}, false
	}
	setLine := func(t *testing.T, path, line string, n int, content string) {
		t.Helper()
		cur, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(cur), "\n"), "\n")
		if n < 1 || n > len(lines) {
			t.Fatalf("line %d out of range 1..%d", n, len(lines))
		}
		lines[n-1] = content
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// ── F1: a successful merge joins the parent turn's touched set ──
	t.Run("child_merge_joins_parent_touched", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		svc := w12Fixture(t, ws, parentFS)
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "writer", Mutating: true,
			Prompt: "create alpha.txt from-alpha ; edit shared.txt 2 line-two-from-child",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if res.Errored {
			t.Fatalf("child failed: %s", res.Err)
		}
		touched := parentFS.Touched()
		if !has(touched, "alpha.txt") {
			t.Errorf("F1: parent touched set misses the child-created file: %v", touched)
		}
		if !has(touched, "shared.txt") {
			t.Errorf("F1: parent touched set misses the child-edited file: %v", touched)
		}
		// The exact view NewAgentTurn closes over onto the prompt row.
		union := unionTouched(nil, touched)
		if !has(union, "alpha.txt") || !has(union, "shared.txt") {
			t.Errorf("F1: union view (what verify sees) misses child files: %v", union)
		}
		// Pre-images are the PRE-merge state: undo must delete the created
		// file and revert the child's edit, not preserve it.
		if s, ok := journalOf(parentFS, "alpha.txt"); !ok || s.Existed {
			t.Errorf("F1: alpha.txt pre-image must be Existed=false (undo deletes it): ok=%v snap=%+v", ok, s)
		}
		if s, ok := journalOf(parentFS, "shared.txt"); !ok ||
			!strings.Contains(s.Content, "original line 2") || strings.Contains(s.Content, "line-two-from-child") {
			t.Errorf("F1: shared.txt pre-image must be the PRE-merge content: ok=%v snap=%+v", ok, s)
		}
		record("child_merge_joins_parent_touched", map[string]any{
			"touched": touched, "union": union, "result_err": res.Err,
		})
	})

	// ── F2: a refused merge withdraws its staged paths ──
	t.Run("failed_merge_touches_nothing", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		// The parent moved the same line after the child checked out —
		// deliberately OUTSIDE the journal so an empty touched set below
		// can only come from correct withdrawal.
		setLine(t, filepath.Join(ws, "shared.txt"), "", 2, "line-two-from-parent")
		svc := w12Fixture(t, ws, parentFS)
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "conflictor", Mutating: true,
			Prompt: "edit shared.txt 2 line-two-from-child",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if !res.Errored || !strings.Contains(res.Err, "did not merge back") {
			t.Fatalf("F2: want a structured merge refusal, got err=%v res=%+v", err, res)
		}
		if touched := parentFS.Touched(); len(touched) != 0 {
			t.Errorf("F2: a merge that never landed must touch nothing: %v", touched)
		}
		if union := unionTouched(nil, parentFS.Touched()); len(union) != 0 {
			t.Errorf("F2: union view must stay empty after a refused merge: %v", union)
		}
		if b, err := os.ReadFile(filepath.Join(ws, "shared.txt")); err != nil ||
			!strings.Contains(string(b), "line-two-from-parent") {
			t.Errorf("F2: parent state must survive: %q err=%v", b, err)
		}
		record("failed_merge_touches_nothing", map[string]any{
			"touched": parentFS.Touched(), "result_err": res.Err,
		})
	})

	// ── F3: a successful child that wrote nothing marks nothing ──
	t.Run("no_change_child_marks_nothing", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		svc := w12Fixture(t, ws, parentFS)
		// ";" parses to zero ops: the child succeeds without writing.
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "idle", Mutating: true, Prompt: ";",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if res.Errored {
			t.Fatalf("F3: no-change child must succeed: %+v", res)
		}
		if touched := parentFS.Touched(); len(touched) != 0 {
			t.Errorf("F3: union must not manufacture entries: %v", touched)
		}
		record("no_change_child_marks_nothing", map[string]any{
			"touched": parentFS.Touched(), "result_err": res.Err,
		})
	})

	// ── F4: a failed merge never strips the parent's own touch ──
	t.Run("parent_touch_survives_failed_merge", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		// The parent wrote shared.txt this turn THROUGH the journal first.
		if err := parentFS.Write("shared.txt", strings.Replace(
			w12ReadShared(t, ws), "original line 2", "line-two-from-parent", 1)); err != nil {
			t.Fatal(err)
		}
		svc := w12Fixture(t, ws, parentFS)
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "rival", Mutating: true,
			Prompt: "edit shared.txt 2 line-two-from-child",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if !res.Errored || !strings.Contains(res.Err, "did not merge back") {
			t.Fatalf("F4: want a structured merge refusal, got err=%v res=%+v", err, res)
		}
		touched := parentFS.Touched()
		if !has(touched, "shared.txt") {
			t.Errorf("F4: the parent's own file-tool touch must survive: %v", touched)
		}
		// ...and with its ORIGINAL pre-image (the file-tool write's), not
		// re-journalled from the failed merge's staging.
		s, ok := journalOf(parentFS, "shared.txt")
		if !ok || !strings.Contains(s.Content, "original line 2") ||
			strings.Contains(s.Content, "line-two-from-parent") {
			t.Errorf("F4: pre-image must remain the turn's first snapshot: ok=%v snap=%+v", ok, s)
		}
		record("parent_touch_survives_failed_merge", map[string]any{
			"touched": touched, "result_err": res.Err,
		})
	})

	// ── F5: a discarded child's partial work never marks ──
	t.Run("discarded_child_marks_nothing", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		svc := w12Fixture(t, ws, parentFS)
		// The child writes a file, then its loop fails: discard, no merge.
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "doomed", Mutating: true,
			Prompt: "create ghost.txt partial ; bogus op",
		})
		if err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if !res.Errored {
			t.Errorf("F5: a failed child must be an errored result: %+v", res)
		}
		if touched := parentFS.Touched(); len(touched) != 0 {
			t.Errorf("F5: discarded work must not mark: %v", touched)
		}
		if _, statErr := os.Stat(filepath.Join(ws, "ghost.txt")); !os.IsNotExist(statErr) {
			t.Errorf("F5: discarded child's file reached the workspace: %v", statErr)
		}
		record("discarded_child_marks_nothing", map[string]any{
			"touched": parentFS.Touched(), "result_err": res.Err,
		})
	})

	// ── F6: concurrent children join one parent journal ──
	t.Run("parallel_children_join_touched", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		ws := w12Repo(t)
		parentFS := tools.NewFS(ws)
		svc := w12Fixture(t, ws, parentFS)
		specs := []subagent.Spec{
			{Label: "par-alpha", Mutating: true,
				Prompt: "create alpha.txt from-alpha ; edit shared.txt 2 line-two-from-A"},
			{Label: "par-beta", Mutating: true,
				Prompt: "create beta.txt from-beta ; edit shared.txt 11 line-eleven-from-B"},
		}
		results := make(chan error, len(specs))
		for _, spec := range specs {
			spec := spec
			go func() {
				res, err := svc.Spawn(context.Background(), spec)
				if err != nil {
					results <- err
					return
				}
				if res.Errored {
					results <- fmt.Errorf("%s: %s", spec.Label, res.Err)
					return
				}
				results <- nil
			}()
		}
		for i := 0; i < len(specs); i++ {
			if err := <-results; err != nil {
				t.Fatalf("child failed: %v", err)
			}
		}
		touched := parentFS.Touched()
		for _, want := range []string{"alpha.txt", "beta.txt", "shared.txt"} {
			if !has(touched, want) {
				t.Errorf("F6: parent touched set misses %s under concurrency: %v", want, touched)
			}
		}
		union := unionTouched(nil, touched)
		for _, want := range []string{"alpha.txt", "beta.txt", "shared.txt"} {
			if !has(union, want) {
				t.Errorf("F6: union view misses %s: %v", want, union)
			}
		}
		// shared.txt was edited by BOTH children: its pre-image must be the
		// pre-merge baseline (first snapshot wins — undo restores HEAD).
		if s, ok := journalOf(parentFS, "shared.txt"); !ok ||
			!strings.Contains(s.Content, "original line 2") {
			t.Errorf("F6: shared.txt pre-image must be the pre-merge baseline: ok=%v snap=%+v", ok, s)
		}
		record("parallel_children_join_touched", map[string]any{
			"touched": touched, "union": union,
		})
	})
}

// w12ReadShared loads the fixture's shared.txt from the workspace.
func w12ReadShared(t *testing.T, ws string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ws, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
