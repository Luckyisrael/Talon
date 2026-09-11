package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResultBlock struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

type AgenticResponse struct {
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

// AgenticChatResult is one assistant turn. Blocks is the authoritative form —
// it round-trips to storage and back into the next request. Text and ToolUses
// are conveniences derived from it.
type AgenticChatResult struct {
	Text       string
	ToolUses   []ToolUseBlock
	Blocks     []Block
	StopReason string
	Usage      Usage
}

// maxOutputTokens caps a single response. Large enough for several complete
// component files in one turn.
const maxOutputTokens = 16384

// ChatWithTools performs one agentic round: send the conversation plus tool
// definitions, get back text and/or tool calls. It does not execute tools —
// the caller owns the loop.
func (c *Client) ChatWithTools(ctx context.Context, system string, history []Message, tools []ToolDefinition) (*AgenticChatResult, error) {
	if c.protocol == ProtocolOpenAI {
		return c.chatWithToolsOpenAI(ctx, system, history, tools)
	}
	return c.chatWithToolsAnthropic(ctx, system, history, tools)
}

// doWithRetry issues the request, retrying transient failures with exponential
// backoff plus jitter and honouring Retry-After when the server sends it.
func (c *Client) doWithRetry(ctx context.Context, url string, body []byte, setHeaders func(*http.Request), label string) ([]byte, error) {
	const maxRetries = 4
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * 2 * time.Second
			// Jitter keeps concurrent builds from retrying in lockstep.
			delay += time.Duration(rand.Int63n(int64(time.Second)))
			log.Printf("%s: retry %d/%d in %v", label, attempt+1, maxRetries, delay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		setHeaders(req)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			return respBody, nil
		case resp.StatusCode == 429 || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("API error (status %d): %s", resp.StatusCode, truncate(string(respBody), 500))
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, convErr := strconv.Atoi(ra); convErr == nil && secs > 0 && secs <= 60 {
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(time.Duration(secs) * time.Second):
					}
				}
			}
			continue
		default:
			// 4xx other than 429 will not succeed on retry.
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, truncate(string(respBody), 1000))
		}
	}

	return nil, fmt.Errorf("%s failed after %d attempts: %w", label, maxRetries, lastErr)
}

// transientEnvelopeRe matches provider error text that clears on its own:
// upstream overloads, timeouts, and OpenRouter's "Provider returned error"
// envelope, which arrives as HTTP 200 with an error body instead of a 5xx.
var transientEnvelopeRe = regexp.MustCompile(`(?i)(provider.{0,20}error|overloaded|temporar\w+|try again|rate.?limit|time\w*\s*out|capacity|upstream.{0,20}error|no endpoints? found)`)

