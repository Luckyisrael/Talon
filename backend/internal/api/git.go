package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/gitremote"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/workspace"
)

// gitProvider is the only provider wired today. It is a constant rather than a
// request parameter so a client cannot address a provider we do not support.
const gitProvider = "github"

// GitHandler owns the premium GitHub integration: the OAuth connect/callback
// flow, per-project remote mapping, and pushing a workspace to GitHub. It is the
// sole holder of the token cipher (via cfg), so the clear access token exists
// only inside these methods, for the moment a push runs.
type GitHandler struct {
	store        *store.Store
	cfg          gitremote.Config
	workspace    *workspace.Manager
	workspaceDir string
	states       *oauthStateStore
}

func NewGitHandler(s *store.Store, cfg gitremote.Config, ws *workspace.Manager, workspaceDir string) *GitHandler {
	return &GitHandler{
		store:        s,
		cfg:          cfg,
		workspace:    ws,
		workspaceDir: workspaceDir,
		states:       newOAuthStateStore(),
	}
}

// ── Status ──────────────────────────────────────────────────────────────────

// Status reports what the frontend needs to decide which surface to show: is the
// integration configured on the server at all, is the caller on a paid plan, and
// have they connected an account. It is safe for any signed-in user to call — a
// free user simply learns paid=false and sees the upgrade path.
func (h *GitHandler) Status(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())
	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return
	}

	resp := map[string]any{
		"configured": h.cfg.Configured(),
		"paid":       isPaidTier(user.Tier),
		"connected":  false,
	}

	cred, err := h.store.GetGitCredential(userID, gitProvider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your git connection")
		return
	}
	if cred != nil {
		resp["connected"] = true
		resp["login"] = cred.ProviderLogin
		resp["connected_at"] = cred.UpdatedAt
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── Connect (start OAuth) ─────────────────────────────────────────────────────

// Connect begins the OAuth flow. Like the billing upgrade, it returns a URL for
// the client to navigate to rather than redirecting itself, because the client
// calls it with a Bearer token that a top-level browser navigation could not
// carry. The premium gate is enforced here, server-side: a free user cannot
// obtain an authorize URL.
func (h *GitHandler) Connect(w http.ResponseWriter, r *http.Request) {
	if !h.requireConfigured(w) {
		return
	}
	userID, ok := h.requirePaid(w, r)
	if !ok {
		return
	}

	state, err := newOAuthState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the connection")
		return
	}
	h.states.put(state, userID)

	writeJSON(w, http.StatusOK, map[string]string{"url": h.cfg.AuthorizeURL(state)})
}

// ── Callback (finish OAuth) ───────────────────────────────────────────────────

// Callback completes the OAuth flow. It is PUBLIC by necessity: GitHub sends the
// user's browser here as a top-level navigation, which carries no Authorization
// header. The caller's identity is recovered from the single-use state token
// minted in Connect, not from the request's auth. On success or failure it
// redirects back to the settings page with a status the UI can render — it never
// returns JSON, because a human's browser is what lands here.
func (h *GitHandler) Callback(w http.ResponseWriter, r *http.Request) {
	redirect := func(status string) {
		// AppBaseURL is server-controlled; status is from our own fixed set.
		http.Redirect(w, r, h.cfg.AppBaseURL+"/settings?github="+status, http.StatusFound)
	}

	if !h.cfg.Configured() {
		redirect("error")
		return
	}

	// GitHub reports user-side denial as an error param; treat it as a clean
	// cancel, not a failure.
	if r.URL.Query().Get("error") != "" {
		redirect("cancelled")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		redirect("error")
		return
	}

	userID, ok := h.states.take(state)
	if !ok {
		// Unknown or expired state — cannot tie this callback to a user.
		redirect("error")
		return
	}

	token, err := h.cfg.ExchangeCode(r.Context(), code)
	if err != nil {
		log.Printf("git oauth: token exchange failed for user %d: %v", userID, err)
		redirect("error")
		return
	}

	ghUser, err := h.cfg.FetchUser(r.Context(), token)
	if err != nil {
		log.Printf("git oauth: fetching user failed for user %d: %v", userID, err)
		redirect("error")
		return
	}

	// Encrypt before it touches the database. The clear token does not outlive
	// this function.
	sealed, err := h.cfg.Cipher().Encrypt([]byte(token))
	if err != nil {
		log.Printf("git oauth: sealing token failed for user %d: %v", userID, err)
		redirect("error")
		return
	}

	if err := h.store.UpsertGitCredential(userID, gitProvider, ghUser.Login, ghUser.ID, sealed, "repo"); err != nil {
		log.Printf("git oauth: storing credential failed for user %d: %v", userID, err)
		redirect("error")
		return
	}

	redirect("connected")
}

