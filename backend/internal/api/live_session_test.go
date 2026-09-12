package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// The refresh lifeline (W1): GET /v1/projects/{id}/live-session returns the
// project's live session row — ownership-checked, 204 when none, and the
// read itself counts as user activity (the same signal a stream join sends)
// so a user refreshing the page doesn't have their device reaped as idle.
func TestLiveForProject(t *testing.T) {
	dir := t.TempDir()
	st, err := store.New(dir + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("live@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser("other@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Live", "v1", "live", "com.talon.live", "com.talon.live", "liveapp")
	if err != nil {
		t.Fatal(err)
	}

	h := NewSessionHandler(st, nil, nil, nil, nil, "", nil, nil, nil)
	call := func(userID int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/projects/1/live-session", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserIDKey, userID))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("projectID", strconv.FormatInt(proj.ID, 10))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.LiveForProject(rec, r)
		return rec
	}

	// No session yet: 204, no body — the honest empty.
	if rec := call(user.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("no-session status = %d, want 204", rec.Code)
	}

	// A live session: 200 with the row.
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := call(user.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response not json: %v", err)
	}
	if body["id"].(float64) != float64(sess.ID) {
		t.Fatalf("returned id = %v, want %d", body["id"], sess.ID)
	}

	// Foreign owner: 404, never a row — the same posture as every other
	// project-scoped route.
	if rec := call(other.ID); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign-owner status = %d, want 404", rec.Code)
	}
}
