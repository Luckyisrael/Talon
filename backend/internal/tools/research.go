package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type WebSearch struct {
	HTTPClient *http.Client
	CacheDir   string
}

func (t *WebSearch) Name() string { return "web_search" }

func (t *WebSearch) Description() string {
	return "Search the web for documentation, API references, or examples. Results are cached per query for the session lifetime."
}

func (t *WebSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query (e.g. 'react-native-maps marker example')"
			}
		},
		"required": ["query"]
	}`)
}

type WebSearchArgs struct {
	Query string `json:"query"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (t *WebSearch) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WebSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	a.Query = strings.TrimSpace(a.Query)
	if a.Query == "" {
		return ToolResult{Content: "query is required", IsError: true}, fmt.Errorf("query is required")
	}

	// DuckDuckGo's HTML endpoint needs no API key.
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(a.Query)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SeekerStudio/1.0)")
	req.Header.Set("Accept", "text/html")

	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}
	defer resp.Body.Close()

	// A rate-limited or blocked request returns a perfectly valid HTML page with
	// no results in it. Treated as a 200 it looks identical to "nothing found".
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("search unavailable: DuckDuckGo returned HTTP %d. Rely on what you already know, or read a specific URL with fetch_url.", resp.StatusCode)
		return ToolResult{Content: msg, IsError: true}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBytes))
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}

	results := parseDuckDuckGoResults(string(body))
	if len(results) == 0 {
		// Reporting an empty success here is how a scraper breaks silently: the
		// agent reads "no results" as fact about the world rather than a broken
		// tool, and quietly proceeds on whatever it guessed instead.
		return ToolResult{
			Content: fmt.Sprintf("no results could be read for %q — the search page returned nothing this parser recognised, which usually means DuckDuckGo changed its markup or throttled the request. Do not treat this as evidence that nothing exists.", a.Query),
			IsError: true,
		}, nil
	}

	var formatted strings.Builder
	fmt.Fprintf(&formatted, "Search results for: %s\n\n", a.Query)
	for i, r := range results {
		if i >= maxSearchResults {
			break
		}
		fmt.Fprintf(&formatted, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&formatted, "   %s\n", r.Snippet)
		}
		formatted.WriteByte('\n')
	}

	return ToolResult{Content: formatted.String()}, nil
}

const (
	maxSearchBytes   = 4 << 20
	maxSearchResults = 5
)

var (
	// DuckDuckGo's HTML is served minified, so the previous line-by-line scan
	// found at most one result per document and usually none at all. These match
	// across the whole body instead.
	ddgResultRe  = regexp.MustCompile(`(?is)class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?is)class="result__snippet"[^>]*>(.*?)</a>`)
	htmlTagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
)

func parseDuckDuckGoResults(body string) []SearchResult {
	matches := ddgResultRe.FindAllStringSubmatch(body, maxSearchResults*4)
	snippets := ddgSnippetRe.FindAllStringSubmatch(body, maxSearchResults*4)

	results := make([]SearchResult, 0, len(matches))
	for i, m := range matches {
		link := unwrapDDGLink(html.UnescapeString(m[1]))
		title := cleanHTML(m[2])
		if link == "" || title == "" {
			continue
		}
		var snippet string
		if i < len(snippets) {
			snippet = cleanHTML(snippets[i][1])
		}
		results = append(results, SearchResult{Title: title, URL: link, Snippet: snippet})
	}
	return results
}

// unwrapDDGLink pulls the real destination out of DuckDuckGo's redirector, which
// wraps every result as //duckduckgo.com/l/?uddg=<percent-encoded target>.
// Handing those to the agent gave it links it could not fetch.
func unwrapDDGLink(href string) string {
	if strings.Contains(href, "uddg=") {
		if parsed, err := url.Parse(href); err == nil {
			if target := parsed.Query().Get("uddg"); target != "" {
				href = target
			}
		}
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
		return ""
	}
	return href
}

func cleanHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

type GitHubSearch struct {
	HTTPClient *http.Client
	CacheDir   string
}

func (t *GitHubSearch) Name() string { return "github_search" }

func (t *GitHubSearch) Description() string {
	return "Search GitHub for code examples, API usage patterns, or implementations. Scoped to known-good repos first."
}

func (t *GitHubSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query (e.g. 'mobile-wallet-adapter connect')"
			},
			"repo": {
				"type": "string",
				"description": "Optional repo to scope search (e.g. 'solana-mobile/mobile-wallet-adapter')"
			}
		},
		"required": ["query"]
	}`)
}

type GitHubSearchArgs struct {
	Query string `json:"query"`
	Repo  string `json:"repo"`
}

type GitHubResult struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Repository string `json:"repository"`
	URL        string `json:"url"`
}

func (t *GitHubSearch) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a GitHubSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	if a.Query == "" {
		return ToolResult{Content: "query is required", IsError: true}, fmt.Errorf("query is required")
	}

	// Build search query
	searchQuery := a.Query
	if a.Repo != "" {
		searchQuery = fmt.Sprintf("%s repo:%s", a.Query, a.Repo)
	}

	// Search GitHub API (unauthenticated, rate-limited)
	searchURL := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=5", url.QueryEscape(searchQuery))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("GitHub search failed: %v", err), IsError: true}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "SeekerStudio/1.0")

	// Use GitHub token if available
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("GitHub search failed: %v", err), IsError: true}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("GitHub search failed: %v", err), IsError: true}, err
	}

	// Parse JSON response
	var apiResp struct {
		Items []struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			HTMLURL string `json:"html_url"`
			Repo    struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return ToolResult{Content: fmt.Sprintf("failed to parse GitHub response: %v", err), IsError: true}, err
	}

	// Format results
	var formatted strings.Builder
	formatted.WriteString(fmt.Sprintf("GitHub search results for: %s\n\n", a.Query))
	for i, item := range apiResp.Items {
		if i >= 5 {
			break
		}
		formatted.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n   %s\n\n", i+1, item.Name, item.Path, item.Repo.FullName, item.HTMLURL))
	}

	return ToolResult{Content: formatted.String()}, nil
}
