package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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

	if a.Query == "" {
		return ToolResult{Content: "query is required", IsError: true}, fmt.Errorf("query is required")
	}

	// Use DuckDuckGo HTML search (no API key needed)
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(a.Query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SeekerStudio/1.0)")

	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ToolResult{Content: fmt.Sprintf("search failed: %v", err), IsError: true}, err
	}

	// Parse results from HTML
	results := parseDuckDuckGoResults(string(body))

	// Format output
	var formatted strings.Builder
	formatted.WriteString(fmt.Sprintf("Search results for: %s\n\n", a.Query))
	for i, r := range results {
		if i >= 5 {
			break
		}
		formatted.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return ToolResult{Content: formatted.String()}, nil
}

func parseDuckDuckGoResults(html string) []SearchResult {
	var results []SearchResult

	lines := strings.Split(html, "\n")
	var currentTitle, currentURL, currentSnippet string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "result__a") {
			if idx := strings.Index(line, "href=\""); idx != -1 {
				endIdx := strings.Index(line[idx+6:], "\"")
				if endIdx != -1 {
					currentURL = line[idx+6 : idx+6+endIdx]
				}
			}
			if idx := strings.Index(line, ">"); idx != -1 {
				endIdx := strings.Index(line[idx+1:], "<")
				if endIdx != -1 {
					currentTitle = strings.TrimSpace(line[idx+1 : idx+1+endIdx])
				}
			}
		}

		if strings.Contains(line, "result__snippet") {
			if idx := strings.Index(line, ">"); idx != -1 {
				endIdx := strings.Index(line[idx+1:], "<")
				if endIdx != -1 {
					currentSnippet = strings.TrimSpace(line[idx+1 : idx+1+endIdx])
				}
			}
		}

		if currentTitle != "" && currentURL != "" {
			results = append(results, SearchResult{
				Title:   currentTitle,
				URL:     currentURL,
				Snippet: currentSnippet,
			})
			currentTitle = ""
			currentURL = ""
			currentSnippet = ""
		}
	}

	return results
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
