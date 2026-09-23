package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// W12.1 phase gates, end to end through the real child build (childBuildFor)
// and the real worktree overlay:
//
//  1. readonly posture (default): a mutating spawn refuses before anything
//     runs — the flag-off path stays byte-identical to today;
//  2. worktree posture over a non-git workspace: structured CHILD_WORKTREE
//     refusal from the runner (git reality, not a panic);
//  3. two mutating children in parallel produce disjoint diffs with no lost
//     update (the parallel write fixture);
//  4. worktree cleanup on child dispose — registration and directory.

func w12NeedGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func w12Git(t *testing.T, dir string, args ...string) string {
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

// w12Repo builds a committed workspace: shared.txt with 12 identified
// lines (child A edits line 2, child B edits line 11 — disjoint hunk
// contexts, so both merges apply deterministically).
func w12Repo(t *testing.T) string {
	t.Helper()
	w12NeedGit(t)
	dir := t.TempDir()
	w12Git(t, dir, "init", "-q")
	var sb strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&sb, "original line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	w12Git(t, dir, "add", "-A")
	w12Git(t, dir, "commit", "-qm", "baseline")
	return dir
}

// w12StubLoop is the child's "work": parse the task prompt
// ("<op> ...; <op> ...") and write through the build session's own FS —
// which for a mutating child is rooted at the private worktree.
func w12StubLoop(_ context.Context, _ *orchestrator.SessionState, bs *buildSession,
	_ []ai.ToolDefinition, history []ai.Message, _ string) (*orchestrator.BuildOutcome, error) {
	if len(history) == 0 {
		return nil, fmt.Errorf("no task prompt")
	}
	var task string
	for _, b := range history[len(history)-1].Blocks {
		if b.Type == "text" {
			task += b.Text
		}
	}
	for _, part := range strings.Split(task, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		var err error
		switch fields[0] {
		case "create": // create <name> <content>
			if len(fields) != 3 {
				return nil, fmt.Errorf("bad create: %q", part)
			}
			err = bs.fs.Write(fields[1], fields[2]+"\n")
		case "edit": // edit <name> <line> <text>
			if len(fields) != 4 {
				return nil, fmt.Errorf("bad edit: %q", part)
			}
			n, ferr := strconv.Atoi(fields[2])
			if ferr != nil {
				return nil, ferr
			}
			cur, _, rerr := bs.fs.Read(fields[1]) // second return is "truncated", not "exists"
			if rerr != nil {
				return nil, fmt.Errorf("read %s: %w", fields[1], rerr)
			}
			lines := strings.Split(strings.TrimRight(cur, "\n"), "\n")
			if n < 1 || n > len(lines) {
				return nil, fmt.Errorf("line %d out of range 1..%d", n, len(lines))
			}
			lines[n-1] = fields[3]
			err = bs.fs.Write(fields[1], strings.Join(lines, "\n")+"\n")
		default:
			return nil, fmt.Errorf("unknown op %q", fields[0])
		}
		if err != nil {
			return nil, err
		}
	}
	return &orchestrator.BuildOutcome{Rounds: 1, FinalText: "did the work"}, nil
}

// w12Fixture wires a mutating-capable service over ws exactly as
// newBuildSession does (childBuildFor + the env-read cap config), with the
// loop stubbed to real file work.
func w12Fixture(t *testing.T, ws string) *subagent.Service {
	t.Helper()
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("w12@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "W12", "v1", "w12", "com.talon.w12", "com.talon.w12", "w12app")
	if err != nil {
		t.Fatal(err)
	}
	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)
	build := childBuildFor(h, proj.ID, "", io.Discard)
	runner := &childRunner{
		workspace: ws,
		build:     build,
		newSession: func(workspace string) *orchestrator.SessionState {
			return orchestrator.NewSession(0, proj.ID, workspace, events.NewSessionEmitter(0, 64))
		},
		runLoop: w12StubLoop,
	}
	svc := subagent.NewService(runner, 2)
	svc.SetWorktreeIsolation(childIsolationWorktree())
	svc.SetSpendCap(childSpendCapFromEnv())
	svc.SetWall(childWallFromEnv())
	return svc
}

func w12TempLeftovers(t *testing.T, before map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "talon-child-worktree-") && !before[e.Name()] {
			t.Errorf("worktree directory leaked after dispose: %s", e.Name())
		}
	}
}

