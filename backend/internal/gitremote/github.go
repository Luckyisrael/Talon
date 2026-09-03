package gitremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The GitHub endpoints this integration talks to. Kept as vars, not consts, so
// tests could point them at a local stub if we ever add HTTP-level tests.
var (
	githubTokenURL = "https://github.com/login/oauth/access_token"
	githubAPIBase  = "https://api.github.com"
)

// oauthScopes is the minimum the push flow needs: create repositories and push
// to them. "repo" covers private repos too; a user pushing their own project
// generally wants that choice available.
const oauthScopes = "repo"

// httpClient is shared and time-bounded. GitHub is a remote dependency; a hung
// connection must not hang a request handler indefinitely.
var httpClient = &http.Client{Timeout: 20 * time.Second}

// AuthorizeURL builds the URL the browser is sent to so the user can approve the
// OAuth app. state ties the redirect back to the user who started it.
func (c Config) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.CallbackURL)
	q.Set("scope", oauthScopes)
	q.Set("state", state)
	// allow_signup left default; the user is approving an existing account.
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}

// ExchangeCode trades an authorization code for an access token. GitHub returns
// the token in a JSON body when asked with an Accept: application/json header.
func (c Config) ExchangeCode(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.CallbackURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github token exchange failed: status %d", resp.StatusCode)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("github token exchange: decoding response: %w", err)
	}
	if out.Error != "" {
		// The description can echo back inputs; the error code is safe and enough.
		return "", fmt.Errorf("github token exchange rejected: %s", out.Error)
	}
	if out.AccessToken == "" {
		return "", errors.New("github token exchange returned an empty token")
	}
	return out.AccessToken, nil
}

// GitHubUser is the slice of the authenticated user we keep: the login is the
// repository owner for pushes, and we surface it in the UI so the user can see
// which account they connected.
type GitHubUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// FetchUser resolves the account behind a token. Used right after the exchange
// to record the login, and it doubles as a token liveness check.
func (c Config) FetchUser(ctx context.Context, token string) (GitHubUser, error) {
	var user GitHubUser
	if err := c.apiJSON(ctx, token, http.MethodGet, "/user", nil, &user); err != nil {
		return GitHubUser{}, err
	}
	if user.Login == "" {
		return GitHubUser{}, errors.New("github: authenticated user has no login")
	}
	return user, nil
}

// Repo is the subset of a repository we act on. CloneURL is the HTTPS URL we
// push to (with a token spliced in for the single push, never stored).
type Repo struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
	HTMLURL  string `json:"html_url"`
	Private  bool   `json:"private"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// EnsureRepo returns the named repository under the authenticated account,
// creating it if it does not yet exist. It is idempotent: a second push to the
// same project finds the repo already there and returns it unchanged.
func (c Config) EnsureRepo(ctx context.Context, token, name string, private bool) (Repo, error) {
	login, err := c.FetchUser(ctx, token)
	if err != nil {
		return Repo{}, err
	}

	// Look first. A 200 means it exists and we push to it; a 404 means we create.
	var existing Repo
	err = c.apiJSON(ctx, token, http.MethodGet, "/repos/"+login.Login+"/"+name, nil, &existing)
	if err == nil {
		return existing, nil
	}
	var ae apiError
	if !errors.As(err, &ae) || ae.status != http.StatusNotFound {
		return Repo{}, err
	}

	// Create under the authenticated user's account.
	payload := map[string]any{
		"name":     name,
		"private":  private,
		"auto_init": false,
	}
	var created Repo
	if err := c.apiJSON(ctx, token, http.MethodPost, "/user/repos", payload, &created); err != nil {
		return Repo{}, err
	}
	return created, nil
}

// apiError carries the HTTP status so callers can distinguish "not found" (a
// normal branch in EnsureRepo) from a real failure, without parsing strings.
type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string { return fmt.Sprintf("github api: %s (status %d)", e.msg, e.status) }

// apiJSON performs one authenticated GitHub REST call, encoding body as JSON
// when non-nil and decoding a 2xx response into out when non-nil.
func (c Config) apiJSON(ctx context.Context, token, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, githubAPIBase+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := githubErrorMessage(raw)
		return apiError{status: resp.StatusCode, msg: msg}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("github api: decoding %s response: %w", path, err)
		}
	}
	return nil
}

// githubErrorMessage pulls the human-readable message out of a GitHub error
// body without leaking the whole payload into logs.
func githubErrorMessage(raw []byte) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return "request failed"
}