// envelopeError extracts an in-body error envelope of the form
// {"error": {"message": "...", "code": 529}}. It returns the message, the
// numeric code (0 when absent or non-numeric), and whether an envelope was
// present at all.
func envelopeError(body []byte) (string, int, bool) {
	var env struct {
		Error *struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Error == nil {
		return "", 0, false
	}
	code := 0
	if len(env.Error.Code) > 0 {
		if n, err := strconv.Atoi(strings.Trim(string(env.Error.Code), `"`)); err == nil {
			code = n
		}
	}
	return env.Error.Message, code, true
}

// doWithEnvelopeRetry is doWithRetry plus one more layer: some gateways
// (notably OpenRouter) report an upstream failure as HTTP 200 carrying an
// error envelope, which the status-code retry above waves through. A transient
// envelope is retried with backoff; a permanent one (bad key, bad model,
// rejected request) fails fast with the code attached so the message names the
// real problem instead of "Provider returned error".
func (c *Client) doWithEnvelopeRetry(ctx context.Context, url string, body []byte, setHeaders func(*http.Request), label string) ([]byte, error) {
	const maxEnvelopeRetries = 3
	for attempt := 0; ; attempt++ {
		respBody, err := c.doWithRetry(ctx, url, body, setHeaders, label)
		if err != nil {
			c.noteFailure(err)
			return nil, err
		}
		msg, code, ok := envelopeError(respBody)
		if !ok {
			return respBody, nil
		}
		transient := (code == 429 || (code >= 500 && code < 600)) || transientEnvelopeRe.MatchString(msg)
		if !transient {
			err := fmt.Errorf("%s: provider refused the request (code %d): %s", label, code, truncate(msg, 500))
			c.noteFailure(err)
			return nil, err
		}
		if attempt+1 >= maxEnvelopeRetries {
			err := fmt.Errorf("%s: provider errored %d times, last: %s", label, maxEnvelopeRetries, truncate(msg, 300))
			c.noteFailure(err)
			return nil, err
		}
		delay := time.Duration(1<<uint(attempt)) * 2 * time.Second
		delay += time.Duration(rand.Int63n(int64(time.Second)))
		log.Printf("%s: transient provider envelope, retry %d/%d in %v: %s",
			label, attempt+1, maxEnvelopeRetries, delay, truncate(msg, 200))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ── OpenAI-compatible path ────────────────────────────

func (c *Client) chatWithToolsOpenAI(ctx context.Context, system string, history []Message, tools []ToolDefinition) (*AgenticChatResult, error) {
	messages := toOpenAI(system, history)

	oaiTools := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		oaiTools = append(oaiTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}

	reqBody := map[string]any{
		"model":      c.model,
		"messages":   messages,
		"max_tokens": maxOutputTokens,
	}
	if len(oaiTools) > 0 {
		reqBody["tools"] = oaiTools
		reqBody["tool_choice"] = "auto"
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	respBody, err := c.doWithEnvelopeRetry(ctx, c.baseURL+"/chat/completions", body, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://talon.app")
		req.Header.Set("X-Title", "Talon")
	}, "ChatWithTools(OpenAI)")
	if err != nil {
		return nil, err
	}

	var oaiResp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
					// Gemini echoes a thought_signature under extra_content.google
					// on EACH tool call it returns. Per Google's docs the signature
					// travels positionally with the part that issued it — parallel
					// calls only carry one on the first part — and must be echoed
					// back exactly as received on the same part.
					ExtraContent struct {
						Google struct {
							ThoughtSignature string `json:"thought_signature"`
						} `json:"google"`
					} `json:"extra_content"`
				} `json:"tool_calls"`
				// Gemini echoes a thought_signature under extra_content.google that
				// must be round-tripped on the assistant turn it came from.
				ExtraContent struct {
					Google struct {
						ThoughtSignature string `json:"thought_signature"`
					} `json:"google"`
				} `json:"extra_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, truncate(string(respBody), 300))
	}
	if oaiResp.Error.Message != "" {
		return nil, fmt.Errorf("provider error: %s", oaiResp.Error.Message)
	}
	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response: %s", truncate(string(respBody), 300))
	}

	choice := oaiResp.Choices[0]
	result := &AgenticChatResult{
		Text: choice.Message.Content,
		Usage: Usage{
			InputTokens:     oaiResp.Usage.PromptTokens,
			OutputTokens:    oaiResp.Usage.CompletionTokens,
			CacheReadTokens: oaiResp.Usage.PromptTokensDetails.CachedTokens,
		},
	}

	// The model's private reasoning comes first — it is what drove the text and
	// the tool calls that follow, so the user reads the decision before the act.
	// Plan mode streams this live; build mode used to vanish it entirely, so a
	// user watching a build saw tool rows stacking up with no idea what the model
	// was deciding.
	if choice.Message.Reasoning != "" {
		result.Blocks = append(result.Blocks, Block{Type: "thinking", Text: choice.Message.Reasoning})
	}
	if choice.Message.Content != "" {
		result.Blocks = append(result.Blocks, Block{Type: "text", Text: choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		// Some providers emit tool_calls with no id; synthesise one so the
		// tool_result we send back can be matched.
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", tc.Function.Name, len(result.ToolUses))
		}
		// Gemini returns the signature on each tool call part; some responses
		// put it once on the whole message instead. Prefer the per-call value —
		// parallel calls only sign the first part, and that positional match is
		// what the API validates on the next request.
		sig := tc.ExtraContent.Google.ThoughtSignature
		if sig == "" {
			sig = choice.Message.ExtraContent.Google.ThoughtSignature
		}
		result.ToolUses = append(result.ToolUses, ToolUseBlock{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
		})
		result.Blocks = append(result.Blocks, Block{
			Type: "tool_use", ID: id, Name: tc.Function.Name, Input: json.RawMessage(args),
			// Gemini requires its thought_signature echoed back on the same
			// part when we replay this tool call on the next request.
			ThoughtSignature: sig,
		})
	}

	// Normalise finish_reason onto the Anthropic vocabulary so the caller's
	// loop has one set of stop conditions to reason about.
	switch {
	case len(result.ToolUses) > 0:
		result.StopReason = "tool_use"
	case choice.FinishReason == "length":
		result.StopReason = "max_tokens"
	default:
		result.StopReason = "end_turn"
	}

	log.Printf("ChatWithTools(OpenAI): stop=%s text=%d tools=%d in=%d out=%d",
		result.StopReason, len(result.Text), len(result.ToolUses),
		result.Usage.InputTokens, result.Usage.OutputTokens)

	return result, nil
}

// ── Anthropic path ────────────────────────────────────

func (c *Client) chatWithToolsAnthropic(ctx context.Context, system string, history []Message, tools []ToolDefinition) (*AgenticChatResult, error) {
	// Cache the system prompt and tool definitions (both stable across a
	// build) plus the conversation prefix up to the last complete turn.
	breakpoint := len(history) - 2
	if breakpoint < 0 {
		breakpoint = -1
	}

	reqBody := map[string]any{
		"model":      c.model,
		"max_tokens": maxOutputTokens,
		"system": []map[string]any{{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]string{"type": "ephemeral"},
		}},
		"messages": toAnthropic(history, breakpoint),
	}
	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	respBody, err := c.doWithEnvelopeRetry(ctx, c.baseURL+"/v1/messages", body, func(req *http.Request) {
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", "Talon/1.0")
	}, "ChatWithTools(Anthropic)")
	if err != nil {
		return nil, err
	}

	var agenticResp AgenticResponse
	if err := json.Unmarshal(respBody, &agenticResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w (body: %s)", err, truncate(string(respBody), 300))
	}

	result := &AgenticChatResult{
		StopReason: agenticResp.StopReason,
		Usage: Usage{
			InputTokens:      agenticResp.Usage.InputTokens,
			OutputTokens:     agenticResp.Usage.OutputTokens,
			CacheReadTokens:  agenticResp.Usage.CacheReadInputTokens,
			CacheWriteTokens: agenticResp.Usage.CacheCreationInputTokens,
		},
	}

	for _, block := range agenticResp.Content {
		switch block.Type {
		case "text":
			result.Text += block.Text
			result.Blocks = append(result.Blocks, Block{Type: "text", Text: block.Text})
		case "thinking":
			result.Blocks = append(result.Blocks, Block{Type: "thinking", Text: block.Thinking})
		case "tool_use":
			result.ToolUses = append(result.ToolUses, ToolUseBlock{
				Type: "tool_use", ID: block.ID, Name: block.Name, Input: block.Input,
			})
			result.Blocks = append(result.Blocks, Block{
				Type: "tool_use", ID: block.ID, Name: block.Name, Input: block.Input,
			})
		}
	}

	log.Printf("ChatWithTools(Anthropic): stop=%s text=%d tools=%d in=%d out=%d cached=%d",
		result.StopReason, len(result.Text), len(result.ToolUses),
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.CacheReadTokens)

	return result, nil
}
