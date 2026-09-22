// Package retrieval is W4's retrieval benchmark (the plan's eval gate):
// queries with known gold paths over a checked-in fixture corpus
// (fixture/queries.json + fixture/src). It measures hit@5 — gold path within
// the first five distinct results — for the indexed search tool against a
// grep-only baseline derived from the same query, end to end through the
// real tool outputs (one path parser reads both, by design: they share the
// path:line prefix).
//
// Today's indexed search is bm25-only: the lexical half of "hybrid". The
// vector half joins at the same Search seam when the embedding provider
// lands (W4.3) and this fixture keeps measuring the fused result — the gate
// (indexed >= grep-only) only gets harder to satisfy dishonestly as the
// halves combine.
package retrieval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/luckyisrael/talon/backend/internal/index"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

type queryCase struct {
	Query string `json:"query"`
	Gold  string `json:"gold"`
}

func loadQueries(t *testing.T) []queryCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("fixture", "queries.json"))
	if err != nil {
		t.Fatalf("read queries.json: %v", err)
	}
	var qs []queryCase
	if err := json.Unmarshal(raw, &qs); err != nil {
		t.Fatalf("parse queries.json: %v", err)
	}
	if len(qs) == 0 {
		t.Fatal("no queries in fixture")
	}
	return qs
}

func toolArgs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return b
}

// pathLineRe parses both tools' shared line prefix: "path:line…". Grep emits
// "path:line: content", search emits "path:line (name): excerpt" — one
// regex reads each, which is the formatting contract this benchmark pins.
var pathLineRe = regexp.MustCompile(`^([^\s:]+):\d+`)

// pathsFromTool returns distinct paths in result order — hit@5 looks only at
// the first five.
func pathsFromTool(content string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(content, "\n") {
		m := pathLineRe.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	return out
}

func inTop5(paths []string, gold string) bool {
	for i, p := range paths {
		if i >= 5 {
			return false
		}
		if p == gold {
			return true
		}
	}
	return false
}

// grepStop / grepPattern build the grep-only baseline: the query's
// meaningful tokens as a case-insensitive alternation — a generous,
// competent-agent reading of "what would you grep for?" (stopwords out,
// short words out, QuoteMeta'd). The indexed system has to beat THIS, not a
// strawman.
var grepStop = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "of": true,
	"to": true, "in": true, "on": true, "for": true, "with": true, "and": true,
	"or": true, "how": true, "what": true, "where": true, "when": true,
	"does": true, "do": true, "did": true, "who": true, "why": true,
	"this": true, "that": true, "these": true, "those": true, "it": true,
}

func grepPattern(query string) string {
	seen := map[string]bool{}
	var toks []string
	for _, w := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		w = strings.ToLower(w)
		if len(w) < 3 || grepStop[w] || seen[w] {
			continue
		}
		seen[w] = true
		toks = append(toks, regexp.QuoteMeta(w))
	}
	if len(toks) == 0 {
		return ""
	}
	return "(?i)" + strings.Join(toks, "|")
}

