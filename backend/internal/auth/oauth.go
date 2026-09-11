// Package auth — OAuth sign-in. The email/password path is gone: identity
// comes from GitHub and Google. The flow follows the gitremote pattern:
// env-gated config (dark until credentials exist), single-use state tokens,
// a public callback route, and a code-exchange finish so the session token
// never rides a URL.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Provider names the identity providers the studio supports.
type Provider string

const (
	ProviderGitHub Provider = "github"
	ProviderGoogle Provider = "google"
)

// OAuthConfig is one provider's app credentials. Dark until both fields are
// set — the same law as every other integration in this codebase.
//
// Endpoint overrides exist for one reason: the E2E flow and these tests run
// against a stub provider, and the override is how production code is aimed
// at test infrastructure without a code path forking. Empty means the real
// provider endpoints.
type OAuthConfig struct {
	Provider     Provider
	ClientID     string
	ClientSecret string
	CallbackURL  string // absolute; derived from the public origin at startup

	// Test/E2E overrides — empty in production.
	AuthorizeEndpoint string
	TokenEndpoint     string
	ProfileEndpoint   string
}

// OAuthProviderConfig loads both providers from the environment:
//
//	AUTH_GITHUB_CLIENT_ID / AUTH_GITHUB_CLIENT_SECRET
//	AUTH_GOOGLE_CLIENT_ID / AUTH_GOOGLE_CLIENT_SECRET
//	AUTH_CALLBACK_BASE — origin for callback URLs (default http://localhost:8080)
func OAuthProviderConfig(base string) map[Provider]OAuthConfig {
	if base == "" {
		base = "http://localhost:8080"
	}
	base = strings.TrimSuffix(base, "/")
	m := map[Provider]OAuthConfig{
		ProviderGitHub: {
			Provider:     ProviderGitHub,
			ClientID:     strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(os.Getenv("AUTH_GITHUB_CLIENT_SECRET")),
			CallbackURL:  base + "/v1/auth/github/callback",
			// E2E/test overrides: point the production flow at a stub.
			AuthorizeEndpoint: strings.TrimSpace(os.Getenv("AUTH_GITHUB_AUTHORIZE_ENDPOINT")),
			TokenEndpoint:      strings.TrimSpace(os.Getenv("AUTH_GITHUB_TOKEN_ENDPOINT")),
			ProfileEndpoint:    strings.TrimSpace(os.Getenv("AUTH_GITHUB_PROFILE_ENDPOINT")),
		},
		ProviderGoogle: {
			Provider:     ProviderGoogle,
			ClientID:     strings.TrimSpace(os.Getenv("AUTH_GOOGLE_CLIENT_ID")),
			ClientSecret: strings.TrimSpace(os.Getenv("AUTH_GOOGLE_CLIENT_SECRET")),
			CallbackURL:  base + "/v1/auth/google/callback",
			// E2E/test overrides: point the production flow at a stub.
			AuthorizeEndpoint: strings.TrimSpace(os.Getenv("AUTH_GOOGLE_AUTHORIZE_ENDPOINT")),
			TokenEndpoint:      strings.TrimSpace(os.Getenv("AUTH_GOOGLE_TOKEN_ENDPOINT")),
			ProfileEndpoint:    strings.TrimSpace(os.Getenv("AUTH_GOOGLE_PROFILE_ENDPOINT")),
		},
	}
	for p, c := range m {
		if c.ClientID == "" || c.ClientSecret == "" {
			delete(m, p) // dark until fully configured
		}
	}
	return m
}

// AuthorizeURL builds the consent-screen URL for the provider.
func (c OAuthConfig) AuthorizeURL(state string) string {
	q := url.Values{}
	switch c.Provider {
	case ProviderGitHub:
		q.Set("client_id", c.ClientID)
		q.Set("redirect_uri", c.CallbackURL)
		q.Set("scope", "read:user user:email")
		q.Set("state", state)
		return c.endpoint("https://github.com/login/oauth/authorize", "authorize") + "?" + q.Encode()
	case ProviderGoogle:
		q.Set("client_id", c.ClientID)
		q.Set("redirect_uri", c.CallbackURL)
		q.Set("response_type", "code")
		q.Set("scope", "openid email profile")
		q.Set("access_type", "online")
		q.Set("state", state)
		return c.endpoint("https://accounts.google.com/o/oauth2/v2/auth", "authorize") + "?" + q.Encode()
	}
	return ""
}


