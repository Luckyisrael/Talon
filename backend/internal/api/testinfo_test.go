package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/relay"
	"github.com/seekersstudio/backend/internal/store"
)

// The phone surface is honest: available=false carries the reason, and the
// deep link is exactly the relay's public port.
func TestTestInfoHonesty(t *testing.T) {
	st, err := store.New(t.TempDir() + "/ti.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	u, err := st.CreateUser("ti@test", "x")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	proj, err := st.CreateProject(u.ID, "p", "v", "ti-slug", "a.b", "c.d", "e")
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	// No session: unavailable with the reason.
	info := fetchTestInfo(t, st, u, proj.ID)
	if info.Available {
		t.Error("fault: available with no session")
	}
	if info.Reason == "" {
		t.Error("fault: unavailable without a reason")
	}

	// A live session with a relay: the deep link names the relay port.
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	metro, err := relay.StartForSession(sess.ID, 17999, "127.0.0.1:8081")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	defer relay.StopSession(sess.ID)

	info = fetchTestInfo(t, st, u, proj.ID)
	if !info.Available {
		t.Fatalf("fault: live session + relay reported unavailable (%s)", info.Reason)
	}
	want := "exp://" + publicHost() + ":17999"
	if info.DeepLink != want {
		t.Errorf("fault: deep link %q, want %q", info.DeepLink, want)
	}
	_ = metro
}

func fetchTestInfo(t *testing.T, st *store.Store, u *store.User, projectID int64) testInfo {
	t.Helper()
	t.Setenv("PUBLIC_HOST", "localhost")
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), nil, nil, nil, nil), nil)
	req := authedRequest(u.ID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Info(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var info testInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return info
}
