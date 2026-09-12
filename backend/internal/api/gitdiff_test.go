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
