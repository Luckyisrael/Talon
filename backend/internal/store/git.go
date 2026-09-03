package store

import (
	"database/sql"
	"time"
)

// This file holds persistence for the premium GitHub integration (W4). It is
// kept apart from store.go the way billing.go is: one concern per file. The
// tables it reads — user_git_credentials and project_git_remotes — are created
// in migrate().
//
// The one real secret here is the OAuth access token. It is stored ENCRYPTED
// (the caller seals it with internal/secrets before handing it to this layer)
// and the struct field carries json:"-" so it can never be serialised into an
// API response. This layer never encrypts or decrypts — it only stores and
// returns the ciphertext — keeping the key confined to the handler that needs it.

// GitCredential is a user's connected git provider account. AccessToken holds
// the ENCRYPTED token, never the clear one, and is withheld from JSON so it
// cannot leak through a handler that returns the struct.
type GitCredential struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	Provider       string    `json:"provider"`
	ProviderLogin  string    `json:"provider_login"`
	ProviderUserID int64     `json:"provider_user_id"`
	AccessToken    string    `json:"-"` // encrypted at rest; never serialised
	Scopes         string    `json:"scopes,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GitRemote maps a project to the repository it pushes to. One row per project.
type GitRemote struct {
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	Provider      string    `json:"provider"`
	Owner         string    `json:"owner"`
	Repo          string    `json:"repo"`
	CloneURL      string    `json:"clone_url"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch"`
	Private       bool      `json:"private"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const gitCredentialColumns = "id, user_id, provider, provider_login, provider_user_id, access_token, scopes, created_at, updated_at"

func scanGitCredential(row *sql.Row) (*GitCredential, error) {
	c := &GitCredential{}
	if err := row.Scan(
		&c.ID, &c.UserID, &c.Provider, &c.ProviderLogin, &c.ProviderUserID,
		&c.AccessToken, &c.Scopes, &c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return c, nil
}

// UpsertGitCredential stores (or refreshes) a user's connected account for a
// provider. One row per (user, provider): reconnecting overwrites the token and
// login in place rather than accumulating rows. encryptedToken must already be
// sealed by the caller — this layer never sees the clear token.
func (s *Store) UpsertGitCredential(userID int64, provider, login string, providerUserID int64, encryptedToken, scopes string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_git_credentials
			(user_id, provider, provider_login, provider_user_id, access_token, scopes, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id, provider) DO UPDATE SET
			provider_login = excluded.provider_login,
			provider_user_id = excluded.provider_user_id,
			access_token = excluded.access_token,
			scopes = excluded.scopes,
			updated_at = CURRENT_TIMESTAMP`,
		userID, provider, login, providerUserID, encryptedToken, scopes,
	)
	return err
}

// GetGitCredential returns the user's connected account for a provider, or
// (nil, nil) when they have not connected one. The AccessToken field is still
// encrypted; the caller decrypts it only at the point of use.
func (s *Store) GetGitCredential(userID int64, provider string) (*GitCredential, error) {
	c, err := scanGitCredential(s.db.QueryRow(
		"SELECT "+gitCredentialColumns+" FROM user_git_credentials WHERE user_id = ? AND provider = ?",
		userID, provider,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// DeleteGitCredential removes a user's connection for a provider. It is
// idempotent: disconnecting when nothing is connected is not an error.
func (s *Store) DeleteGitCredential(userID int64, provider string) error {
	_, err := s.db.Exec(
		"DELETE FROM user_git_credentials WHERE user_id = ? AND provider = ?",
		userID, provider,
	)
	return err
}

const gitRemoteColumns = "id, project_id, provider, owner, repo, clone_url, html_url, default_branch, private, created_at, updated_at"

func scanGitRemote(row *sql.Row) (*GitRemote, error) {
	r := &GitRemote{}
	var private int
	if err := row.Scan(
		&r.ID, &r.ProjectID, &r.Provider, &r.Owner, &r.Repo, &r.CloneURL,
		&r.HTMLURL, &r.DefaultBranch, &private, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	r.Private = private != 0
	return r, nil
}

// UpsertGitRemote records (or updates) the repository a project pushes to. One
// row per project (project_id UNIQUE): re-targeting a project overwrites the
// mapping rather than adding a second.
func (s *Store) UpsertGitRemote(rm GitRemote) error {
	private := 0
	if rm.Private {
		private = 1
	}
	branch := rm.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	_, err := s.db.Exec(
		`INSERT INTO project_git_remotes
			(project_id, provider, owner, repo, clone_url, html_url, default_branch, private, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(project_id) DO UPDATE SET
			provider = excluded.provider,
			owner = excluded.owner,
			repo = excluded.repo,
			clone_url = excluded.clone_url,
			html_url = excluded.html_url,
			default_branch = excluded.default_branch,
			private = excluded.private,
			updated_at = CURRENT_TIMESTAMP`,
		rm.ProjectID, rm.Provider, rm.Owner, rm.Repo, rm.CloneURL, rm.HTMLURL, branch, private,
	)
	return err
}

// GetGitRemote returns the repository mapping for a project, or (nil, nil) when
// the project has never been pushed. A nil result means "not yet linked", not an
// error.
func (s *Store) GetGitRemote(projectID int64) (*GitRemote, error) {
	r, err := scanGitRemote(s.db.QueryRow(
		"SELECT "+gitRemoteColumns+" FROM project_git_remotes WHERE project_id = ?", projectID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}
