package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

type WebSearch struct {
	HTTPClient *http.Client
	Cache      map[string]SearchCacheEntry
}

type SearchCacheEntry struct {
	Results   []SearchResult
	Timestamp time.Time
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (t *WebSearch) Name() string { return "web_search" }

func (t *WebSearch) Description() string {
	return "Search the web for information. Use for looking up documentation, APIs, and best practices."
}

func (t *WebSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query"
			}
		},
		"required": ["query"]
	}`)
}

type WebSearchArgs struct {
	Query string `json:"query"`
}

func (t *WebSearch) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a WebSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	if t.Cache == nil {
		t.Cache = make(map[string]SearchCacheEntry)
	}

	if entry, ok := t.Cache[a.Query]; ok {
		if time.Since(entry.Timestamp) < 24*time.Hour {
			data, _ := json.MarshalIndent(entry.Results, "", "  ")
			return ToolResult{Content: string(data)}, nil
		}
	}

	results := []SearchResult{
		{
			Title:   "Search result for: " + a.Query,
			URL:     "https://example.com/search?q=" + url.QueryEscape(a.Query),
			Snippet: "This is a placeholder result. In production, this would call a real search API.",
		},
	}

	t.Cache[a.Query] = SearchCacheEntry{
		Results:   results,
		Timestamp: time.Now(),
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return ToolResult{Content: string(data)}, nil
}

type GitHubSearch struct {
	HTTPClient *http.Client
	Cache      map[string]SearchCacheEntry
}

func (t *GitHubSearch) Name() string { return "github_search" }

func (t *GitHubSearch) Description() string {
	return "Search GitHub for code examples. Scoped to Solana Mobile repositories first."
}

func (t *GitHubSearch) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Search query"
			},
			"repo": {
				"type": "string",
				"description": "Optional repository to search in (e.g., 'solana-mobile/mobile-wallet-adapter')"
			}
		},
		"required": ["query"]
	}`)
}

type GitHubSearchArgs struct {
	Query string `json:"query"`
	Repo  string `json:"repo"`
}

func (t *GitHubSearch) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	var a GitHubSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{Content: "invalid arguments", IsError: true}, err
	}

	if t.Cache == nil {
		t.Cache = make(map[string]SearchCacheEntry)
	}

	cacheKey := a.Query + ":" + a.Repo
	if entry, ok := t.Cache[cacheKey]; ok {
		if time.Since(entry.Timestamp) < 24*time.Hour {
			data, _ := json.MarshalIndent(entry.Results, "", "  ")
			return ToolResult{Content: string(data)}, nil
		}
	}

	results := []SearchResult{
		{
			Title:   "GitHub search result for: " + a.Query,
			URL:     "https://github.com/search?q=" + url.QueryEscape(a.Query),
			Snippet: "This is a placeholder result. In production, this would call the GitHub API.",
		},
	}

	t.Cache[cacheKey] = SearchCacheEntry{
		Results:   results,
		Timestamp: time.Now(),
	}

	data, _ := json.MarshalIndent(results, "", "  ")
	return ToolResult{Content: string(data)}, nil
}