// newRetrievalRig indexes the fixture once and returns the search tool wired
// the way api/buildsession wires it in production, next to the grep baseline.
func newRetrievalRig(t *testing.T) (*tools.CodebaseSearch, *tools.GrepCodebase) {
	t.Helper()
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	fixtureDir := "fixture"
	st, err := store.New(filepath.Join(t.TempDir(), "retrieval.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const pid = int64(11)
	reconcile := func() error {
		_, err := index.Reconcile(fixtureDir, pid, st)
		return err
	}
	if err := reconcile(); err != nil {
		t.Fatalf("index build: %v", err)
	}
	fs := tools.NewFS(fixtureDir)
	search := &tools.CodebaseSearch{
		FS:        fs,
		Reconcile: reconcile,
		Search: func(query string, limit int) ([]tools.IndexHit, error) {
			hits, err := st.SearchCodeChunks(pid, query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]tools.IndexHit, 0, len(hits))
			for _, h := range hits {
				out = append(out, tools.IndexHit{Path: h.Path, Name: h.Name, StartLine: h.StartLine, Body: h.Body})
			}
			return out, nil
		},
	}
	return search, &tools.GrepCodebase{FS: fs}
}

// TestHitAt5IndexedAtLeastGrep is THE retrieval gate: over queries with known
// gold paths, indexed hit@5 must be >= grep-only hit@5 — and above a floor,
// so "0 >= 0" can never satisfy it.
func TestHitAt5IndexedAtLeastGrep(t *testing.T) {
	queries := loadQueries(t)
	search, grep := newRetrievalRig(t)

	indexed, grepHits := 0, 0
	for _, q := range queries {
		pattern := grepPattern(q.Query)
		if pattern == "" {
			t.Fatalf("query %q yielded no baseline tokens", q.Query)
		}

		sr, err := search.Execute(context.Background(), toolArgs(t, map[string]any{"query": q.Query, "limit": 5}))
		if err != nil {
			t.Fatalf("search(%q): %v", q.Query, err)
		}
		if sr.IsError {
			t.Fatalf("search(%q) errored: %s", q.Query, sr.Content)
		}
		sPaths := pathsFromTool(sr.Content)
		if inTop5(sPaths, q.Gold) {
			indexed++
		} else {
			t.Logf("indexed MISS: %q -> gold %s, got %v", q.Query, q.Gold, sPaths)
		}

		gr, err := grep.Execute(context.Background(), toolArgs(t, map[string]any{"pattern": pattern}))
		if err != nil {
			t.Fatalf("grep(%q): %v", q.Query, err)
		}
		gPaths := pathsFromTool(gr.Content)
		if inTop5(gPaths, q.Gold) {
			grepHits++
		} else {
			t.Logf("grep MISS: %q -> gold %s, got %v", q.Query, q.Gold, gPaths)
		}
	}

	if indexed < grepHits {
		t.Errorf("hit@5: indexed %d/%d < grep-only %d/%d — indexed retrieval must not lose to grep",
			indexed, len(queries), grepHits, len(queries))
	}
	// Floor: all eight fixture queries are lexically answerable; a regression
	// that stops the index finding most of them must fail even if grep does
	// worse too.
	if indexed < 6 {
		t.Errorf("hit@5 floor: indexed %d/%d, want >= 6 — the gate must not pass vacuously",
			indexed, len(queries))
	}
}

func convertEdges(edges []store.CodeEdge) []tools.IndexEdge {
	out := make([]tools.IndexEdge, 0, len(edges))
	for _, e := range edges {
		out = append(out, tools.IndexEdge{FromPath: e.FromPath, FromName: e.FromName, Kind: e.Kind, ToPath: e.ToPath, ToName: e.ToName})
	}
	return out
}

// TestGraphEndToEnd walks the real edge rows through the real tool:
// definitions, callers, callees, and the two-hop caller expansion (which the
// fixture deliberately under-fills — nothing calls authorizeTransaction —
// so a stray hop-2 line would itself be a bug).
func TestGraphEndToEnd(t *testing.T) {
	t.Setenv("TALONSTUDIO_INDEX", "v1")
	fixtureDir := "fixture"
	st, err := store.New(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const pid = int64(12)
	reconcile := func() error {
		_, err := index.Reconcile(fixtureDir, pid, st)
		return err
	}
	if err := reconcile(); err != nil {
		t.Fatalf("index build: %v", err)
	}
	fs := tools.NewFS(fixtureDir)
	graph := &tools.CodebaseGraph{
		FS:        fs,
		Reconcile: reconcile,
		Definitions: func(symbol string) ([]tools.IndexSymbol, error) {
			syms, err := st.CodeDefinitions(pid, symbol)
			if err != nil {
				return nil, err
			}
			out := make([]tools.IndexSymbol, 0, len(syms))
			for _, s := range syms {
				out = append(out, tools.IndexSymbol{Path: s.Path, Name: s.Name, Kind: s.Kind, Line: s.Line})
			}
			return out, nil
		},
		Callers: func(symbol string) ([]tools.IndexEdge, error) {
			edges, err := st.CodeEdgesTo(pid, "call", symbol)
			return convertEdges(edges), err
		},
		Callees: func(symbol string) ([]tools.IndexEdge, error) {
			edges, err := st.CodeEdgesFromSymbol(pid, symbol)
			return convertEdges(edges), err
		},
	}

	mustSay := func(t *testing.T, input map[string]any, wants ...string) string {
		t.Helper()
		res, err := graph.Execute(context.Background(), toolArgs(t, input))
		if err != nil {
			t.Fatalf("graph(%v): %v", input, err)
		}
		if res.IsError {
			t.Fatalf("graph(%v) errored: %s", input, res.Content)
		}
		for _, w := range wants {
			if !strings.Contains(res.Content, w) {
				t.Errorf("graph(%v) missing %q in:\n%s", input, w, res.Content)
			}
		}
		return res.Content
	}

	mustSay(t, map[string]any{"symbol": "issueJWTSession", "direction": "definitions"},
		"src/auth.ts:", "function issueJWTSession")
	callers := mustSay(t, map[string]any{"symbol": "signTransaction", "direction": "callers"},
		"src/wallet.ts", "authorizeTransaction")
	if strings.Contains(callers, "hop 2") {
		t.Errorf("unexpected hop-2 caller in fixture:\n%s", callers)
	}
	mustSay(t, map[string]any{"symbol": "authorizeTransaction", "direction": "callees"},
		"signTransaction")
}
