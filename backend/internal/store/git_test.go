package store

import (
	"testing"

	_ "modernc.org/sqlite"
)

func TestGitCredentialRoundTrip(t *testing.T) {
	s := newTestStore(t)
	u, err := s.CreateUser("git@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Nothing connected yet.
	if c, err := s.GetGitCredential(u.ID, "github"); err != nil || c != nil {
		t.Fatalf("expected (nil, nil) before connecting, got (%v, %v)", c, err)
	}

	if err := s.UpsertGitCredential(u.ID, "github", "octocat", 583231, "sealed-token-1", "repo"); err != nil {
		t.Fatalf("upsert credential: %v", err)
	}

	got, err := s.GetGitCredential(u.ID, "github")
	if err != nil || got == nil {
		t.Fatalf("get credential: (%v, %v)", got, err)
	}
	if got.ProviderLogin != "octocat" || got.ProviderUserID != 583231 || got.AccessToken != "sealed-token-1" || got.Scopes != "repo" {
		t.Fatalf("stored credential mismatch: %+v", got)
	}

	// Reconnecting overwrites in place — one row per (user, provider), not two.
	if err := s.UpsertGitCredential(u.ID, "github", "octocat-renamed", 583231, "sealed-token-2", "repo"); err != nil {
		t.Fatalf("re-upsert credential: %v", err)
	}
	got, err = s.GetGitCredential(u.ID, "github")
	if err != nil || got == nil {
		t.Fatalf("get after re-upsert: (%v, %v)", got, err)
	}
	if got.ProviderLogin != "octocat-renamed" || got.AccessToken != "sealed-token-2" {
		t.Fatalf("re-upsert did not overwrite: %+v", got)
	}

	// Disconnect, then a second disconnect is not an error.
	if err := s.DeleteGitCredential(u.ID, "github"); err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	if c, err := s.GetGitCredential(u.ID, "github"); err != nil || c != nil {
		t.Fatalf("expected (nil, nil) after delete, got (%v, %v)", c, err)
	}
	if err := s.DeleteGitCredential(u.ID, "github"); err != nil {
		t.Fatalf("second delete should be a no-op, got: %v", err)
	}
}

func TestGitRemoteRoundTrip(t *testing.T) {
	s := newTestStore(t)
	pid := seedProject(t, s)

	// A project that has never been pushed has no remote.
	if r, err := s.GetGitRemote(pid); err != nil || r != nil {
		t.Fatalf("expected (nil, nil) before push, got (%v, %v)", r, err)
	}

	if err := s.UpsertGitRemote(GitRemote{
		ProjectID:     pid,
		Provider:      "github",
		Owner:         "octocat",
		Repo:          "my-app",
		CloneURL:      "https://github.com/octocat/my-app.git",
		HTMLURL:       "https://github.com/octocat/my-app",
		DefaultBranch: "main",
		Private:       true,
	}); err != nil {
		t.Fatalf("upsert remote: %v", err)
	}

	got, err := s.GetGitRemote(pid)
	if err != nil || got == nil {
		t.Fatalf("get remote: (%v, %v)", got, err)
	}
	if got.Owner != "octocat" || got.Repo != "my-app" || !got.Private || got.DefaultBranch != "main" {
		t.Fatalf("stored remote mismatch: %+v", got)
	}

	// Re-targeting a project overwrites the single mapping; the private flag flips.
	if err := s.UpsertGitRemote(GitRemote{
		ProjectID: pid,
		Provider:  "github",
		Owner:     "octocat",
		Repo:      "renamed-app",
		CloneURL:  "https://github.com/octocat/renamed-app.git",
		Private:   false,
	}); err != nil {
		t.Fatalf("re-upsert remote: %v", err)
	}
	got, err = s.GetGitRemote(pid)
	if err != nil || got == nil {
		t.Fatalf("get after re-upsert: (%v, %v)", got, err)
	}
	if got.Repo != "renamed-app" || got.Private {
		t.Fatalf("re-upsert did not overwrite: %+v", got)
	}
	// DefaultBranch defaults to main when left empty.
	if got.DefaultBranch != "main" {
		t.Fatalf("expected default branch 'main', got %q", got.DefaultBranch)
	}
}
