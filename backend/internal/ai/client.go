package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Provider string

const (
	ProtocolAnthropic Provider = "anthropic"
	ProtocolOpenAI    Provider = "openai"
)

type Client struct {
	protocol     Provider
	baseURL      string
	apiKey       string
	model        string
	httpClient   *http.Client
	workspaceDir string
}

// ProviderConfig holds all configuration for an AI provider.
// All fields map 1:1 to environment variables.
type ProviderConfig struct {
	Protocol string // AI_PROTOCOL: "anthropic" or "openai"
	BaseURL  string // AI_BASE_URL
	APIKey   string // AI_API_KEY
	Model    string // AI_MODEL
}

func loadConfig() ProviderConfig {
	return ProviderConfig{
		Protocol: os.Getenv("AI_PROTOCOL"),
		BaseURL:  os.Getenv("AI_BASE_URL"),
		APIKey:   os.Getenv("AI_API_KEY"),
		Model:    os.Getenv("AI_MODEL"),
	}
}

func (c ProviderConfig) validate() error {
	var missing []string
	if c.BaseURL == "" {
		missing = append(missing, "AI_BASE_URL")
	}
	if c.APIKey == "" {
		missing = append(missing, "AI_API_KEY")
	}
	if c.Model == "" {
		missing = append(missing, "AI_MODEL")
	}
	if c.Protocol == "" {
		missing = append(missing, "AI_PROTOCOL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if c.Protocol != string(ProtocolAnthropic) && c.Protocol != string(ProtocolOpenAI) {
		return fmt.Errorf("AI_PROTOCOL must be 'anthropic' or 'openai', got '%s'", c.Protocol)
	}
	return nil
}

func New() (*Client, error) {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir = os.TempDir()
	}

	return &Client{
		protocol: Provider(cfg.Protocol),
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		// Generous: an agentic turn asks for up to 16k output tokens and a
		// reasoning model can spend minutes on one before the first byte.
		httpClient:   &http.Client{Timeout: 10 * time.Minute},
		workspaceDir: workspaceDir,
	}, nil
}

// Model reports the configured model id, for logging and usage accounting.
func (c *Client) Model() string { return c.model }

type PlanResponse struct {
	Message string   `json:"message"`
	Plan    *AppPlan `json:"plan,omitempty"`
}

type AppPlan struct {
	Screens    []Screen `json:"screens"`
	Features   []string `json:"features"`
	Navigation string   `json:"navigation"`
	Packages   []string `json:"packages"`
}

