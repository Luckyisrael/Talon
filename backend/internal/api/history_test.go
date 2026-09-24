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
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/store"
)

func testHistoryStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/hist.db")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser("hist@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "hist", "1.0.0", "hist-slug", "com.test.hist", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, p.ID
}

func seedTurn(t *testing.T, st *store.Store, pid, promptID int64, role, text string) {
	t.Helper()
	blocks := ai.MarshalBlocks([]ai.Block{{Type: "text", Text: text}})
	if err := st.AppendMessage(pid, promptID, role, blocks, 0, 0); err != nil {
		t.Fatal(err)
	}
}

// The turn index lists newest first with metadata only, cursor-paged, and
// never the full prompt text.
func TestListPromptsIndex(t *testing.T) {
	st, pid := testHistoryStore(t)
	h := &PromptHandler{store: st}
	for i := 0; i < 3; i++ {
		p, err := st.CreateTitledPrompt(pid, "build", "prompt text number "+string(rune('a'+i)), "title")
		if err != nil {
			t.Fatal(err)
		}
		_ = p
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, int64(1)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", pid))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ListPrompts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", rec.Code, rec.Body.String())
	}
	var entries []store.PromptIndexEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want 3", entries)
	}
	if entries[0].ID < entries[1].ID {
		t.Error("index must be newest first")
	}
	if strings.Contains(rec.Body.String(), "prompt text number") {
		t.Error("index must not carry full prompt text")
	}
}

// before_seq pages backwards and latest_seq watermarks every response.
func TestConversationPagination(t *testing.T) {
	st, pid := testHistoryStore(t)
	h := &PromptHandler{store: st, slog: newSessionLogCache()}
	for i := 0; i < 5; i++ {
		seedTurn(t, st, pid, int64(i+1), "user", fmt.Sprintf("turn %d", i))
	}

	get := func(query string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, "/"+query, nil)
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, int64(1)))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("projectID", fmt.Sprintf("%d", pid))
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.GetConversation(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := get("?limit=2")
	if code != http.StatusOK {
		t.Fatalf("page one: status %d", code)
	}
	turns := body["turns"].([]any)
	if len(turns) != 2 {
		t.Fatalf("page one turns = %d, want 2", len(turns))
	}
	if body["latest_seq"].(float64) != 5 {
		t.Errorf("latest_seq = %v, want 5", body["latest_seq"])
	}
	first := turns[0].(map[string]any)
	if first["seq"].(float64) != 4 {
		t.Errorf("page one opens at seq %v, want 4", first["seq"])
	}
	if _, ok := first["prompt_id"]; !ok {
		t.Error("turns must carry prompt_id for scroll mapping")
	}

	code, body = get("?limit=2&before_seq=4")
	if code != http.StatusOK {
		t.Fatalf("page two: status %d", code)
	}
	turns = body["turns"].([]any)
	if len(turns) != 2 || turns[0].(map[string]any)["seq"].(float64) != 2 {
		t.Errorf("page two = %v, want seqs 2,3", turns)
	}

	if code, _ := get("?before_seq=0"); code != http.StatusBadRequest {
		t.Errorf("before_seq=0: status %d, want 400", code)
	}
}

// Search matches substrings literally: LIKE metacharacters in the query
// must not act as wildcards.
func TestSearchConversationEscapesWildcards(t *testing.T) {
	st, pid := testHistoryStore(t)
	h := &PromptHandler{store: st}
	seedTurn(t, st, pid, 1, "user", "discount 100% today")
	seedTurn(t, st, pid, 2, "user", "unrelated hello")

	// Through a real chi router carrying the production path pattern, not a
	// hand-built RouteContext: injecting params by hand mirrors whatever the
	// handler assumes, which is exactly how a route/param name mismatch
	// (reading "id" while the route declares "{projectID}") stays invisible
	// until a live request 400s on every call.
	router := chi.NewRouter()
	router.Get("/v1/projects/{projectID}/conversation/search", h.SearchConversation)
	search := func(q string) (int, []any) {
		req := httptest.NewRequest(http.MethodGet,
			"/v1/projects/"+fmt.Sprintf("%d", pid)+"/conversation/search", nil)
		req = req.WithContext(context.WithValue(req.Context(), UserIDKey, int64(1)))
		req.URL.RawQuery = "q=" + q
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		turns, _ := body["turns"].([]any)
		return rec.Code, turns
	}

	// % must match literally: only the discount turn qualifies, not everything.
	if code, turns := search("100%25"); code != http.StatusOK || len(turns) != 1 {
		t.Errorf("escaped search: status %d turns %d, want 200/1", code, len(turns))
	}
	if code, _ := search("x"); code != http.StatusBadRequest {
		t.Errorf("short query: status %d, want 400", code)
	}
}
