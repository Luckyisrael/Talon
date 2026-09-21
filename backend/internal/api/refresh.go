package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// Refresh tokens: opaque session-continuation credentials so a visit never
// forces another OAuth round trip. The access token stays short (24h); the
// refresh token lives 30 days, rotates on every use, and dies with its whole
// family if a spent token is ever presented again (reuse = leak).

const refreshTokenTTL = 30 * 24 * time.Hour

// mintRefreshToken creates a raw token and stores only its hash. The raw
// value leaves this function once, in the response — it is never logged,
// never stored, never recoverable server-side.
func mintRefreshToken(s *store.Store, userID int64, deviceLabel string) (raw string, rec *store.RefreshToken, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	rec, err = s.AddRefreshToken(userID, base64.RawURLEncoding.EncodeToString(sum[:]), time.Now().Add(refreshTokenTTL), deviceLabel)
	if err != nil {
		return "", nil, err
	}
	return raw, rec, nil
}

// issueSession mints the access + refresh pair every sign-in path returns.
// One place so OAuth, wallet, and any future login cannot drift on shape.
func issueSession(s *store.Store, auth interface {
	GenerateToken(userID int64, email string) (string, error)
}, userID int64, email, deviceLabel string) (access, refresh string, rec *store.RefreshToken, err error) {
	access, err = auth.GenerateToken(userID, email)
	if err != nil {
		return "", "", nil, err
	}
	var raw string
	raw, rec, err = mintRefreshToken(s, userID, deviceLabel)
	if err != nil {
		return "", "", nil, err
	}
	return access, raw, rec, nil
}

// deviceLabel names the caller for the sessions screen: the User-Agent,
// truncated. Empty when the client sends none.
func deviceLabel(r *http.Request) string {
	ua := strings.TrimSpace(r.UserAgent())
	if len(ua) > 128 {
		ua = ua[:128]
	}
	return ua
}

// ── POST /v1/auth/refresh {refresh_token} ───────────────────────────
// Spends one refresh token, returns a fresh access + refresh pair. Rotation
// is atomic (single conditional UPDATE): concurrent double-use claims one
// winner. Presenting a rotated, expired, or unknown token fails closed; a
// rotated token additionally kills the whole family, because only a leak
// explains it.
func (h *OAuthAuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}
	sum := sha256.Sum256([]byte(req.RefreshToken))
	rec, err := h.store.GetRefreshToken(base64.RawURLEncoding.EncodeToString(sum[:]))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "that session has ended — sign in again")
		return
	}
	if rec.RevokedAt != nil {
		// Spent token presented again: treat as theft, kill the family.
		log.Printf("refresh reuse for user %d — revoking token family", rec.UserID)
		_ = h.store.RevokeUserRefreshTokens(rec.UserID)
		writeError(w, http.StatusUnauthorized, "that session has ended — sign in again")
		return
	}
	if time.Now().After(rec.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "that session has ended — sign in again")
		return
	}
	user, err := h.store.GetUserByID(rec.UserID)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			writeError(w, http.StatusGone, "that account no longer exists — sign in again")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return
	}
	access, rotated, newRec, err := issueSession(h.store, h.auth, user.ID, user.Email, deviceLabel(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not refresh your session")
		return
	}
	ok, err := h.store.RotateRefreshToken(rec.ID, newRec.ID)
	if err != nil || !ok {
		// Lost the rotation race (or the row vanished): the new pair is
		// orphaned — revoke it so it cannot linger unclaimed, and fail.
		_ = h.store.RevokeRefreshToken(newRec.ID)
		writeError(w, http.StatusUnauthorized, "that session has ended — sign in again")
		return
	}
	// The rotation succeeded: this token is alive and in use now.
	_ = h.store.TouchRefreshToken(newRec.ID)
	go h.store.PurgeExpiredRefreshTokens()
	writeJSON(w, http.StatusOK, map[string]string{
		"token":         access,
		"refresh_token": rotated,
	})
}

func refreshHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ── POST /v1/auth/logout ─────────────────────────────────────────────
// Revokes the presented refresh token (body {refresh_token}), ending that
// device's session without touching others. Unknown tokens still return 200:
// logout must never oracle which tokens exist. Clients always clear local
// tokens too — revocation is the server half.
func (h *OAuthAuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.RefreshToken != "" {
		if rec, err := h.store.GetRefreshToken(refreshHash(req.RefreshToken)); err == nil {
			_ = h.store.RevokeRefreshToken(rec.ID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

// ── GET /v1/auth/sessions ────────────────────────────────────────────
// The caller's live logins, newest first: what the Profile sessions screen
// renders. Hashes never leave the database; ids are revocation handles.
func (h *OAuthAuthHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.store.ListUserSessions(GetUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list sessions")
		return
	}
	if sessions == nil {
		sessions = []store.UserSession{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

// ── DELETE /v1/auth/sessions/{id} ────────────────────────────────────
// Revokes one login. Foreign or missing ids read as missing, so the endpoint
// does not confirm other users' session ids exist.
func (h *OAuthAuthHandler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	rec, err := h.store.GetRefreshTokenByID(id)
	if err != nil || rec.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	_ = h.store.RevokeRefreshToken(rec.ID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ── POST /v1/auth/logout/all ─────────────────────────────────────────
// Revokes every login on the account, current device included: the caller
// signs itself out right after. For a suspected theft, pair with a password
// or provider-side review — this ends sessions, not provider access.
func (h *OAuthAuthHandler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	_ = h.store.RevokeUserRefreshTokens(GetUserID(r.Context()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out everywhere"})
}
