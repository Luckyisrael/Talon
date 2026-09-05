package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/store"
)

// The triage report persists and reads back exactly what was sent — decision,
// dates, and every cent of the dollar exposure.
func TestTriageRoundTrip(t *testing.T) {
	st, err := store.New(t.TempDir() + "/triage.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("triage@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "triage", "1.0.0", "triage-slug", "com.test.triage", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := NewTriageHandler(st)

	withID := func(req *http.Request) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", fmt.Sprintf("%d", p.ID))
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}

	// Blank slate: GET returns null, not an error.
	getRec := httptest.NewRecorder()
	h.Get(getRec, withID(authedRequest(u.ID, "")))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get empty: status %d", getRec.Code)
	}
	if strings.TrimSpace(getRec.Body.String()) != "null" {
		t.Fatalf("expected null, got %s", getRec.Body.String())
	}

	// Save with an odd-cent exposure — floats would round this.
	body := `{"decision":"cutover_scheduled","cutover_date":"2026-10-01","exposure_cents":19999,"notes":"holiday freeze"}`
	putRec := httptest.NewRecorder()
	h.Save(putRec, withID(authedRequest(u.ID, body)))
	if putRec.Code != http.StatusOK {
		t.Fatalf("save: status %d, body %s", putRec.Code, putRec.Body.String())
	}

	var got store.TriageReport
	getRec2 := httptest.NewRecorder()
	h.Get(getRec2, withID(authedRequest(u.ID, "")))
	if err := json.Unmarshal(getRec2.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Decision != "cutover_scheduled" || got.CutoverDate != "2026-10-01" ||
		got.ExposureCents != 19999 || got.Notes != "holiday freeze" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Revise: same project, rewritten not forked.
	body2 := `{"decision":"cutover_now","cutover_date":"2026-09-10","exposure_cents":499,"notes":""}`
	putRec2 := httptest.NewRecorder()
	h.Save(putRec2, withID(authedRequest(u.ID, body2)))
	if putRec2.Code != http.StatusOK {
		t.Fatalf("revise: status %d", putRec2.Code)
	}
	again, err := st.LoadTriageReport(p.ID)
	if err != nil || again.Decision != "cutover_now" || again.ExposureCents != 499 {
		t.Fatalf("revise mismatch: %+v %v", again, err)
	}

	// Another user's project is invisible.
	other, _ := st.CreateUser("other@test.com", "hash")
	otherRec := httptest.NewRecorder()
	h.Get(otherRec, withID(authedRequest(other.ID, "")))
	if otherRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for foreign project, got %d", otherRec.Code)
	}
}
