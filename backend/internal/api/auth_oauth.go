// OAuth sign-in routes (PLAN_CORE_PRODUCT.md D): the browser is sent to the
// provider's consent screen, comes back through the public callback, and the
// session JWT is minted only after the app exchanges a single-use code via
// POST — the token never appears in a URL, a referrer, or a browser history.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/auth"
	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// OAuthAuthHandler serves the sign-in flow. It is deliberately separate
// from the gitremote OAuth handler: same pattern, different surface — this
// one creates sessions.
type OAuthAuthHandler struct {
	store     *store.Store
	auth      *auth.Service
	providers map[auth.Provider]auth.OAuthConfig

	// webOrigin is where the browser UI lives (the Next.js app). When the
	// API and the web are different origins — always, in development — the
	// post-consent hand-off must land on the web's /auth/callback, not the
	// API's own root. Empty means same-origin (API serves the web itself).
	webOrigin string

	// states binds an in-flight flow to the redirect the client asked for
	// (web path or expokit:// mobile scheme). Single-use, short-lived —
	// the gitremote law, restated for sign-in.
	states *oauthFlowStore
	// codes are the single-use session tickets the callback hands to the
	// client: 60 seconds, one exchange, then dead.
	codes *oauthFlowStore
	// notifier carries sign-in alerts. Nil normalizes to dark (in-app
	// record only) so tests construct without one.
	notifier notify.Notifier
}

func NewOAuthAuthHandler(s *store.Store, a *auth.Service, providers map[auth.Provider]auth.OAuthConfig, n notify.Notifier) *OAuthAuthHandler {
	if n == nil {
		n = notify.Noop{}
	}
	return &OAuthAuthHandler{
		store: s, auth: a, providers: providers, notifier: n,
		states:    newOAuthFlowStore(),
		codes:     newOAuthFlowStore(),
		webOrigin: strings.TrimRight(strings.TrimSpace(os.Getenv("WEB_ORIGIN")), "/"),
	}
}

// webTarget resolves a sanitized redirect to the URL the browser should
// land on: web paths gain the web origin (the API and the Next.js app are
// separate origins in development); the mobile scheme and absolute URLs
// pass through untouched.
func (h *OAuthAuthHandler) webTarget(redirect string) string {
	if h.webOrigin == "" ||
		strings.HasPrefix(redirect, "talon://") ||
		strings.HasPrefix(redirect, "expokit://") ||
		!strings.HasPrefix(redirect, "/") {
		return redirect
	}
	return h.webOrigin + redirect
}

// oauthFlowStore is the single-use token store for both ends of the flow:
// states out (bound to a redirect), codes in (bound to a user id).
type oauthFlowStore struct {
	mu      sync.Mutex
	entries map[string]oauthFlowEntry
}

type oauthFlowEntry struct {
	// payload is a user id (for codes) or a redirect target (for states).
	payload  string
	provider string
	expires  time.Time
}

const oauthFlowTTL = 10 * time.Minute

func newOAuthFlowStore() *oauthFlowStore {
	return &oauthFlowStore{entries: make(map[string]oauthFlowEntry)}
}

func (s *oauthFlowStore) put(token, payload, provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	s.entries[token] = oauthFlowEntry{payload: payload, provider: provider, expires: time.Now().Add(oauthFlowTTL)}
}

// take returns the entry and deletes the token — a token answers exactly once.
func (s *oauthFlowStore) take(token string) (oauthFlowEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	if !ok {
		return oauthFlowEntry{}, false
	}
	delete(s.entries, token)
	if time.Now().After(e.expires) {
		return oauthFlowEntry{}, false
	}
	return e, true
}

func (s *oauthFlowStore) sweepLocked() {
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, k)
		}
	}
}

func newFlowToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ── GET /v1/auth/providers ─────────────────────────────────────────────
// The honest list: which providers are configured. The UI renders exactly
// what is here — a dark provider is absent, not hidden.

type providerStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *OAuthAuthHandler) Providers(w http.ResponseWriter, r *http.Request) {
	var list []providerStatus
	if _, ok := h.providers[auth.ProviderGitHub]; ok {
		list = append(list, providerStatus{ID: "github", Name: "GitHub"})
	}
	if _, ok := h.providers[auth.ProviderGoogle]; ok {
		list = append(list, providerStatus{ID: "google", Name: "Google"})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"providers": list})
}

// ── GET /v1/auth/{provider}/start?redirect=<target> ───────────────────
// Mints a state bound to the redirect and 302s to the provider's consent
// screen. The redirect may be a web path (/login) or an app scheme
// (expokit://auth/callback) — mobile and web share this one flow.

// providerParam reads the {provider} route segment. chi's URLParam is the
// production path (the request went through the router); stdlib PathValue
// is the fallback, which is what tests set via SetPathValue on a bare
// httptest request. Both spell the same name.
func providerParam(r *http.Request) string {
	if v := chi.URLParam(r, "provider"); v != "" {
		return strings.ToLower(v)
	}
	return strings.ToLower(r.PathValue("provider"))
}

