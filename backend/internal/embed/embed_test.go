package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// decodeRequest is the handler-side view of one embeddings call.
func decodeRequest(t *testing.T, r *http.Request) struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
} {
	t.Helper()
	var req struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return req
}

// TestAPIRequestShapeAndReordering pins the wire contract (path, headers,
// model/input body) and the client's index handling: the server deliberately
// serves items in reverse order, and each vector carries its own input
// position — so out[ i ][0] must equal i after the client sorts.
func TestAPIRequestShapeAndReordering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s, want /v1/embeddings", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer sk-test"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		req := decodeRequest(t, r)
		if req.Model != "m-test" {
			t.Errorf("model = %q, want m-test", req.Model)
		}
		n := len(req.Input)
		items := make([]apiItem, n)
		for i := range req.Input {
			pos := n - 1 - i
			items[i] = apiItem{Index: pos, Embedding: []float32{float32(pos)}}
		}
		_ = json.NewEncoder(w).Encode(apiResponse{Data: items})
	}))
	defer srv.Close()

	a := &API{Endpoint: srv.URL + "/v1/embeddings", APIKey: "sk-test", Model: "m-test"}
	vecs, err := a.Embed(context.Background(), []string{"alpha", "beta", "gamma"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 1 || v[0] != float32(i) {
			t.Errorf("vecs[%d] = %v, want marker %d (reordering broken)", i, v, i)
		}
	}
}

// TestAPIBatchesAndConcatenates pins the batching loop: 5 texts at batch 2
// must produce requests of 2/2/1 whose vectors land back in input order.
func TestAPIBatchesAndConcatenates(t *testing.T) {
	var mu sync.Mutex
	var sizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeRequest(t, r)
		mu.Lock()
		sizes = append(sizes, len(req.Input))
		mu.Unlock()
		items := make([]apiItem, len(req.Input))
		for i, text := range req.Input {
			items[i] = apiItem{Index: i, Embedding: []float32{float32(len(text))}}
		}
		_ = json.NewEncoder(w).Encode(apiResponse{Data: items})
	}))
	defer srv.Close()

	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	a := &API{Endpoint: srv.URL, Batch: 2}
	vecs, err := a.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 {
		t.Errorf("batch sizes = %v, want [2 2 1]", sizes)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if v[0] != float32(len(texts[i])) {
			t.Errorf("vecs[%d][0] = %v, want %d — concatenation misaligned", i, v[0], len(texts[i]))
		}
	}
}

// TestAPIRejectsBadResponses: every shape that could silently misalign the
// cache is a hard error, never a best-effort guess.
func TestAPIRejectsBadResponses(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"status 500", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}, "status 500"},
		{"wrong count", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apiResponse{Data: []apiItem{{Index: 0, Embedding: []float32{1}}}})
		}, "1 items for 2 inputs"},
		{"duplicate index", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apiResponse{Data: []apiItem{
				{Index: 0, Embedding: []float32{1}},
				{Index: 0, Embedding: []float32{2}},
			}})
		}, "untrustworthy indices"},
		{"mixed dims", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apiResponse{Data: []apiItem{
				{Index: 0, Embedding: []float32{1}},
				{Index: 1, Embedding: []float32{1, 2}},
			}})
		}, "mixed vector dimensions"},
		{"empty vectors", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(apiResponse{Data: []apiItem{
				{Index: 0, Embedding: []float32{}},
				{Index: 1, Embedding: []float32{}},
			}})
		}, "empty vectors"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			a := &API{Endpoint: srv.URL}
			_, err := a.Embed(context.Background(), []string{"x", "y"})
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("err = %v, want containing %q", err, c.wantErr)
			}
		})
	}
}

// TestAPIEdgeCases: empty input is a no-op (no request), and a missing
// endpoint is an error rather than a nil-vector surprise.
func TestAPIEdgeCases(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(apiResponse{})
	}))
	defer srv.Close()

	a := &API{Endpoint: srv.URL}
	vecs, err := a.Embed(context.Background(), nil)
	if err != nil || vecs != nil {
		t.Errorf("empty input: (%v, %v), want (nil, nil)", vecs, err)
	}
	if hits != 0 {
		t.Errorf("empty input made %d requests, want 0", hits)
	}
	if _, err := (&API{}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("missing endpoint must error")
	}
}
