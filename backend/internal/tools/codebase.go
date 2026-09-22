package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/recall"
)

// ── codebase_search / codebase_graph (W4.4) ─────────────────────────
//
// The index-backed retrieval pair, registered only when TALONSTUDIO_INDEX is
// on. Like StoreAdapter for memory, both take function fields wired by the
// session builder (api/buildsession): the tools package never imports the
// store — the Index* boundary types below are converted at that wiring
// point. Both reconcile the index before answering (freshness with no
// filesystem watcher — reconcile-on-touch), and both degrade honestly:
// flag off they don't exist at all (grep-only fallback), wiring missing
// they say so instead of pretending.

// IndexHit is one ranked chunk from the lexical layer. The bm25 Score the
// store returns is deliberately absent: rank order carries it, and a number
// the model cannot act on is noise.
type IndexHit struct {
	Path      string
	Name      string
	StartLine int
	Body      string
}

// IndexSymbol is one declaration vertex of the graph.
type IndexSymbol struct {
	Path string
	Name string
	Kind string
	Line int
}

// IndexEdge is one directed graph edge — same shape the store returns.
type IndexEdge struct {
	FromPath string
	FromName string
	Kind     string
	ToPath   string
	ToName   string
}

// CodebaseSearch is hybrid retrieval: bm25 over chunks, names, and paths
// fused (W4.5, recall.Fuse) with a cosine vector leg when one is wired —
// Reconcile runs first so results reflect files just written this turn.
type CodebaseSearch struct {
	FS        *FS
	Reconcile func() error // freshness hook; nil = index not wired
	Search    func(query string, limit int) ([]IndexHit, error)
	// VectorSearch is the cosine leg (W4.5). nil means the vector layer is
	// off (no provider configured) — lexical behavior is byte-identical;
	// an error surfaces as an inline note, never a failed call.
	VectorSearch func(query string, limit int) ([]IndexHit, error)
}

func (t *CodebaseSearch) Name() string { return "codebase_search" }

func (t *CodebaseSearch) Description() string {
	return "Search the indexed codebase by meaning, not just spelling: natural-language questions or " +
		"identifiers rank over code chunks (bm25 across declaration names, bodies, and paths). The index " +
		"reconciles first, so files written this turn are already in the results. Returns ranked " +
		"path:line spans — read_file for contents. Prefer this for \"how/where is X done\" questions; " +
		"grep_codebase stays the exact-regex tool."
}

