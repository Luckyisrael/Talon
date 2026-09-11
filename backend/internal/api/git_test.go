package api

import (
	"testing"

	"github.com/luckyisrael/talon/backend/internal/store"
)

func TestIsPaidTier(t *testing.T) {
	paid := []string{"pro", "Pro", "team", "enterprise", "unlimited", " PRO "}
	for _, tier := range paid {
		if !isPaidTier(tier) {
			t.Errorf("isPaidTier(%q) = false, want true", tier)
		}
	}
	free := []string{"free", "", "nonsense", "basic"}
	for _, tier := range free {
		if isPaidTier(tier) {
			t.Errorf("isPaidTier(%q) = true, want false", tier)
		}
	}
}

func TestRepoName(t *testing.T) {
	cases := []struct {
		name    string
		project store.Project
		want    string
	}{
		{"slug preferred", store.Project{Slug: "cool-app", Name: "Cool App"}, "cool-app"},
		{"name fallback with spaces", store.Project{Name: "My Great App"}, "my-great-app"},
		{"strips illegal chars", store.Project{Name: "Hello, World! (v2)"}, "hello-world-v2"},
		{"id fallback when empty", store.Project{ID: 42}, "talon-project-42"},
		{"trims leading/trailing separators", store.Project{Slug: "--edge--"}, "edge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoName(&tc.project); got != tc.want {
				t.Errorf("repoName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRepoNameCapsLength(t *testing.T) {
	long := ""
	for i := 0; i < 200; i++ {
		long += "a"
	}
	if got := repoName(&store.Project{Slug: long}); len(got) != 100 {
		t.Errorf("repoName length = %d, want 100", len(got))
	}
}

func TestOAuthStateStore(t *testing.T) {
	s := newOAuthStateStore()

	state, err := newOAuthState()
	if err != nil {
		t.Fatalf("newOAuthState: %v", err)
	}
	// Two states must differ — they are the CSRF/identity binding on the public
	// callback, so a predictable value would be a real vulnerability.
	state2, _ := newOAuthState()
	if state == state2 || len(state) != 64 {
		t.Fatalf("states not distinct/expected length: %q %q", state, state2)
	}

	s.put(state, 7)

	// take returns the bound user exactly once.
	if uid, ok := s.take(state); !ok || uid != 7 {
		t.Fatalf("take = (%d, %v), want (7, true)", uid, ok)
	}
	// A replay of the same state fails — it was single-use.
	if _, ok := s.take(state); ok {
		t.Fatal("state was reusable; it must be single-use")
	}
	// An unknown state fails.
	if _, ok := s.take("never-issued"); ok {
		t.Fatal("unknown state resolved to a user")
	}
}
