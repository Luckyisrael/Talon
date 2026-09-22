// Package embed is W4.3's embedding provider behind an interface: an
// API-based default that speaks the OpenAI-compatible /v1/embeddings shape,
// and an explicit opt-out — unconfigured means nil, which means the vector
// layer simply doesn't exist and retrieval stays FTS/grep-only. No key, no
// network call, no hard dependency (the plan's risk-mitigation contract).
//
// Local (ONNX) is a deliberate slot on the same interface, not a stub: the
// build stays pure-Go (modernc sqlite, no cgo — W4.1's law), and shipping a
// fake "local" embedder that emits deterministic-but-meaningless vectors
// would lie about retrieval quality. Whoever owns the native dependency
// implements Provider; nothing else changes.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Provider turns texts into vectors. The store depends on this shape only —
// it declares its own structurally-identical Embedder, so neither side
// imports the other.
type Provider interface {
	Name() string
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Environment the default provider reads (documented in .env.example).
const (
	EnvURL   = "TALONSTUDIO_EMBED_URL"
	EnvKey   = "TALONSTUDIO_EMBED_KEY"
	EnvModel = "TALONSTUDIO_EMBED_MODEL"
)

// DefaultModel matches the endpoint most OpenAI-compatible providers expose.
const DefaultModel = "text-embedding-3-small"

// defaultBatch keeps one request well under every provider's input cap —
// chunks are ≤4KiB (≈1k tokens), so 64 × 1k tokens stays conservative.
const defaultBatch = 64

// FromEnv builds the API default, or nil when unconfigured. URL is the only
// required variable (keyless local endpoints exist), the model defaults, and
// every nil caller must treat as "vector layer off" — never as an error.
func FromEnv() Provider {
	endpoint := strings.TrimSpace(os.Getenv(EnvURL))
	if endpoint == "" {
		return nil
	}
	model := strings.TrimSpace(os.Getenv(EnvModel))
	if model == "" {
		model = DefaultModel
	}
	return &API{
		Endpoint: endpoint,
		APIKey:   strings.TrimSpace(os.Getenv(EnvKey)),
		Model:    model,
	}
}

// API is the default provider: POST {model, input:[…]} against any
// OpenAI-compatible embeddings endpoint. Batched, context-respecting, and
// strict about response shape — an endpoint returning the wrong count,
// duplicate indices, or mixed dimensions is an error, never a silent
// misalignment (misaligned vectors would poison content-addressed cache
// rows and keep being served long after).
type API struct {
	Endpoint string
	APIKey   string
	Model    string
	Batch    int // texts per request; <=0 means defaultBatch
	HTTP     *http.Client
}

func (a *API) Name() string { return "api:" + a.Model }

func (a *API) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if a.Endpoint == "" {
		return nil, fmt.Errorf("embed: no endpoint configured")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	batch := a.Batch
	if batch <= 0 {
		batch = defaultBatch
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := a.embedOnce(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("embed batch starting at %d: %w", start, err)
		}
		if len(vecs) != end-start {
			return nil, fmt.Errorf("embed: endpoint returned %d vectors for %d inputs", len(vecs), end-start)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

type apiRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type apiItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type apiResponse struct {
	Data []apiItem `json:"data"`
}

func (a *API) embedOnce(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(apiRequest{Model: a.Model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("embed: endpoint status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var parsed apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("embed: decoding response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embed: endpoint returned %d items for %d inputs", len(parsed.Data), len(texts))
	}
	// The contract says data[i].index == input position. Accept in-place
	// delivery, sort when an endpoint delivers out of order, and reject
	// anything ambiguous — a permutation we can't trust is a misalignment.
	inPlace := true
	for i, item := range parsed.Data {
		if item.Index != i {
			inPlace = false
			break
		}
	}
	if !inPlace {
		items := make([]apiItem, len(parsed.Data))
		copy(items, parsed.Data)
		sort.Slice(items, func(x, y int) bool { return items[x].Index < items[y].Index })
		for i, item := range items {
			if item.Index != i {
				return nil, fmt.Errorf("embed: untrustworthy indices (position %d carries index %d)", i, item.Index)
			}
		}
		parsed.Data = items
	}
	out := make([][]float32, len(texts))
	dims := -1
	for i, item := range parsed.Data {
		if dims == -1 {
			dims = len(item.Embedding)
		} else if len(item.Embedding) != dims {
			return nil, fmt.Errorf("embed: mixed vector dimensions (%d then %d)", dims, len(item.Embedding))
		}
		out[i] = item.Embedding
	}
	if dims <= 0 {
		return nil, fmt.Errorf("embed: endpoint returned empty vectors")
	}
	return out, nil
}