func (t *CodebaseSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Natural-language question or identifier(s) to find"},
			"limit": {"type": "integer", "description": "Max results, 1-25 (default 10)"}
		},
		"required": ["query"]
	}`)
}

func (t *CodebaseSearch) ReadOnly() bool { return true }

func (t *CodebaseSearch) Execute(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	if strings.TrimSpace(a.Query) == "" {
		return errResult("query must not be empty"), nil
	}
	limit := a.Limit
	if limit <= 0 || limit > 25 {
		limit = 10
	}

	// A reconcile failure leaves the answer stale-but-honest rather than
	// failing the call: the index accelerates, it does not gate.
	stale := ""
	if t.Reconcile != nil {
		if err := t.Reconcile(); err != nil {
			stale = fmt.Sprintf("\n\n[index reconcile failed — results may be stale: %v]", err)
		}
	}
	if t.Search == nil {
		return ToolResult{
			Content: "the codebase index is not wired in this session — use grep_codebase instead",
			IsError: true,
		}, nil
	}
	hits, err := t.Search(a.Query, limit)
	if err != nil {
		return errResult("index search failed: %v — fall back to grep_codebase", err), nil
	}
	// W4.5 vector leg: nil field (or nil provider inside the wiring) means
	// off — lexical behavior is byte-identical; a failing leg degrades to
	// lexical with an honest note — the index accelerates, it never gates.
	// Both legs read the same reconciled rows, so chunk IDs align for fusion.
	var vecHits []IndexHit
	if t.VectorSearch != nil {
		v, verr := t.VectorSearch(a.Query, limit)
		if verr != nil {
			stale += fmt.Sprintf("\n\n[vector leg unavailable: %v]", verr)
		}
		vecHits = v
	}
	if len(vecHits) > 0 {
		if len(hits) == 0 {
			hits = vecHits
		} else {
			hits = fuseIndexHits(hits, vecHits, limit)
		}
	}
	if len(hits) == 0 {
		return ToolResult{
			Content: fmt.Sprintf("No index hits for %q. Try grep_codebase for exact patterns, or reword with different terms.", a.Query) + stale,
		}, nil
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, fmt.Sprintf("%s:%d (%s): %s", h.Path, h.StartLine, h.Name, excerpt(h.Body, 160)))
	}
	return ToolResult{Content: strings.Join(out, "\n") + stale}, nil
}

// hitID is a chunk's fusion identity: path + line + name — both legs read
// the same reconciled rows, so an FTS hit and a vector hit for one chunk
// pair up exactly.
func hitID(h IndexHit) string {
	return h.Path + ":" + strconv.Itoa(h.StartLine) + ":" + h.Name
}

// fuseIndexHits merges both legs through recall.Fuse (lexical passed first,
// so exact-score ties resolve toward bm25 evidence) and truncates to limit.
func fuseIndexHits(fts, vec []IndexHit, limit int) []IndexHit {
	byID := map[string]IndexHit{}
	lexIDs := make([]string, 0, len(fts))
	vecIDs := make([]string, 0, len(vec))
	for _, h := range fts {
		id := hitID(h)
		lexIDs = append(lexIDs, id)
		byID[id] = h
	}
	for _, h := range vec {
		id := hitID(h)
		if _, dup := byID[id]; !dup {
			byID[id] = h
		}
		vecIDs = append(vecIDs, id)
	}
	fused := recall.Fuse(lexIDs, vecIDs)
	out := make([]IndexHit, 0, len(fused))
	for _, id := range fused {
		out = append(out, byID[id])
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// excerpt is the first non-empty line of a body, trimmed — the same
// path:line-prefixed shape grep_codebase outputs, so one parser reads both.
func excerpt(body string, n int) string {
	for _, line := range strings.Split(body, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			if len(s) > n {
				return s[:n] + "…"
			}
			return s
		}
	}
	return ""
}

// CodebaseGraph answers "who uses X" and "what does X use" without reading
// files: definitions, callers, or callees, expandable to two hops. Callers
// and callees are name-joined (declarations are unique; call sites are not),
// attributed to the file and enclosing symbol that made the edge.
type CodebaseGraph struct {
	FS          *FS
	Reconcile   func() error
	Definitions func(symbol string) ([]IndexSymbol, error)
	Callers     func(symbol string) ([]IndexEdge, error)
	Callees     func(symbol string) ([]IndexEdge, error)
}

func (t *CodebaseGraph) Name() string { return "codebase_graph" }

func (t *CodebaseGraph) Description() string {
	return "Walk the symbol graph: where a name is defined, who calls it, or what it calls — with the " +
		"file and enclosing symbol for each edge, and an optional second hop. Answers \"who uses X\" / " +
		"\"what does X use\" in one call instead of grepping for every call site. The index reconciles " +
		"first, so edges reflect files just written."
}

func (t *CodebaseGraph) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"symbol": {"type": "string", "description": "Identifier to trace, e.g. useAuth"},
			"direction": {"type": "string", "enum": ["definitions", "callers", "callees"], "description": "Which way to walk (default callers)"},
			"distance": {"type": "integer", "description": "Hop depth 1 or 2 for callers/callees (default 1); definitions are always direct"}
		},
		"required": ["symbol"]
	}`)
}

func (t *CodebaseGraph) ReadOnly() bool { return true }