// endpoint returns the override for kind ("authorize"/"token"/"profile") when
// one is configured, else the production default.
func (c OAuthConfig) endpoint(def, kind string) string {
	switch kind {
	case "authorize":
		if c.AuthorizeEndpoint != "" {
			return c.AuthorizeEndpoint
		}
	case "token":
		if c.TokenEndpoint != "" {
			return c.TokenEndpoint
		}
	case "profile":
		if c.ProfileEndpoint != "" {
			return c.ProfileEndpoint
		}
	}
	return def
}

// oauthTokenResponse is the shape both providers' token endpoints return.
type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
}

// ExchangeCode trades the consent code for the provider access token.
func (c OAuthConfig) ExchangeCode(ctx context.Context, code string) (string, error) {
	var tokenURL string
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.CallbackURL)
	form.Set("grant_type", "authorization_code")
	switch c.Provider {
	case ProviderGitHub:
		tokenURL = c.endpoint("https://github.com/login/oauth/access_token", "token")
		form.Del("grant_type")
	case ProviderGoogle:
		tokenURL = c.endpoint("https://oauth2.googleapis.com/token", "token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// GitHub answers JSON only when asked; Google always does.
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("provider unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange failed (HTTP %d)", resp.StatusCode)
	}
	var tr oauthTokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("token response unreadable: %w", err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("provider error: %s", tr.Error)
	}
	if tr.AccessToken == "" {
		return "", errors.New("provider returned no access token")
	}
	return tr.AccessToken, nil
}

// ProviderIdentity is the normalized profile every provider resolves to.
type ProviderIdentity struct {
	ProviderID string
	Email      string
	Name       string
	AvatarURL  string
}

// FetchProfile resolves the access token to a stable identity.
func (c OAuthConfig) FetchProfile(ctx context.Context, token string) (ProviderIdentity, error) {
	switch c.Provider {
	case ProviderGitHub:
		return c.fetchGitHub(ctx, token)
	case ProviderGoogle:
		return c.fetchGoogle(ctx, token)
	}
	return ProviderIdentity{}, fmt.Errorf("unknown provider %q", c.Provider)
}

func (c OAuthConfig) fetchGitHub(ctx context.Context, token string) (ProviderIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("https://api.github.com/user", "profile"), nil)
	if err != nil {
		return ProviderIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	var u struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := oauthJSON(ctx, req, &u); err != nil {
		return ProviderIdentity{}, err
	}
	email := u.Email
	if email == "" {
		// Private primary address: /user/emails needs user:email, which we asked for.
		email = c.githubPrimaryEmail(ctx, token)
	}
	if email == "" {
		// No email exposed: a deterministic synthetic address keeps the NOT NULL
		// users.email honest without inventing a real-looking one.
		email = fmt.Sprintf("%d@users.noreply.github.com", u.ID)
	}
	return ProviderIdentity{
		ProviderID: fmt.Sprintf("%d", u.ID),
		Email:      email,
		Name:       firstNonEmpty(u.Name, u.Login),
		AvatarURL:  u.AvatarURL,
	}, nil
}

func (c OAuthConfig) githubPrimaryEmail(ctx context.Context, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	var list []struct {
		Email   string `json:"email"`
		Primary bool   `json:"primary"`
	}
	if err := oauthJSON(ctx, req, &list); err != nil {
		return ""
	}
	for _, e := range list {
		if e.Primary {
			return e.Email
		}
	}
	if len(list) > 0 {
		return list[0].Email
	}
	return ""
}

func (c OAuthConfig) fetchGoogle(ctx context.Context, token string) (ProviderIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("https://www.googleapis.com/oauth2/v2/userinfo", "profile"), nil)
	if err != nil {
		return ProviderIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var u struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := oauthJSON(ctx, req, &u); err != nil {
		return ProviderIdentity{}, err
	}
	if u.ID == "" || u.Email == "" {
		return ProviderIdentity{}, errors.New("google profile incomplete (needs openid email profile scopes)")
	}
	return ProviderIdentity{
		ProviderID: u.ID,
		Email:      u.Email,
		Name:       u.Name,
		AvatarURL:  u.Picture,
	}, nil
}

// oauthHTTPClient bounds every provider call. The provider is a remote
// dependency; a hung consent exchange must not hang the callback handler.
var oauthHTTPClient = &http.Client{Timeout: 20 * time.Second}

func oauthJSON(ctx context.Context, req *http.Request, out interface{}) error {
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("provider API HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
