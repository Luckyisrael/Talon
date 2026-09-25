package tools

import (
	"context"
	"strings"
	"testing"
)

// stubSearcher records what the tool asked for and returns preset hits.
type stubSearcher struct {
	hits      []PluginSearchHit
	err       error
	gotQuery  string
	gotPins   map[string]string
	callCount int
}

func (s *stubSearcher) Search(query string, pins map[string]string) ([]PluginSearchHit, error) {
	s.gotQuery, s.gotPins, s.callCount = query, pins, s.callCount+1
	return s.hits, s.err
}

// stubPinReader stands in for .talon/plugins.json.
type stubPinReader struct {
	pins map[string]string
	err  error
}

func (s stubPinReader) ReadPins(root string) (map[string]string, error) { return s.pins, s.err }

func searchTool(s PluginSearcher, pins map[string]string) *SearchPlugins {
	return &SearchPlugins{WorkspaceRoot: "/ws", Searcher: s, Pins: stubPinReader{pins: pins}}
}

// The happy path: ranked matches, why they matched, pin state, and the
// exact enable step — metadata only, no bodies anywhere.
func TestSearchPluginsFormatsMatches(t *testing.T) {
	stub := &stubSearcher{hits: []PluginSearchHit{{
		Name: "moove-payment-api", Version: "1.0.0",
		Description: "Card and transfer payments.", License: "MIT",
		Matched: []string{"payment"}, Pinned: false,
	}}}
	res, err := stubSearcherToolExecute(t, stub, map[string]string{"other": "1.0.0"}, "card payment")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	for _, want := range []string{
		"moove-payment-api@1.0.0",
		"MIT",
		"matched: payment",
		"card payment",
		".talon/plugins.json",
		"read_skill",
	} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("output missing %q:\n%s", want, res.Content)
		}
	}
	if stub.gotQuery != "card payment" {
		t.Errorf("query forwarded = %q", stub.gotQuery)
	}
	if stub.gotPins["other"] != "1.0.0" {
		t.Errorf("workspace pins must reach the searcher, got %v", stub.gotPins)
	}
}

// stubSearcherToolExecute wires a stub pins reader and runs the tool.
func stubSearcherToolExecute(t *testing.T, s PluginSearcher, pins map[string]string, query string) (ToolResult, error) {
	t.Helper()
	return searchTool(s, pins).Execute(context.Background(), rawArgs(t, map[string]any{"query": query}))
}

// An already-pinned hit says so — search never tells the model to enable
// what is enabled.
func TestSearchPluginsReportsPinned(t *testing.T) {
	stub := &stubSearcher{hits: []PluginSearchHit{{
		Name: "talon-mobile-ui", Version: "1.0.0",
		Description: "Wallet bits.", Pinned: true,
	}}}
	res, err := stubSearcherToolExecute(t, stub, map[string]string{"talon-mobile-ui": "1.0.0"}, "wallet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "already pinned") {
		t.Errorf("pinned hit must say so:\n%s", res.Content)
	}
}

// No match is an honest stop, not an invitation to keep searching.
func TestSearchPluginsNoMatchStops(t *testing.T) {
	res, err := stubSearcherToolExecute(t, &stubSearcher{}, nil, "insurance underwriting")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("empty result is not an error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "do not keep searching") {
		t.Errorf("no-match output must stop the search loop:\n%s", res.Content)
	}
}

// An unreachable directory degrades to an honest error that preserves
// what still works (pinned plugins from cache).
func TestSearchPluginsUnreachableDegrades(t *testing.T) {
	res, err := stubSearcherToolExecute(t, &stubSearcher{err: context.DeadlineExceeded}, nil, "wallet")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("searcher failure must surface as a tool error")
	}
	if !strings.Contains(res.Content, "unreachable") || !strings.Contains(res.Content, "cache") {
		t.Errorf("error should name unreachability and what still works: %s", res.Content)
	}
}

// Unconfigured and malformed inputs degrade instead of panicking, and
// search is read-only so it runs alongside other reads in a turn.
func TestSearchPluginsGuards(t *testing.T) {
	if !(&SearchPlugins{}).ReadOnly() {
		t.Error("search_plugins must be read-only")
	}
	res, _ := (&SearchPlugins{Pins: stubPinReader{}}).Execute(context.Background(), rawArgs(t, map[string]any{"query": "x"}))
	if !res.IsError || !strings.Contains(res.Content, "not configured") {
		t.Errorf("nil searcher must degrade honestly, got: %s", res.Content)
	}
	bad, _ := searchTool(&stubSearcher{}, nil).Execute(context.Background(), rawArgs(t, map[string]any{"query": 42}))
	if !bad.IsError {
		t.Error("a non-string query must be an error, not a lookup")
	}
	pinsErr, _ := (&SearchPlugins{Searcher: &stubSearcher{}, Pins: stubPinReader{err: context.Canceled}}).
		Execute(context.Background(), rawArgs(t, map[string]any{"query": "x"}))
	if !pinsErr.IsError || !strings.Contains(pinsErr.Content, "pins") {
		t.Errorf("pin read failure must surface: %s", pinsErr.Content)
	}
}