// ── Disconnect ────────────────────────────────────────────────────────────────

// Disconnect forgets the user's stored token. It is idempotent — disconnecting
// when nothing is connected still returns 200 — and does not require a paid plan,
// so a user who downgrades can always revoke a connection they no longer use.
func (h *GitHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())
	if err := h.store.DeleteGitCredential(userID, gitProvider); err != nil {
		writeError(w, http.StatusInternalServerError, "could not disconnect")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

// ── Project remote mapping ────────────────────────────────────────────────────

// GetProjectRemote returns the repository a project is linked to, or a null
// remote when it has never been pushed. Owner-gated: a project you do not own
// reports 404, never 403, so an id cannot be probed.
func (h *GitHandler) GetProjectRemote(w http.ResponseWriter, r *http.Request) {
	project, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	remote, err := h.store.GetGitRemote(project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the project's git remote")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"remote": remote})
}

// ── Push ──────────────────────────────────────────────────────────────────────

type pushRequest struct {
	Message string `json:"message"`
	Private *bool  `json:"private"`
}

// Push commits the project's workspace and pushes it to GitHub, creating the
// repository on first push. It is gated three ways, all server-side: the
// integration must be configured, the caller must be on a paid plan, and the
// caller must own the project. The clear access token is decrypted only here and
// handed to the push subprocess, which never persists it.
func (h *GitHandler) Push(w http.ResponseWriter, r *http.Request) {
	if !h.requireConfigured(w) {
		return
	}
	// Ownership first: a non-owner must not even learn whether the feature is
	// gated for them on this project. ownedProject returns 404 for a project the
	// caller does not own.
	project, ok := h.ownedProject(w, r)
	if !ok {
		return
	}
	userID, ok := h.requirePaid(w, r)
	if !ok {
		return
	}

	var req pushRequest
	if r.Body != nil {
		// A missing/empty body is fine — all fields are optional.
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	cred, err := h.store.GetGitCredential(userID, gitProvider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your git connection")
		return
	}
	if cred == nil {
		writeError(w, http.StatusBadRequest, "connect your GitHub account first")
		return
	}

	tokenBytes, err := h.cfg.Cipher().Decrypt(cred.AccessToken)
	if err != nil {
		// A token that will not decrypt is unusable — most likely the encryption
		// key changed. Tell the user to reconnect rather than failing opaquely.
		log.Printf("git push: decrypting token failed for user %d: %v", userID, err)
		writeError(w, http.StatusBadRequest, "your GitHub connection is no longer valid; please reconnect")
		return
	}
	token := string(tokenBytes)

	// Decide the target repo: reuse the project's existing mapping if present,
	// otherwise derive a name from the project and default to private.
	existing, err := h.store.GetGitRemote(project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the project's git remote")
		return
	}
	private := true
	name := repoName(project)
	if existing != nil {
		name = existing.Repo
		private = existing.Private
	}
	if req.Private != nil {
		private = *req.Private
	}

	repo, err := h.cfg.EnsureRepo(r.Context(), token, name, private)
	if err != nil {
		log.Printf("git push: ensuring repo for project %d failed: %v", project.ID, err)
		writeError(w, http.StatusBadGateway, "could not create or find the GitHub repository")
		return
	}

	// Record the mapping now that we know the repo exists, so a later push (or a
	// failed push below) still finds the link.
	if err := h.store.UpsertGitRemote(store.GitRemote{
		ProjectID:     project.ID,
		Provider:      gitProvider,
		Owner:         repo.Owner.Login,
		Repo:          repo.Name,
		CloneURL:      repo.CloneURL,
		HTMLURL:       repo.HTMLURL,
		DefaultBranch: "main",
		Private:       repo.Private,
	}); err != nil {
		log.Printf("git push: storing remote for project %d failed: %v", project.ID, err)
		// Non-fatal to the push itself; continue.
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Update " + project.Name + " from Talon"
	}

	result, err := gitremote.Push(r.Context(), gitremote.PushOptions{
		Dir:         h.dir(project.ID),
		RepoURL:     repo.CloneURL,
		Token:       token,
		Branch:      "main",
		Message:     message,
		AuthorName:  cred.ProviderLogin,
		AuthorEmail: cred.ProviderLogin + "@users.noreply.github.com",
	})
	if err != nil {
		// The error is already scrubbed of the token by the push layer.
		log.Printf("git push: pushing project %d failed: %v", project.ID, err)
		writeError(w, http.StatusBadGateway, "the push to GitHub failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"repo":         repo.FullName,
		"html_url":     repo.HTMLURL,
		"branch":       result.Branch,
		"commit_made":  result.CommitMade,
		"nothing_todo": result.NothingToDo,
	})
}

// ── Gates and helpers ─────────────────────────────────────────────────────────

// requireConfigured blocks a route when the OAuth app is absent, so the UI's
// "not configured" state is authoritative rather than something a crafted
// request can slip past.
func (h *GitHandler) requireConfigured(w http.ResponseWriter) bool {
	if !h.cfg.Configured() {
		writeError(w, http.StatusServiceUnavailable, "the GitHub integration is not configured on this server")
		return false
	}
	return true
}

// requirePaid enforces the premium gate against the user's stored tier and
// returns their id on success. The gate is never a client claim: it reads
// users.tier through the store.
func (h *GitHandler) requirePaid(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID := GetUserID(r.Context())
	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return 0, false
	}
	if !isPaidTier(user.Tier) {
		writeError(w, http.StatusPaymentRequired, "GitHub integration is a paid feature; upgrade to use it")
		return 0, false
	}
	return userID, true
}

// ownedProject loads the {id} project and confirms the caller owns it, mirroring
// ProjectHandler.project: a non-owner (or missing) project is reported as 404 so
// an id cannot be probed.
func (h *GitHandler) ownedProject(w http.ResponseWriter, r *http.Request) (*store.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return nil, false
	}
	project, err := h.store.GetProject(id)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// dir returns a project's workspace directory, matching ProjectHandler.dir.
func (h *GitHandler) dir(projectID int64) string {
	if h.workspace != nil {
		return h.workspace.Path(projectID)
	}
	return filepath.Join(h.workspaceDir, strconv.FormatInt(projectID, 10))
}

// repoName derives a GitHub repository name for a project. The project's slug is
// already a URL-safe identifier; we fall back to sanitising the name, and to a
// stable id-based name if all else is empty. GitHub allows letters, digits, '-',
// '_', and '.'.
func repoName(project *store.Project) string {
	base := strings.TrimSpace(project.Slug)
	if base == "" {
		base = strings.TrimSpace(project.Name)
	}
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-._")
	if name == "" {
		name = "talon-project-" + strconv.FormatInt(project.ID, 10)
	}
	// GitHub caps repo names at 100 chars.
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// ── OAuth state store ─────────────────────────────────────────────────────────

// oauthStateStore ties an in-flight OAuth flow to the user who began it. The
// state token travels through GitHub and comes back on the (unauthenticated)
// callback, where it is the only thing identifying the user — so it must be
// unguessable (see newOAuthState), single-use (take deletes it), and short-lived
// (entries expire). It lives in memory: an interrupted flow across a restart just
// means the user clicks Connect again.
type oauthStateStore struct {
	mu      sync.Mutex
	entries map[string]oauthStateEntry
}

type oauthStateEntry struct {
	userID  int64
	expires time.Time
}

const oauthStateTTL = 10 * time.Minute

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{entries: make(map[string]oauthStateEntry)}
}

func (s *oauthStateStore) put(state string, userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	s.entries[state] = oauthStateEntry{userID: userID, expires: time.Now().Add(oauthStateTTL)}
}

// take returns the user for a state and removes it, so a state cannot be replayed.
func (s *oauthStateStore) take(state string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[state]
	if !ok {
		return 0, false
	}
	delete(s.entries, state)
	if time.Now().After(e.expires) {
		return 0, false
	}
	return e.userID, true
}

// sweepLocked drops expired entries opportunistically; the map never holds more
// than the last TTL window of abandoned flows. Caller holds the lock.
func (s *oauthStateStore) sweepLocked() {
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, k)
		}
	}
}

// newOAuthState returns 32 bytes of cryptographic randomness as hex — an
// unguessable, single-use token. crypto/rand is used deliberately: a predictable
// state on the public callback would let an attacker bind their GitHub account to
// another user's session.
func newOAuthState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
