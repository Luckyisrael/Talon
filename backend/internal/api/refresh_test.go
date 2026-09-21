package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/store"
)

func testRefreshUser(t *testing.T, st *store.Store) int64 {
	t.Helper()
	u, err := st.CreateUser("refresh@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func postRefresh(t *testing.T, h *OAuthAuthHandler, raw string) (int, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh",
		strings.NewReader(fmt.Sprintf(`{"refresh_token":%q}`, raw)))
	rec := httptest.NewRecorder()
	h.Refresh(rec, req)
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

// A fresh token refreshes: new access + rotated refresh, old one spent.
func TestRefreshRotates(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}

	code, body := postRefresh(t, h, raw)
	if code != http.StatusOK {
		t.Fatalf("refresh: status %d, body %v", code, body)
	}
	if body["token"] == "" || body["refresh_token"] == "" || body["refresh_token"] == raw {
		t.Errorf("refresh must return a new access + rotated pair, got %v", body)
	}

	// The spent token is dead now.
	code2, _ := postRefresh(t, h, raw)
	if code2 != http.StatusUnauthorized {
		t.Errorf("spent token reuse: status %d, want 401", code2)
	}
}

// Presenting a rotated token again means theft: the whole family dies,
// including the rotated replacement.
func TestRefreshReuseKillsFamily(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, first := postRefresh(t, h, raw)
	replacement := first["refresh_token"]
	if replacement == "" {
		t.Fatal("first refresh produced no replacement")
	}

	// Attacker replays the original.
	if code, _ := postRefresh(t, h, raw); code != http.StatusUnauthorized {
		t.Fatalf("replay: status %d, want 401", code)
	}
	// The legitimate replacement dies with the family.
	if code, _ := postRefresh(t, h, replacement); code != http.StatusUnauthorized {
		t.Errorf("family member after reuse: status %d, want 401", code)
	}
}

func TestRefreshRejectsUnknownAndExpired(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)

	if code, _ := postRefresh(t, h, "bogus-token"); code != http.StatusUnauthorized {
		t.Errorf("unknown token: status %d, want 401", code)
	}

	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ExpireRefreshToken(refreshHash(raw)); err != nil {
		t.Fatal(err)
	}
	if code, _ := postRefresh(t, h, raw); code != http.StatusUnauthorized {
		t.Errorf("expired token: status %d, want 401", code)
	}
}

// Logout revokes the presented token and always answers 200 — even for
// unknown tokens, so it never oracles which tokens exist.
func TestLogoutRevokesSingleToken(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/logout",
		strings.NewReader(fmt.Sprintf(`{"refresh_token":%q}`, raw)))
	rec := httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: status %d", rec.Code)
	}
	if code, _ := postRefresh(t, h, raw); code != http.StatusUnauthorized {
		t.Errorf("logged-out token: status %d, want 401", code)
	}

	// Unknown tokens still get a calm 200.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/auth/logout",
		strings.NewReader(`{"refresh_token":"nope"}`))
	rec2 := httptest.NewRecorder()
	h.Logout(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("unknown-token logout: status %d, want 200", rec2.Code)
	}
}

// Only hashes persist: the raw token resolves by hash, never by value, and
// a fresh row carries a future expiry.
func TestRefreshStoresHashesOnly(t *testing.T) {
	_, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}

	// The raw value is not a lookup key anywhere.
	if _, err := st.GetRefreshToken(raw); err == nil {
		t.Error("raw token resolves as a hash — only hashes may persist")
	}
	rec, err := st.GetRefreshToken(refreshHash(raw))
	if err != nil {
		t.Fatalf("lookup by hash: %v", err)
	}
	if rec.UserID != userID {
		t.Errorf("row user = %d, want %d", rec.UserID, userID)
	}
	if time.Now().After(rec.ExpiresAt) {
		t.Error("fresh token already expired")
	}
	if rec.RevokedAt != nil {
		t.Error("fresh token born revoked")
	}
}

