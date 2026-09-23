package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// needGit skips the suite where git is absent — the isolation feature is
// honestly git-dependent, and a skip says so better than a false green.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=talon", "GIT_AUTHOR_EMAIL=talon@test",
		"GIT_COMMITTER_NAME=talon", "GIT_COMMITTER_EMAIL=talon@test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initRepo builds a committed workspace: base.txt at "base\n" plus a
// .talon/config.json (the untracked-config fidelity case rides the same
// fixture).
func initRepo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".talon", "config.json"), []byte(`{"permission_mode":"ask"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-qm", "baseline")
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOpenRefusesNonGitWorkspace(t *testing.T) {
	_, err := Open(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "CHILD_WORKTREE") ||
		!strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("want a structured refusal, got %v", err)
	}
}

// The happy path: child writes a new file and edits an old one; merge-back
// lands both in the parent; the worktree and its registration are gone.
func TestMergeBackAppliesChildDiffAndCleansUp(t *testing.T) {
	ws := initRepo(t)
	ov, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	// The config surface must have ridden along (untracked in git).
	if got := read(t, filepath.Join(ov.Root, ".talon", "config.json")); !strings.Contains(got, "permission_mode") {
		t.Errorf(".talon not carried into worktree: %q", got)
	}
	if err := os.WriteFile(filepath.Join(ov.Root, "child-new.txt"), []byte("child work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ov.Root, "base.txt"), []byte("base\nchild edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ov.MergeBack(); err != nil {
		t.Fatalf("MergeBack: %v", err)
	}
	if got := read(t, filepath.Join(ws, "child-new.txt")); got != "child work\n" {
		t.Errorf("new file not merged: %q", got)
	}
	if got := read(t, filepath.Join(ws, "base.txt")); got != "base\nchild edit\n" {
		t.Errorf("edit not merged: %q", got)
	}
	if _, err := os.Stat(ov.Root); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after dispose: %v", err)
	}
	// The registration is gone too: exactly one entry remains — main.
	out := run(t, ws, "worktree", "list")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 1 || !strings.HasPrefix(lines[0], ws) {
		t.Errorf("only the main worktree should remain:\n%s", out)
	}
}

// The lost-update class: parent and child edited the same region. The
// apply must fail as a structured stage error — parent state preserved —
// and cleanup must still run (gate: worktree cleanup on dispose, even on
// a failed merge).
func TestMergeBackConflictIsStructuredAndStillCleansUp(t *testing.T) {
	ws := initRepo(t)
	ov, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ov.Root, "base.txt"), []byte("child version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Parent moves the same line after the child checked out.
	if err := os.WriteFile(filepath.Join(ws, "base.txt"), []byte("parent version\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = ov.MergeBack()
	if err == nil {
		t.Fatal("a same-line conflict must not merge silently")
	}
	if !strings.Contains(err.Error(), "merge apply") || !strings.Contains(err.Error(), "CHILD_WORKTREE") {
		t.Errorf("error must name its stage: %v", err)
	}
	if got := read(t, filepath.Join(ws, "base.txt")); got != "parent version\n" {
		t.Errorf("parent state must survive a failed merge, got %q", got)
	}
	if _, statErr := os.Stat(ov.Root); !os.IsNotExist(statErr) {
		t.Errorf("cleanup must run even when the merge fails: %v", statErr)
	}
}

// A failed child's dispose: changes discarded, registration cleaned.
func TestDiscardDropsChildChanges(t *testing.T) {
	ws := initRepo(t)
	ov, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ov.Root, "oops.txt"), []byte("partial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ov.Discard(); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "oops.txt")); !os.IsNotExist(err) {
		t.Errorf("discarded child's file reached the parent: %v", err)
	}
	if _, err := os.Stat(ov.Root); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists: %v", err)
	}
}

// No changes: merge is a no-op, cleanup still runs.
func TestMergeBackWithNoChanges(t *testing.T) {
	ws := initRepo(t)
	ov, err := Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := ov.MergeBack(); err != nil {
		t.Fatalf("no-change merge: %v", err)
	}
	if _, err := os.Stat(ov.Root); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists: %v", err)
	}
}