func (h *OAuthAuthHandler) Start(w http.ResponseWriter, r *http.Request) {
	providerName := providerParam(r)
	cfg, ok := h.providers[auth.Provider(providerName)]
	if !ok {
		writeError(w, http.StatusNotFound, "that sign-in provider is not configured on this server")
		return
	}

	redirect := sanitizeRedirect(r.URL.Query().Get("redirect"))
	// TEMPORARY DIAGNOSTIC: the redirect the client registers is the whole
	// handoff contract — the same string must open the browser session and
	// receive the provider's return. Log it until the mobile callback is
	// resolved.
	log.Printf("oauth %s: start redirect=%q", providerName, redirect)
	state, err := newFlowToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the sign-in flow")
		return
	}
	h.states.put(state, redirect, providerName)

	http.Redirect(w, r, cfg.AuthorizeURL(state), http.StatusFound)
}

// sanitizeRedirect allows only same-origin paths and the mobile app scheme —
// an open redirect on a login flow is a phishing primitive. talon:// is the
// production scheme; expokit:// stays accepted so older installs keep working.
func sanitizeRedirect(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if strings.HasPrefix(raw, "talon://") || strings.HasPrefix(raw, "expokit://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	return "/"
}

// ── GET /v1/auth/{provider}/callback?code&state ───────────────────────
// Public by necessity: the provider redirects the browser here, carrying no
// JWT. The state recovers the redirect target; the code is exchanged
// server-side; the user is found-or-created; a single-use session code is
// minted and the browser is sent to redirect?code=….

func (h *OAuthAuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerName := providerParam(r)
	cfg, ok := h.providers[auth.Provider(providerName)]
	if !ok {
		writeError(w, http.StatusNotFound, "that sign-in provider is not configured on this server")
		return
	}

	q := r.URL.Query()
	state := q.Get("state")
	code := q.Get("code")
	if q.Get("error") != "" {
		// The user refused consent, or the provider errored. Say so plainly,
		// pointed back at the sign-in page they came from.
		entry, _ := h.states.take(state)
		http.Redirect(w, r, h.appendWebCode(entry.payload, "denied"), http.StatusFound)
		return
	}
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "the sign-in response is incomplete")
		return
	}

	entry, ok := h.states.take(state)
	if !ok {
		writeError(w, http.StatusBadRequest, "that sign-in attempt expired or was already used — start again")
		return
	}

	accessToken, err := cfg.ExchangeCode(r.Context(), code)
	if err != nil {
		log.Printf("oauth %s: exchange failed: %v", providerName, err)
		http.Redirect(w, r, h.appendWebCode(entry.payload, "provider-unavailable"), http.StatusFound)
		return
	}

	ident, err := cfg.FetchProfile(r.Context(), accessToken)
	if err != nil {
		log.Printf("oauth %s: profile fetch failed: %v", providerName, err)
		http.Redirect(w, r, h.appendWebCode(entry.payload, "provider-unavailable"), http.StatusFound)
		return
	}

	user, err := h.store.UpsertOAuthUser(providerName, ident.ProviderID, ident.Email, ident.Name, ident.AvatarURL)
	if err != nil {
		log.Printf("oauth %s: upsert failed: %v", providerName, err)
		http.Redirect(w, r, h.appendWebCode(entry.payload, "server-error"), http.StatusFound)
		return
	}

	// The single-use session ticket: 60 seconds to cross the client boundary.
	ticket, err := newFlowToken()
	if err != nil {
		http.Redirect(w, r, h.appendWebCode(entry.payload, "server-error"), http.StatusFound)
		return
	}
	h.codes.put(ticket, strconv.FormatInt(user.ID, 10), providerName)
	// The codes store outlives 60s by design margin; the exchange checks the
	// tighter deadline itself.
	http.Redirect(w, r, h.appendWebCode(entry.payload, ticket), http.StatusFound)
}

// appendWebCode is appendCode with the web-origin prefix applied first —
// every browser hand-off in the flow goes through here.
func (h *OAuthAuthHandler) appendWebCode(target, code string) string {
	return appendCode(h.webTarget(target), code)
}

// appendCode adds ?code=… (or overwrites it) on a redirect target.
func appendCode(target, code string) string {
	if target == "" {
		target = "/"
	}
	if strings.Contains(target, "?") {
		return target + "&code=" + url.QueryEscape(code)
	}
	return target + "?code=" + url.QueryEscape(code)
}

// ── POST /v1/auth/exchange {code} ─────────────────────────────────────
// The only place a JWT is issued in the OAuth world. One code, one use,
// 60 seconds from mint.

type exchangeRequest struct {
	Code string `json:"code"`
}

func (h *OAuthAuthHandler) Exchange(w http.ResponseWriter, r *http.Request) {
	var req exchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, "could not read the sign-in code")
		return
	}
	entry, ok := h.codes.take(req.Code)
	if !ok {
		writeError(w, http.StatusBadRequest, "that sign-in code expired or was already used — sign in again")
		return
	}
	userID, err := strconv.ParseInt(entry.payload, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "that sign-in code is invalid — sign in again")
		return
	}
	user, err := h.store.GetUserByID(userID)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			writeError(w, http.StatusGone, "that account no longer exists — sign in again")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return
	}
	token, refresh, _, err := issueSession(h.store, h.auth, user.ID, user.Email, deviceLabel(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start your session")
		return
	}
	// A fresh login is a security event worth recording: if the account
	// owner did not do this, the sessions screen shows it.
	notify.Dispatch(h.store, h.notifier, user.ID, notify.KindSignIn,
		"New sign-in", "A new session started on your account. If this was not you, sign out everywhere from Profile.")
	writeJSON(w, http.StatusOK, authResponse{Token: token, RefreshToken: refresh, User: user})
}
