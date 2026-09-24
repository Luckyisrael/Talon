package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// The §7 diff surface reads the workspace at <workspaceDir>/<projectID> —
// the provision convention (workspace.go), not the display slug. This pins
// that contract: a diff for a project whose workspace exists must come back
// 200 with the real diff, never "could not be read".
func TestGitDiffReadsNumericWorkspaceDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	// A workspace the way the provisioner makes it: numeric id directory
	// with a committed baseline and one uncommitted change.
	ws := filepath.Join(dir, "0") // renamed to the project's real id below
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(ws, "App.tsx"), []byte("export const hello = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "baseline")
	// A modification to a tracked file — `git diff HEAD` shows tracked
	// changes only; untracked files never appear.
	if err := os.WriteFile(filepath.Join(ws, "App.tsx"), []byte("export const hello = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A store with a project whose slug deliberately differs from its id —
	// the old bug joined the slug and always failed.
	dbPath := filepath.Join(dir, "t.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("diff@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Wallet App", "v1", "wallet-app-slug", "com.talon.wallet", "com.talon.wallet", "walletapp")
	if err != nil {
		t.Fatal(err)
	}
	// The workspace directory must match the project's real id, whatever it is.
	if err := os.Rename(ws, filepath.Join(dir, strconv.FormatInt(proj.ID, 10))); err != nil {
		t.Fatal(err)
	}

	h := &PromptHandler{store: st, workspaceDir: dir}
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/1/diff", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserIDKey, user.ID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(proj.ID, 10))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.GitDiff(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("GitDiff status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Diff string `json:"diff"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v", err)
	}
	if body.Note != "" {
		t.Fatalf("note = %q, want empty (workspace is a real git repo)", body.Note)
	}
	if !strings.Contains(body.Diff, "App.tsx") || !strings.Contains(body.Diff, "hello") {
		t.Fatalf("diff missing the uncommitted change; got: %q", body.Diff)
	}
}

// The §7 panel renders diff ∩ this turn's touched set — and a build turn's
// most common result is a CREATED file, which plain `git diff HEAD` never
// shows (untracked files are invisible to it). The row-45 live E2E found
// the panel unable to list the turn's own new file on any path, restored or
// live. The handler must diff through a throwaway index that intent-adds the
// worktree (`read-tree HEAD` + `add -N .`), so new files come back as
// `+++ b/…` sections while the workspace's real index — the one the agent's
// git_status/git_diff tools read — stays untouched.
//
// Failure modes pinned here (rule 1):
//
//	F1 the untracked created file appears in the diff   [absent pre-fix → RED]
//	F2 polarity: the tracked modification still appears        [green both ways]
//	F3 polarity: the real index is not mutated (file stays `??`) [green both]
func TestGitDiffIncludesUntrackedFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	ws := filepath.Join(dir, "0")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	write("App.tsx", "export const hello = 1;\n")
	run("add", "-A")
	run("commit", "-m", "template baseline")
	// A tracked modification (F2) and an untracked creation (F1) — the exact
	// shape a build turn leaves behind.
	write("App.tsx", "export const hello = 2;\n")
	write("ROW45_LIVE.md", "# row45 live\n")

	dbPath := filepath.Join(dir, "t.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("diff-untracked@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Row45 Live", "v1", "row45-live", "com.talon.row45", "com.talon.row45", "row45live")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ws, filepath.Join(dir, strconv.FormatInt(proj.ID, 10))); err != nil {
		t.Fatal(err)
	}
	realWS := filepath.Join(dir, strconv.FormatInt(proj.ID, 10))

	h := &PromptHandler{store: st, workspaceDir: dir}
	r := httptest.NewRequest(http.MethodGet, "/v1/projects/1/diff", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserIDKey, user.ID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(proj.ID, 10))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.GitDiff(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("GitDiff status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Diff string `json:"diff"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v", err)
	}
	if body.Note != "" {
		t.Fatalf("note = %q, want empty (workspace is a real git repo)", body.Note)
	}
	// F1: the created file must be in the diff as an added-file section.
	if !strings.Contains(body.Diff, "+++ b/ROW45_LIVE.md") {
		t.Fatalf("F1 — untracked created file missing from diff (plain `git diff HEAD` blind spot); got: %q", body.Diff)
	}
	// F2: the tracked modification must still be there.
	if !strings.Contains(body.Diff, "hello = 2") {
		t.Fatalf("F2 polarity — tracked modification missing from diff; got: %q", body.Diff)
	}
	// F3: the workspace's real index must be untouched — the created file
	// still untracked, nothing staged.
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = realWS
	statusOut, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(string(statusOut), "?? ROW45_LIVE.md") {
		t.Fatalf("F3 polarity — real index was mutated (created file no longer untracked): %s", statusOut)
	}
	if strings.Contains(string(statusOut), "A  ROW45_LIVE.md") {
		t.Fatalf("F3 polarity — the throwaway index leaked into the real one: %s", statusOut)
	}
}