func w12TempSnapshot() map[string]bool {
	out := map[string]bool{}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "talon-child-worktree-") {
			out[e.Name()] = true
		}
	}
	return out
}

// TestPhaseW12MutatingChildrenIsolated: clauses 1 and 3 of the W12 eval
// gate plus the runner's non-git refusal.
func TestPhaseW12MutatingChildrenIsolated(t *testing.T) {
	t.Run("readonly posture refuses the mutating spawn", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "") // default = readonly
		ws := w12Repo(t)
		svc := w12Fixture(t, ws)
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "refused", Prompt: "create nope.txt x", Mutating: true,
		})
		if !errors.Is(err, subagent.ErrIsolation) {
			t.Fatalf("want ErrIsolation, got %v", err)
		}
		if !res.Errored {
			t.Errorf("refusal must be an errored result: %+v", res)
		}
		if _, statErr := os.Stat(filepath.Join(ws, "nope.txt")); !os.IsNotExist(statErr) {
			t.Errorf("refused child touched the workspace: %v", statErr)
		}
	})

	t.Run("worktree posture over a non-git workspace refuses structurally", func(t *testing.T) {
		t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
		w12NeedGit(t)
		plain := t.TempDir() // never git-init'd
		svc := w12Fixture(t, plain)
		res, err := svc.Spawn(context.Background(), subagent.Spec{
			Label: "non-git", Prompt: "create nope.txt x", Mutating: true,
		})
		if err != nil {
			t.Fatalf("runner refusal is a result, not a Go error: %v", err)
		}
		if !res.Errored || !strings.Contains(res.Err, "CHILD_WORKTREE") ||
			!strings.Contains(res.Err, "not a git repository") {
			t.Errorf("want structured CHILD_WORKTREE refusal, got %+v", res)
		}
	})

	t.Run("two mutating children: disjoint diffs, no lost update, clean dispose",
		func(t *testing.T) {
			t.Setenv("TALONSTUDIO_CHILD_ISOLATION", "worktree")
			ws := w12Repo(t)
			before := w12TempSnapshot()
			svc := w12Fixture(t, ws)

			specs := []subagent.Spec{
				{Label: "child-alpha", Mutating: true,
					Prompt: "create alpha.txt from-alpha ; edit shared.txt 2 line-two-from-A"},
				{Label: "child-beta", Mutating: true,
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

			// Clause 1: disjoint diffs both landed; the shared file carries
			// BOTH updates — no lost update.
			if b, err := os.ReadFile(filepath.Join(ws, "alpha.txt")); err != nil || string(b) != "from-alpha\n" {
				t.Errorf("alpha.txt: %q err=%v", b, err)
			}
			if b, err := os.ReadFile(filepath.Join(ws, "beta.txt")); err != nil || string(b) != "from-beta\n" {
				t.Errorf("beta.txt: %q err=%v", b, err)
			}
			shared, err := os.ReadFile(filepath.Join(ws, "shared.txt"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(string(shared), "\n"), "\n")
			if len(lines) != 12 {
				t.Fatalf("shared.txt line count = %d, want 12:\n%s", len(lines), shared)
			}
			if lines[1] != "line-two-from-A" {
				t.Errorf("line 2 lost: %q", lines[1])
			}
			if lines[10] != "line-eleven-from-B" {
				t.Errorf("line 11 lost: %q", lines[10])
			}
			for i, l := range lines {
				if i == 1 || i == 10 {
					continue
				}
				if want := fmt.Sprintf("original line %d", i+1); l != want {
					t.Errorf("untouched line %d = %q, want %q", i+1, l, want)
				}
			}

			// Clause 4: worktree cleanup on dispose — no registration, no dir.
			out := w12Git(t, ws, "worktree", "list")
			if wl := strings.Split(strings.TrimSpace(out), "\n"); len(wl) != 1 || !strings.HasPrefix(wl[0], ws) {
				t.Errorf("stale worktree registrations:\n%s", out)
			}
			w12TempLeftovers(t, before)

			// Both children's durable records reflect the successful run.
			for _, k := range svc.List() {
				if k.State != "done" {
					t.Errorf("child %s state=%s", k.Label, k.State)
				}
			}
		})
}
