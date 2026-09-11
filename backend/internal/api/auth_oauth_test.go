package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/store"
)

// The OAuth sign-in flow is tested against a stub provider: a local
// httptest server standing in for github.com / google.com. The config's
// endpoint overrides (AuthorizeEndpoint/TokenEndpoint/ProfileEndpoint) aim
// production code at the stub, so the flow under test is byte-identical to
// the one that runs live — only the host differs.

// stubOAuth stands in for a provider: it serves the consent redirect, the
// token exchange, and the profile fetch.
type stubOAuth struct {
	t         *testing.T
	server    *httptest.Server
	profile   map[string]interface{}
	tokenFail bool
}

func newStubOAuth(t *testing.T, profile map[string]interface{}) *stubOAuth {
	t.Helper()
	s := &stubOAuth{t: t, profile: profile}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// A real provider would render a consent page; the stub redirects
		// straight back to the callback with a fixed code, exactly the shape
		// the real provider's "approve" click produces.
		q := url.Values{}
		q.Set("code", "stub-consent-code")
		q.Set("state", r.URL.Query().Get("state"))
		http.Redirect(w, r, "/callback?"+q.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if s.tokenFail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("code") != "stub-consent-code" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "stub-access-token"})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stub-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(s.profile)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

// newOAuthHarness builds the handler over a real store, with the stub as the
// only configured provider.
func newOAuthHarness(t *testing.T, stub *stubOAuth) (*OAuthAuthHandler, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := auth.OAuthConfig{
		Provider:     auth.ProviderGitHub,
		ClientID:     "stub-client-id",
		ClientSecret: "stub-client-secret",
		CallbackURL:  "http://studio.test/v1/auth/github/callback",
	}
	if stub != nil {
		cfg.AuthorizeEndpoint = stub.server.URL + "/authorize"
		cfg.TokenEndpoint = stub.server.URL + "/token"
		cfg.ProfileEndpoint = stub.server.URL + "/profile"
	}
	providers := map[auth.Provider]auth.OAuthConfig{auth.ProviderGitHub: cfg}
	svc := auth.New()
	return NewOAuthAuthHandler(st, svc, providers), st
}