type Screen struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type PromptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Anthropic Messages API request
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Anthropic Messages API response
type anthropicResponse struct {
	Content []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// OpenAI-compatible request (for OpenRouter)
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Plan runs one Plan-mode turn and parses the structured plan out of the reply.
// The model is asked for JSON; if the first attempt is not parseable it gets one
// chance to correct itself, because a malformed reply loses the plan entirely.
func (c *Client) Plan(ctx context.Context, history []PromptMessage) (*PlanResponse, error) {
	resp, err := c.chat(ctx, SystemPromptPlan, history)
	if err != nil {
		return nil, err
	}

	if plan, ok := parsePlan(resp); ok {
		return plan, nil
	}

	retry := make([]PromptMessage, 0, len(history)+2)
	retry = append(retry, history...)
	retry = append(retry, PromptMessage{Role: "assistant", Content: resp})
	retry = append(retry, PromptMessage{
		Role: "user",
		Content: "That was not valid JSON. Send the same reply again as a single JSON object " +
			"with \"message\" and \"plan\" keys, and nothing outside the object.",
	})

	resp2, err := c.chat(ctx, SystemPromptPlan, retry)
	if err != nil {
		// The first reply is still worth showing even though the plan is lost.
		return &PlanResponse{Message: StripCodeBlocks(resp)}, nil
	}
	if plan, ok := parsePlan(resp2); ok {
		return plan, nil
	}

	// Both attempts were prose. Show it rather than failing the request — the
	// text is usually a perfectly good answer that simply was not wrapped.
	return &PlanResponse{Message: StripCodeBlocks(resp2)}, nil
}

// parsePlan reports whether a reply is a usable PlanResponse.
func parsePlan(resp string) (*PlanResponse, bool) {
	var p PlanResponse
	if err := json.Unmarshal([]byte(StripCodeBlocks(resp)), &p); err != nil {
		return nil, false
	}
	if p.Message == "" && p.Plan == nil {
		return nil, false
	}
	return &p, true
}

// StripCodeBlocks removes markdown code block wrappers from AI responses.
func StripCodeBlocks(s string) string {
	s = strings.TrimSpace(s)

	// Remove opening code block (```json, ```tsx, ```typescript, etc.)
	if strings.HasPrefix(s, "```") {
		// Find end of first line
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
	}

	// Remove closing code block
	if strings.HasSuffix(s, "```") {
		s = s[:len(s)-3]
	}

	return strings.TrimSpace(s)
}

func (c *Client) chat(ctx context.Context, system string, history []PromptMessage) (string, error) {
	if c.protocol == ProtocolOpenAI {
		return c.chatOpenAI(ctx, system, history)
	}
	return c.chatAnthropic(ctx, system, history)
}

func (c *Client) chatAnthropic(ctx context.Context, system string, history []PromptMessage) (string, error) {
	messages := make([]anthropicMessage, 0, len(history))
	for _, h := range history {
		messages = append(messages, anthropicMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:     c.model,
		MaxTokens: 8192,
		System:    system,
		Messages:  messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("chatAnthropic: retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		url := c.baseURL + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", "SeekerStudio/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("chatAnthropic: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return "", fmt.Errorf("Connection failed. The AI service may be temporarily unavailable. Please try again.")
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			if attempt < maxRetries-1 {
				log.Printf("chatAnthropic: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
			switch resp.StatusCode {
			case 429:
				return "", fmt.Errorf("Rate limit exceeded. The AI service is receiving too many requests. Please wait a moment and try again.")
			case 502:
				return "", fmt.Errorf("AI service temporarily unavailable. This usually resolves quickly — please try again.")
			case 503:
				return "", fmt.Errorf("AI service is currently overloaded. Please wait a moment and try again.")
			case 504:
				return "", fmt.Errorf("AI service request timed out. The service may be under heavy load — please try again.")
			default:
				return "", fmt.Errorf("AI service error (HTTP %d). Please try again.", resp.StatusCode)
			}
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		var anthResp anthropicResponse
		if err := json.Unmarshal(respBody, &anthResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if anthResp.Error != nil {
			return "", fmt.Errorf("API error: %s", anthResp.Error.Message)
		}

		if len(anthResp.Content) == 0 {
			return "", fmt.Errorf("empty response from AI")
		}

		// Find the text block (skip thinking and tool_use blocks)
		for _, block := range anthResp.Content {
			if block.Type == "text" && block.Text != "" {
				return block.Text, nil
			}
		}

		// Fallback: some providers return only thinking blocks with no text.
		// Concatenate thinking content as a last resort.
		var thinkingParts []string
		for _, block := range anthResp.Content {
			if block.Type == "thinking" && block.Thinking != "" {
				thinkingParts = append(thinkingParts, block.Thinking)
			}
		}
		if len(thinkingParts) > 0 {
			return strings.Join(thinkingParts, "\n"), nil
		}

		return "", fmt.Errorf("no text content in AI response")
	}

	return "", fmt.Errorf("max retries exceeded")
}

func (c *Client) chatOpenAI(ctx context.Context, system string, history []PromptMessage) (string, error) {
	messages := []openaiMessage{
		{Role: "system", Content: system},
	}
	for _, h := range history {
		messages = append(messages, openaiMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	reqBody := openaiRequest{
		Model:    c.model,
		Messages: messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("chatOpenAI: retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		url := c.baseURL + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://seekerstudio.app")
		req.Header.Set("X-Title", "SeekerStudio")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("chatOpenAI: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return "", fmt.Errorf("Connection failed. The AI service may be temporarily unavailable. Please try again.")
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			if attempt < maxRetries-1 {
				log.Printf("chatOpenAI: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
			switch resp.StatusCode {
			case 429:
				return "", fmt.Errorf("Rate limit exceeded. The AI service is receiving too many requests. Please wait a moment and try again.")
			case 502:
				return "", fmt.Errorf("AI service temporarily unavailable. This usually resolves quickly — please try again.")
			case 503:
				return "", fmt.Errorf("AI service is currently overloaded. Please wait a moment and try again.")
			case 504:
				return "", fmt.Errorf("AI service request timed out. The service may be under heavy load — please try again.")
			default:
				return "", fmt.Errorf("AI service error (HTTP %d). Please try again.", resp.StatusCode)
			}
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		var oaiResp openaiResponse
		if err := json.Unmarshal(respBody, &oaiResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if oaiResp.Error != nil {
			return "", fmt.Errorf("API error: %s", oaiResp.Error.Message)
		}

		if len(oaiResp.Choices) == 0 {
			return "", fmt.Errorf("empty response from AI")
		}

		return oaiResp.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("max retries exceeded")
}

// ── Streaming ─────────────────────────────────────────

// StreamEvent represents a single event in a streaming AI response.
type StreamEvent struct {
	Type  string      `json:"type"`            // thinking.start, text.delta, tool.start, etc.
	Data  interface{} `json:"data,omitempty"`  // event-specific payload
	Error string      `json:"error,omitempty"` // error message if type is "error"
}

// StreamTextDelta is the payload for text.delta and thinking.delta events.
type StreamTextDelta struct {
	Text string `json:"text"`
}

// StreamToolStart is the payload for tool.start events.
type StreamToolStart struct {
	Name  string      `json:"name"`            // e.g., "write_file"
	Input interface{} `json:"input,omitempty"` // e.g., {"path": "src/App.tsx"}
}

// StreamToolEnd is the payload for tool.end events.
type StreamToolEnd struct {
	Name   string `json:"name"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ChatStream streams a plain-text (no tools) response and returns a channel of
// events. The channel is closed when the stream ends or fails; an "error" event
// is always sent before closing on failure, so a consumer that only watches for
// "done" will not hang.
func (c *Client) ChatStream(ctx context.Context, system string, history []PromptMessage) <-chan StreamEvent {
	events := make(chan StreamEvent, 64)

	go func() {
		defer close(events)
		if c.protocol == ProtocolOpenAI {
			c.streamOpenAI(ctx, system, history, events)
			return
		}
		c.streamAnthropic(ctx, system, history, events)
	}()

	return events
}

// emit sends an event unless the caller has gone away. Without the ctx guard a
// disconnected client would block the producer goroutine forever once the
// buffer filled.
func emit(ctx context.Context, events chan<- StreamEvent, e StreamEvent) bool {
	select {
	case events <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

func emitStreamError(ctx context.Context, events chan<- StreamEvent, format string, args ...interface{}) {
	emit(ctx, events, StreamEvent{Type: "error", Error: fmt.Sprintf(format, args...)})
}

// openStream issues the request and hands back the body for SSE reading. The
// caller closes it.
func (c *Client) openStream(ctx context.Context, url string, body []byte, setHeaders func(*http.Request)) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the AI service: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("AI service returned HTTP %d: %s", resp.StatusCode, truncate(string(detail), 500))
	}
	return resp.Body, nil
}

// ── OpenAI-compatible streaming ───────────────────────

// streamOpenAI reads a Chat Completions stream. These streams carry no `event:`
// lines — every frame is a bare `data: {...}` — so the scanner must surface
// data-only events or nothing arrives at all.
func (c *Client) streamOpenAI(ctx context.Context, system string, history []PromptMessage, events chan<- StreamEvent) {
	messages := make([]openaiMessage, 0, len(history)+1)
	if system != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: system})
	}
	for _, h := range history {
		messages = append(messages, openaiMessage{Role: h.Role, Content: h.Content})
	}

	reqBody := map[string]interface{}{
		"model":      c.model,
		"messages":   messages,
		"stream":     true,
		"max_tokens": maxOutputTokens,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		emitStreamError(ctx, events, "could not encode the request: %v", err)
		return
	}

	respBody, err := c.openStream(ctx, c.baseURL+"/chat/completions", body, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		if strings.Contains(c.baseURL, "openrouter") {
			req.Header.Set("HTTP-Referer", "https://seekerstudio.dev")
			req.Header.Set("X-Title", "Seeker Studio")
		}
	})
	if err != nil {
		emitStreamError(ctx, events, "%v", err)
		return
	}
	defer respBody.Close()

	// Reasoning and prose are separate streams to the UI, and each has to be
	// opened before its first delta and closed before the other opens.
	const (
		phaseNone = iota
		phaseThinking
		phaseText
	)
	phase := phaseNone

	closePhase := func() bool {
		switch phase {
		case phaseThinking:
			phase = phaseNone
			return emit(ctx, events, StreamEvent{Type: "thinking.end"})
		case phaseText:
			phase = phaseNone
			return emit(ctx, events, StreamEvent{Type: "text.end"})
		}
		return true
	}

	openPhase := func(want int) bool {
		if phase == want {
			return true
		}
		if !closePhase() {
			return false
		}
		phase = want
		if want == phaseThinking {
			return emit(ctx, events, StreamEvent{Type: "thinking.start"})
		}
		return emit(ctx, events, StreamEvent{Type: "text.start"})
	}

	scanner := newSSEScanner(respBody)
	for scanner.Next() {
		event := scanner.Event()
		if len(event.Data) == 0 {
			continue
		}
		// The sentinel is not JSON; it marks a clean end of stream.
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			// Keep-alives and provider-specific frames are not fatal.
			continue
		}
		if chunk.Error != nil {
			closePhase()
			emitStreamError(ctx, events, "%s", chunk.Error.Message)
			return
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		reasoning := choice.Delta.Reasoning
		if reasoning == "" {
			reasoning = choice.Delta.ReasoningContent
		}
		if reasoning != "" {
			if !openPhase(phaseThinking) || !emit(ctx, events,
				StreamEvent{Type: "thinking.delta", Data: StreamTextDelta{Text: reasoning}}) {
				return
			}
		}

		if choice.Delta.Content != "" {
			if !openPhase(phaseText) || !emit(ctx, events,
				StreamEvent{Type: "text.delta", Data: StreamTextDelta{Text: choice.Delta.Content}}) {
				return
			}
		}

		if choice.FinishReason != "" {
			break
		}
	}

	if err := scanner.Err(); err != nil {
		closePhase()
		emitStreamError(ctx, events, "the AI stream ended early: %v", err)
		return
	}

	if !closePhase() {
		return
	}
	emit(ctx, events, StreamEvent{Type: "done"})
}

// ── Anthropic streaming ───────────────────────────────

// streamAnthropic reads a Messages API stream. Unlike the OpenAI format this
// one names every event, and content blocks are indexed — so the block type is
// tracked per index to close the right stream on content_block_stop.
func (c *Client) streamAnthropic(ctx context.Context, system string, history []PromptMessage, events chan<- StreamEvent) {
	messages := make([]anthropicMessage, 0, len(history))
	for _, h := range history {
		messages = append(messages, anthropicMessage{Role: h.Role, Content: h.Content})
	}

	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": maxOutputTokens,
		"messages":   messages,
		"stream":     true,
	}
	if system != "" {
		reqBody["system"] = system
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		emitStreamError(ctx, events, "could not encode the request: %v", err)
		return
	}

	respBody, err := c.openStream(ctx, c.baseURL+"/messages", body, func(req *http.Request) {
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	})
	if err != nil {
		emitStreamError(ctx, events, "%v", err)
		return
	}
	defer respBody.Close()

	// blockKinds maps a content-block index to the event family it belongs to,
	// so content_block_stop can close the stream it actually opened.
	blockKinds := make(map[int]string, 4)

	scanner := newSSEScanner(respBody)
	for scanner.Next() {
		event := scanner.Event()
		if len(event.Data) == 0 {
			continue
		}

		switch event.Type {
		case "content_block_start":
			var payload struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			switch payload.ContentBlock.Type {
			case "thinking", "redacted_thinking":
				blockKinds[payload.Index] = "thinking"
				if !emit(ctx, events, StreamEvent{Type: "thinking.start"}) {
					return
				}
			case "tool_use":
				blockKinds[payload.Index] = "tool"
				if !emit(ctx, events, StreamEvent{
					Type: "tool.start",
					Data: StreamToolStart{Name: payload.ContentBlock.Name},
				}) {
					return
				}
			default:
				blockKinds[payload.Index] = "text"
				if !emit(ctx, events, StreamEvent{Type: "text.start"}) {
					return
				}
			}

		case "content_block_delta":
			var payload struct {
				Index int `json:"index"`
				Delta struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					Thinking string `json:"thinking"`
				} `json:"delta"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			switch payload.Delta.Type {
			case "thinking_delta":
				if !emit(ctx, events, StreamEvent{
					Type: "thinking.delta",
					Data: StreamTextDelta{Text: payload.Delta.Thinking},
				}) {
					return
				}
			case "text_delta":
				if !emit(ctx, events, StreamEvent{
					Type: "text.delta",
					Data: StreamTextDelta{Text: payload.Delta.Text},
				}) {
					return
				}
			}

		case "content_block_stop":
			var payload struct {
				Index int `json:"index"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			kind := blockKinds[payload.Index]
			delete(blockKinds, payload.Index)
			switch kind {
			case "thinking":
				if !emit(ctx, events, StreamEvent{Type: "thinking.end"}) {
					return
				}
			case "tool":
				// Tool results come from the executor, not the stream.
			default:
				if !emit(ctx, events, StreamEvent{Type: "text.end"}) {
					return
				}
			}

		case "error":
			var payload struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			msg := "the AI service reported an error"
			if json.Unmarshal(event.Data, &payload) == nil && payload.Error.Message != "" {
				msg = payload.Error.Message
			}
			emitStreamError(ctx, events, "%s", msg)
			return

		case "message_stop":
			emit(ctx, events, StreamEvent{Type: "done"})
			return
		}
	}

	if err := scanner.Err(); err != nil {
		emitStreamError(ctx, events, "the AI stream ended early: %v", err)
		return
	}

	// The stream closed without message_stop. Still finish cleanly so the client
	// stops waiting.
	emit(ctx, events, StreamEvent{Type: "done"})
}

// ── SSE scanner ───────────────────────────────────────

// sseEvent is one server-sent event. Type is empty for the data-only frames used
// by OpenAI-compatible providers.
type sseEvent struct {
	Type string
	Data json.RawMessage
}

// sseScanner reads text/event-stream frames. It is written against the SSE spec
// rather than one provider's dialect: events are separated by a blank line,
// `data:` may appear on several lines and is joined with newlines, lines
// starting with `:` are comments, and a frame with data but no `event:` line is
// still a valid event.
type sseScanner struct {
	reader *bufio.Reader
	event  sseEvent
	err    error
	done   bool
}

func newSSEScanner(r io.Reader) *sseScanner {
	// Large buffer: a single tool_use frame can carry a whole file.
	return &sseScanner{reader: bufio.NewReaderSize(r, 256*1024)}
}

// Next advances to the next event, reporting false at end of stream or on error.
func (s *sseScanner) Next() bool {
	if s.done {
		return false
	}

	var eventType string
	var data []string

	flush := func() bool {
		if eventType == "" && len(data) == 0 {
			return false
		}
		s.event = sseEvent{Type: eventType}
		if len(data) > 0 {
			s.event.Data = json.RawMessage(strings.Join(data, "\n"))
		}
		return true
	}

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			s.done = true
			if err != io.EOF {
				s.err = err
			}
			// A final frame may arrive without its trailing blank line.
			s.consumeSSELine(line, &eventType, &data)
			return flush()
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			// End of frame. Skip blank runs between frames.
			if flush() {
				return true
			}
			continue
		}
		s.consumeSSELine(trimmed, &eventType, &data)
	}
}

func (s *sseScanner) consumeSSELine(line string, eventType *string, data *[]string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" || strings.HasPrefix(line, ":") {
		return // comment / keep-alive
	}
	name, value, found := strings.Cut(line, ":")
	if !found {
		return // a bare field name carries no value we use
	}
	value = strings.TrimPrefix(value, " ")
	switch name {
	case "event":
		*eventType = strings.TrimSpace(value)
	case "data":
		*data = append(*data, value)
	}
}

// Event returns the event most recently read by Next.
func (s *sseScanner) Event() sseEvent { return s.event }

// Err returns the first non-EOF read error, if any.
func (s *sseScanner) Err() error { return s.err }