func (t *CodebaseGraph) Execute(_ context.Context, args json.RawMessage) (ToolResult, error) {
	var a struct {
		Symbol    string `json:"symbol"`
		Direction string `json:"direction"`
		Distance  int    `json:"distance"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err), nil
	}
	symbol := strings.TrimSpace(a.Symbol)
	if symbol == "" {
		return errResult("symbol must not be empty"), nil
	}
	direction := a.Direction
	if direction == "" {
		direction = "callers"
	}
	distance := a.Distance
	if distance <= 0 {
		distance = 1
	}
	if distance > 2 {
		distance = 2
	}

	stale := ""
	if t.Reconcile != nil {
		if err := t.Reconcile(); err != nil {
			stale = fmt.Sprintf("\n\n[index reconcile failed — graph may be stale: %v]", err)
		}
	}

	switch direction {
	case "definitions":
		if t.Definitions == nil {
			return notWired("definitions"), nil
		}
		defs, err := t.Definitions(symbol)
		if err != nil {
			return errResult("index definitions lookup failed: %v", err), nil
		}
		if len(defs) == 0 {
			return ToolResult{Content: fmt.Sprintf("No definitions of %q in the index.", symbol) + stale}, nil
		}
		out := make([]string, 0, len(defs))
		for _, d := range defs {
			out = append(out, fmt.Sprintf("%s:%d: %s %s", d.Path, d.Line, d.Kind, d.Name))
		}
		return ToolResult{Content: strings.Join(out, "\n") + stale}, nil

	case "callers":
		if t.Callers == nil {
			return notWired("callers"), nil
		}
		level1, err := t.Callers(symbol)
		if err != nil {
			return errResult("index caller lookup failed: %v", err), nil
		}
		seen := map[string]bool{}
		var out []string
		add := func(e IndexEdge, hop int) {
			key := e.FromPath + "\x00" + e.FromName
			if seen[key] {
				return
			}
			seen[key] = true
			line := fmt.Sprintf("%s · %s", e.FromPath, e.FromName)
			if e.FromName == "" {
				line = e.FromPath + " (file level)"
			}
			if hop == 2 {
				line += " (hop 2)"
			}
			out = append(out, line)
		}
		for _, e := range level1 {
			add(e, 1)
		}
		if distance == 2 {
			for _, e := range level1 {
				if e.FromName == "" || e.FromName == symbol {
					continue
				}
				next, err := t.Callers(e.FromName)
				if err != nil {
					return errResult("index caller lookup failed: %v", err), nil
				}
				for _, e2 := range next {
					add(e2, 2)
				}
			}
		}
		if len(out) == 0 {
			return ToolResult{Content: fmt.Sprintf("No callers of %q in the index — unused, or defined outside the indexed files.", symbol) + stale}, nil
		}
		return ToolResult{Content: fmt.Sprintf("Callers of %s:", symbol) + "\n" + strings.Join(out, "\n") + stale}, nil

	case "callees":
		if t.Callees == nil {
			return notWired("callees"), nil
		}
		level1, err := t.Callees(symbol)
		if err != nil {
			return errResult("index callee lookup failed: %v", err), nil
		}
		seen := map[string]bool{}
		var out []string
		addCallee := func(name string, hop int) {
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			line := name
			if hop == 2 {
				line += " (hop 2)"
			}
			out = append(out, line)
		}
		for _, e := range level1 {
			if e.Kind == "call" {
				addCallee(e.ToName, 1)
			}
		}
		if distance == 2 {
			for _, e := range level1 {
				if e.Kind != "call" || e.ToName == symbol {
					continue
				}
				next, err := t.Callees(e.ToName)
				if err != nil {
					return errResult("index callee lookup failed: %v", err), nil
				}
				for _, e2 := range next {
					if e2.Kind == "call" {
						addCallee(e2.ToName, 2)
					}
				}
			}
		}
		if len(out) == 0 {
			return ToolResult{Content: fmt.Sprintf("No calls attributed to %q in the index.", symbol) + stale}, nil
		}
		return ToolResult{Content: fmt.Sprintf("Callees of %s:", symbol) + "\n" + strings.Join(out, "\n") + stale}, nil

	default:
		return errResult("unknown direction %q — use definitions, callers, or callees", direction), nil
	}
}

func notWired(direction string) ToolResult {
	return ToolResult{
		Content: "the codebase index is not wired in this session — use grep_codebase to find call sites",
		IsError: true,
	}
}
