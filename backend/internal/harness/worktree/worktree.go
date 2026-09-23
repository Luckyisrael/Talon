// Package worktree is the W12.1 write overlay: a mutating child runs in a
// private, detached git worktree of the workspace, and its diff merges
// back on dispose. Read-only children never touch this package — they
// keep the shared-workspace path at zero cost.
//
// The merge-back is deliberately three-staged and fail-loud:
//
//  1. capture — the worktree's own per-worktree index stages everything
//     (`git add -A`, so new files ride too) and emits a binary-safe patch;
//  2. apply — `git apply --binary` against the parent working tree, which
//     fails as a structured error when the child's base and the parent's
//     current state disagree (the lost-update class, surfaced instead of
//     silently winning);
//  3. verify — a reverse `--check` of the same patch proves it actually
//     landed (apply can no-op in ways a human would miss).
//
// Cleanup runs no matter which stage failed: a failed child's worktree is
// discarded, never merged; a failed merge still removes the directory and
// the worktree registration. Every error names its stage with the
// CHILD_WORKTREE prefix the model can act on.
package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Overlay is one child's private checkout.
type Overlay struct {
	// workspace is the parent repo — the merge target.
	workspace string
	// Root is the worktree directory the child's session is rooted at.
	Root string
}

func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 0 {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// Open prepares a detached worktree of workspace for one mutating child.
// It refuses (structured, never a panic) when git is unavailable or the
// workspace is not a git repository with at least one commit to check out.
func Open(workspace string) (*Overlay, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("CHILD_WORKTREE: git is not available, so mutating children cannot be isolated: %w", err)
	}
	if _, err := git(workspace, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, fmt.Errorf("CHILD_WORKTREE: %s is not a git repository — mutating children need TALONSTUDIO_CHILD_ISOLATION=worktree over a git workspace: %w", workspace, err)
	}
	dir, err := os.MkdirTemp("", "talon-child-worktree-")
	if err != nil {
		return nil, fmt.Errorf("CHILD_WORKTREE: could not create the worktree directory: %w", err)
	}
	if _, err := git(workspace, "worktree", "add", "--detach", dir, "HEAD"); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("CHILD_WORKTREE: git worktree add failed (a workspace with no commits cannot be overlaid): %w", err)
	}
	o := &Overlay{workspace: workspace, Root: dir}
	// The config surface rides along: untracked files never reach a
	// worktree checkout, and every .talon reader (config, hooks, modes,
	// pins) silently falling back to defaults inside the child would be a
	// lie about what governed it. Copy failure fails the overlay closed.
	if err := copyDir(filepath.Join(workspace, ".talon"), filepath.Join(dir, ".talon")); err != nil {
		_ = o.cleanup()
		return nil, fmt.Errorf("CHILD_WORKTREE: could not carry .talon into the worktree: %w", err)
	}
	return o, nil
}

// copyDir copies src's files into dst (overwriting), skipping absent src.
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if mkerr := os.MkdirAll(filepath.Dir(target), 0o755); mkerr != nil {
			return mkerr
		}
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// patch captures the child's work as a binary-safe diff against HEAD —
// staged through the worktree's own index so creations ride along.
func (o *Overlay) patch() ([]byte, error) {
	if _, err := git(o.Root, "add", "-A"); err != nil {
		return nil, fmt.Errorf("CHILD_WORKTREE: diff capture (stage) failed: %w", err)
	}
	out, err := git(o.Root, "diff", "--cached", "--binary")
	if err != nil {
		return nil, fmt.Errorf("CHILD_WORKTREE: diff capture failed: %w", err)
	}
	return out, nil
}

// cleanup removes the worktree directory and its registration, by force
// if needed — it must succeed even when the child's files are dirty (they
// are the whole point).
func (o *Overlay) cleanup() error {
	_, err := git(o.workspace, "worktree", "remove", "--force", o.Root)
	if err != nil {
		// The registration is stale-safe: prune drops entries whose
		// directory is gone, and the directory is what costs disk.
		_, _ = git(o.workspace, "worktree", "prune")
		if rerr := os.RemoveAll(o.Root); rerr != nil && !os.IsNotExist(rerr) {
			return fmt.Errorf("CHILD_WORKTREE: cleanup failed (worktree remove: %v): %w", err, rerr)
		}
		return nil
	}
	if rerr := os.RemoveAll(o.Root); rerr != nil && !os.IsNotExist(rerr) {
		return fmt.Errorf("CHILD_WORKTREE: cleanup failed: %w", rerr)
	}
	return nil
}

// wsLocks serializes disposal per parent workspace: parallel children's
// merge-backs (and the git metadata worktree remove does) must not
// interleave on one repo. Disjoint patches then apply in any order with
// no lost update; same-region patches conflict as structured errors.
var wsLocks sync.Map // workspace path → *sync.Mutex

func lockFor(workspace string) *sync.Mutex {
	m, _ := wsLocks.LoadOrStore(workspace, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu
}

// MergeBack applies the child's diff to the parent workspace, verifies it
// landed, then removes the worktree. A no-change child skips straight to
// cleanup. Every failure names its stage; cleanup is attempted on all
// paths, and its own error joins the stage error rather than hiding it.
func (o *Overlay) MergeBack() error {
	mu := lockFor(o.workspace)
	defer mu.Unlock()
	patch, capErr := o.patch()
	if capErr == nil && len(patch) > 0 {
		if err := o.apply(patch, false); err != nil {
			capErr = fmt.Errorf("CHILD_WORKTREE: merge apply failed (the child's base and the workspace now disagree — resolve the conflict by hand; its diff is preserved below):\n%s\n%w", patch, err)
		} else if err := o.apply(patch, true); err != nil {
			capErr = fmt.Errorf("CHILD_WORKTREE: merge verify failed (the patch was accepted but does not reverse-check against the workspace): %w", err)
		}
	}
	return errors.Join(capErr, o.cleanup())
}

// Discard removes the worktree without merging — the child failed, so its
// isolated changes never touch the parent workspace. The same per-workspace
// lock covers it: git worktree remove mutates shared repo metadata.
func (o *Overlay) Discard() error {
	mu := lockFor(o.workspace)
	defer mu.Unlock()
	return o.cleanup()
}

// apply runs git apply against the parent workspace; reverse turns it
// into the verification pass (a reverse check succeeds exactly when the
// patch is currently applied).
func (o *Overlay) apply(patch []byte, reverse bool) error {
	args := []string{"-C", o.workspace, "apply", "--binary", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--check", "--reverse")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdin = bytes.NewReader(patch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 0 {
			return fmt.Errorf("%v: %s", err, msg)
		}
		return err
	}
	return nil
}