// driveFlow walks the full browser-visible flow: Start → (stub consent) →
// Callback → extract the code from the redirect → Exchange. It returns the
// exchange response body and the final HTTP status.
func driveFlow(t *testing.T, h *OAuthAuthHandler, redirect string) (int, string) {
	t.Helper()

	// 1 — Start: mints the state and 302s to the provider.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start?redirect="+url.QueryEscape(redirect), nil)
	req.SetPathValue("provider", "github")
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: HTTP %d (want 302)", rec.Code)
	}
	consentURL := rec.Header().Get("Location")

	// 2 — The stub consent immediately redirects to our callback with the
	// code + state. Follow it manually: extract the query.
	cu, err := url.Parse(consentURL)
	if err != nil {
		t.Fatalf("consent url: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_ = client // callback is served by our handler, not the stub server

	cbQuery := cu.Query()
	_ = cbQuery

	// The stub's /authorize handler 302s to /callback on the stub host —
	// but our callback route lives on the studio. Drive it directly with
	// the same query the stub produced.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?code=stub-consent-code&state="+cu.Query().Get("state"), nil)
	req2.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Callback(rec2, req2)
	if rec2.Code != http.StatusFound {
		t.Fatalf("callback: HTTP %d body=%s (want 302)", rec2.Code, rec2.Body.String())
	}
	final := rec2.Header().Get("Location")

	// 3 — Extract the session code the callback appended.
	fu, err := url.Parse(final)
	if err != nil {
		t.Fatalf("final redirect %q: %v", final, err)
	}
	code := fu.Query().Get("code")
	if code == "" {
		t.Fatalf("final redirect %q carries no code", final)
	}

	// 4 — Exchange: the one-time ticket becomes a JWT.
	body := fmt.Sprintf(`{"code":%q}`, code)
	req3 := httptest.NewRequest(http.MethodPost, "/v1/auth/exchange", strings.NewReader(body))
	rec3 := httptest.NewRecorder()
	h.Exchange(rec3, req3)
	return rec3.Code, rec3.Body.String()
}

func TestOAuthFullFlowCreatesUser(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{
		"id": 12345, "login": "octocat", "name": "Octo Cat", "email": "octo@example.com", "avatar_url": "http://ava",
	})
	h, st := newOAuthHarness(t, stub)

	code, body := driveFlow(t, h, "/dashboard")
	if code != http.StatusOK {
		t.Fatalf("exchange: HTTP %d body=%s", code, body)
	}
	var resp struct {
		Token string      `json:"token"`
		User  *store.User `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("exchange body: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("exchange returned no token")
	}
	if resp.User == nil || resp.User.Email != "octo@example.com" {
		t.Fatalf("user = %+v", resp.User)
	}

	// The provider identity must be bound: a second flow finds the same user.
	u, err := st.GetUserByProvider("github", "12345")
	if err != nil {
		t.Fatalf("GetUserByProvider: %v", err)
	}
	if u.ID != resp.User.ID {
		t.Fatalf("provider lookup id %d != exchange id %d", u.ID, resp.User.ID)
	}
}

func TestOAuthSecondSignInSameUser(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{
		"id": 12345, "login": "octocat", "name": "Octo Cat", "email": "octo@example.com",
	})
	h, st := newOAuthHarness(t, stub)

	_, body1 := driveFlow(t, h, "/")
	_, body2 := driveFlow(t, h, "/")
	var u1, u2 struct {
		User *store.User `json:"user"`
	}
	_ = json.Unmarshal([]byte(body1), &u1)
	_ = json.Unmarshal([]byte(body2), &u2)
	if u1.User == nil || u2.User == nil {
		t.Fatal("a flow produced no user")
	}
	if u1.User.ID != u2.User.ID {
		t.Fatalf("same provider account made two users: %d vs %d", u1.User.ID, u2.User.ID)
	}
	// Exactly one users row exists.
	if got := countUsers(t, st); got != 1 {
		t.Fatalf("users rows = %d (want 1)", got)
	}
}

func TestOAuthEmailCollisionLinksExistingUser(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// A pre-OAuth-era user with the same verified email.
	existing, err := st.CreateUser("octo@example.com", "legacy-hash")
	if err != nil {
		t.Fatal(err)
	}

	stub := newStubOAuth(t, map[string]interface{}{
		"id": 12345, "login": "octocat", "email": "octo@example.com",
	})
	cfg := auth.OAuthConfig{
		Provider: auth.ProviderGitHub, ClientID: "c", ClientSecret: "s",
		CallbackURL:         "http://studio.test/v1/auth/github/callback",
		AuthorizeEndpoint:   stub.server.URL + "/authorize",
		TokenEndpoint:       stub.server.URL + "/token",
		ProfileEndpoint:     stub.server.URL + "/profile",
	}
	h := NewOAuthAuthHandler(st, auth.New(), map[auth.Provider]auth.OAuthConfig{auth.ProviderGitHub: cfg})

	_, body := driveFlow(t, h, "/")
	var resp struct {
		User *store.User `json:"user"`
	}
	_ = json.Unmarshal([]byte(body), &resp)
	if resp.User == nil {
		t.Fatal("no user in exchange")
	}
	if resp.User.ID != existing.ID {
		t.Fatalf("email collision created a second account: %d vs existing %d", resp.User.ID, existing.ID)
	}
	if got := countUsers(t, st); got != 1 {
		t.Fatalf("users rows = %d (want 1 — the provider email verified the same human)", got)
	}
}

func TestOAuthMobileSchemeRedirect(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{"id": 1, "login": "u", "email": "u@example.com"})
	h, _ := newOAuthHarness(t, stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start?redirect="+url.QueryEscape("expokit://auth/callback"), nil)
	req.SetPathValue("provider", "github")
	rec := httptest.NewRecorder()
	h.Start(rec, req)

	// Drive the callback and check the final redirect targets the scheme.
	state := extractState(t, rec.Header().Get("Location"))
	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?code=stub-consent-code&state="+url.QueryEscape(state), nil)
	req2.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Callback(rec2, req2)
	final := rec2.Header().Get("Location")
	if !strings.HasPrefix(final, "expokit://auth/callback?code=") {
		t.Fatalf("mobile redirect = %q (want expokit://auth/callback?code=…)", final)
	}
}

func TestOAuthOpenRedirectBlocked(t *testing.T) {
	h, _ := newOAuthHarness(t, nil) // nil stub: start only mints state
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start?redirect="+url.QueryEscape("https://evil.example/phish"), nil)
	req.SetPathValue("provider", "github")
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	consent := rec.Header().Get("Location")
	state := extractState(t, consent)

	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?code=x&state="+url.QueryEscape(state), nil)
	req2.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Callback(rec2, req2) // exchange fails (stub absent) — but redirect must be safe
	final := rec2.Header().Get("Location")
	if strings.Contains(final, "evil.example") {
		t.Fatalf("open redirect survived: %q", final)
	}
}

func TestOAuthCodeSingleUse(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{"id": 7, "login": "u", "email": "u7@example.com"})
	h, _ := newOAuthHarness(t, stub)

	// Drive once, then replay the exact same code.
	_, body := driveFlow(t, h, "/")
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Token == "" {
		t.Fatalf("first exchange failed: %v body=%s", err, body)
	}

	// The one flaw in replaying: driveFlow mints fresh states each time, so
	// replay means exchanging an already-spent code directly. Mint a fresh
	// flow, grab its callback code, exchange it twice.
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start", nil)
	req.SetPathValue("provider", "github")
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	state := extractState(t, rec.Header().Get("Location"))
	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?code=stub-consent-code&state="+url.QueryEscape(state), nil)
	req2.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Callback(rec2, req2)
	code := extractCode(t, rec2.Header().Get("Location"))

	first := httptest.NewRequest(http.MethodPost, "/v1/auth/exchange", strings.NewReader(fmt.Sprintf(`{"code":%q}`, code)))
	recA := httptest.NewRecorder()
	h.Exchange(recA, first)
	if recA.Code != http.StatusOK {
		t.Fatalf("first use: HTTP %d", recA.Code)
	}
	second := httptest.NewRequest(http.MethodPost, "/v1/auth/exchange", strings.NewReader(fmt.Sprintf(`{"code":%q}`, code)))
	recB := httptest.NewRecorder()
	h.Exchange(recB, second)
	if recB.Code == http.StatusOK {
		t.Fatal("a spent code exchanged a second time — single-use law broken")
	}
}

func TestOAuthDarkProviderHonest(t *testing.T) {
	// No credentials in env → no providers configured → the list is empty,
	// start 404s. Honesty: a dark provider is absent, not hidden.
	t.Setenv("AUTH_GITHUB_CLIENT_ID", "")
	t.Setenv("AUTH_GITHUB_CLIENT_SECRET", "")
	t.Setenv("AUTH_GOOGLE_CLIENT_ID", "")
	t.Setenv("AUTH_GOOGLE_CLIENT_SECRET", "")
	providers := auth.OAuthProviderConfig("http://x")
	if len(providers) != 0 {
		t.Fatalf("dark env produced %d providers", len(providers))
	}

	st, err := store.New(filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := NewOAuthAuthHandler(st, auth.New(), providers)

	rec := httptest.NewRecorder()
	h.Providers(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil))
	if !strings.Contains(rec.Body.String(), `"providers":null`) && !strings.Contains(rec.Body.String(), `"providers":[]`) {
		t.Fatalf("providers list = %s", rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start", nil)
	req.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Start(rec2, req)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("dark provider start: HTTP %d (want 404)", rec2.Code)
	}
}

func TestOAuthProviderTokenFailureSurfaces(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{"id": 1, "email": "e@x"})
	stub.tokenFail = true
	h, _ := newOAuthHarness(t, stub)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/start", nil)
	req.SetPathValue("provider", "github")
	rec := httptest.NewRecorder()
	h.Start(rec, req)
	state := extractState(t, rec.Header().Get("Location"))
	req2 := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?code=stub-consent-code&state="+url.QueryEscape(state), nil)
	req2.SetPathValue("provider", "github")
	rec2 := httptest.NewRecorder()
	h.Callback(rec2, req2)
	final := rec2.Header().Get("Location")
	// The failure must be visible to the client, not a success-shaped lie.
	if !strings.Contains(final, "provider-unavailable") {
		t.Fatalf("token failure redirect = %q", final)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func extractState(t *testing.T, consentURL string) string {
	t.Helper()
	u, err := url.Parse(consentURL)
	if err != nil {
		t.Fatalf("consent url: %v", err)
	}
	s := u.Query().Get("state")
	if s == "" {
		t.Fatalf("consent url %q carries no state", consentURL)
	}
	return s
}

func extractCode(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	c := u.Query().Get("code")
	if c == "" {
		t.Fatalf("redirect %q carries no code", redirect)
	}
	return c
}

func countUsers(t *testing.T, st *store.Store) int {
	t.Helper()
	// CountUsers is a store method added for this test: one exact row is
	// the assertion, and the public surface should say it plainly.
	n, err := st.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	return n
}