// Sessions: live logins list newest first with no hashes; revoke drops one,
// foreign ids read as missing, logout-all ends everything.
func TestSessionsListRevokeLogoutAll(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	otherUser, err := st.CreateUser("other-sess@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	otherID := otherUser.ID

	mk := func(uid int64, label string) int64 {
		raw, _, err := mintRefreshToken(st, uid, label)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := st.GetRefreshToken(refreshHash(raw))
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	id1 := mk(userID, "phone")
	id2 := mk(userID, "browser")
	foreignID := mk(otherID, "other-phone")
	dead, _, err := mintRefreshToken(st, userID, "dead")
	if err != nil {
		t.Fatal(err)
	}
	deadRec, _ := st.GetRefreshToken(refreshHash(dead))
	_ = st.RevokeRefreshToken(deadRec.ID)

	authed := func(uid int64, params map[string]string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, uid))
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}

	// List: the two live ones, newest first, no hashes anywhere.
	rec := httptest.NewRecorder()
	h.ListSessions(rec, authed(userID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "token_hash") {
		t.Error("session list leaks token hashes")
	}
	var sessions []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || int(sessions[0]["id"].(float64)) != int(id2) {
		t.Errorf("sessions = %v, want [id2 id1] newest first", sessions)
	}
	if sessions[0]["device_label"] != "browser" {
		t.Errorf("device label = %v, want browser", sessions[0]["device_label"])
	}

	// Revoke one; foreign and missing read as 404.
	del := httptest.NewRecorder()
	h.RevokeSession(del, authed(userID, map[string]string{"id": "999999"}))
	if del.Code != http.StatusNotFound {
		t.Errorf("missing revoke: status %d, want 404", del.Code)
	}
	del2 := httptest.NewRecorder()
	h.RevokeSession(del2, authed(userID, map[string]string{"id": strconv.FormatInt(foreignID, 10)}))
	if del2.Code != http.StatusNotFound {
		t.Errorf("foreign revoke: status %d, want 404", del2.Code)
	}
	del3 := httptest.NewRecorder()
	req3 := authed(userID, map[string]string{"id": strconv.FormatInt(id1, 10)})
	req3.Method = http.MethodDelete
	h.RevokeSession(del3, req3)
	if del3.Code != http.StatusOK {
		t.Fatalf("revoke: status %d", del3.Code)
	}
	rec4 := httptest.NewRecorder()
	h.ListSessions(rec4, authed(userID, nil))
	var after []map[string]any
	_ = json.Unmarshal(rec4.Body.Bytes(), &after)
	if len(after) != 1 {
		t.Errorf("after revoke: %d sessions, want 1", len(after))
	}

	// Logout-all ends the survivor too.
	out := httptest.NewRecorder()
	h.LogoutAll(out, authed(userID, nil))
	if out.Code != http.StatusOK {
		t.Fatalf("logout-all: status %d", out.Code)
	}
	rec5 := httptest.NewRecorder()
	h.ListSessions(rec5, authed(userID, nil))
	var empty []map[string]any
	_ = json.Unmarshal(rec5.Body.Bytes(), &empty)
	if len(empty) != 0 {
		t.Errorf("after logout-all: %d sessions, want 0", len(empty))
	}
}

// Successful rotation stamps last-used on the surviving row.
func TestRefreshTouchesLastUsed(t *testing.T) {
	h, st := newOAuthHarness(t, nil)
	userID := testRefreshUser(t, st)
	raw, _, err := mintRefreshToken(st, userID, "")
	if err != nil {
		t.Fatal(err)
	}
	code, body := postRefresh(t, h, raw)
	if code != http.StatusOK {
		t.Fatalf("refresh: status %d", code)
	}
	rec, err := st.GetRefreshToken(refreshHash(body["refresh_token"]))
	if err != nil {
		t.Fatal(err)
	}
	if rec.LastUsedAt == nil {
		t.Error("rotated row carries no last-used stamp")
	}
}

// A fresh OAuth login records a sign-in alert: theft visibility for the
// account owner, next to the sessions that prove it.
func TestExchangeRecordsSignInAlert(t *testing.T) {
	stub := newStubOAuth(t, map[string]interface{}{
		"id": 12345, "login": "octocat", "name": "Octo Cat", "email": "octo@example.com", "avatar_url": "http://ava",
	})
	h, st := newOAuthHarness(t, stub)

	code, body := driveFlow(t, h, "/dashboard")
	if code != http.StatusOK {
		t.Fatalf("exchange: HTTP %d body=%s", code, body)
	}
	var resp struct {
		RefreshToken string      `json:"refresh_token"`
		User         *store.User `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RefreshToken == "" {
		t.Error("exchange must issue a refresh token")
	}
	notifs, err := st.ListNotifications(resp.User.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notifs {
		if n.Kind == "sign_in" {
			return
		}
	}
	t.Errorf("no sign_in alert recorded, got %+v", notifs)
}
