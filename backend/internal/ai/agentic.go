package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
}

type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type AgenticChatResult struct {
	Text       string
	ToolUses   []ToolUseBlock
	StopReason string
}

type agenticMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (c *Client) ChatWithTools(ctx context.Context, system string, history []PromptMessage, tools []ToolDefinition) (*AgenticChatResult, error) {
	if c.protocol == ProtocolOpenAI {
		return c.chatWithToolsOpenRouter(ctx, system, history, tools)
	}
	return c.chatWithToolsAnthropic(ctx, system, history, tools)
}

func (c *Client) chatWithToolsOpenRouter(ctx context.Context, system string, history []PromptMessage, tools []ToolDefinition) (*AgenticChatResult, error) {
	messages := []openaiMessage{
		{Role: "system", Content: system},
	}
	for _, h := range history {
		messages = append(messages, openaiMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	// Convert tools to OpenAI format
	var oaiTools []map[string]interface{}
	for _, t := range tools {
		oaiTools = append(oaiTools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}

	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": messages,
		"max_tokens": 8192,
	}
	if len(oaiTools) > 0 {
		reqBody["tools"] = oaiTools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("ChatWithTools(OpenRouter): sending %d tools to %s", len(tools), c.baseURL)

	var respBody []byte
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("ChatWithTools(OpenRouter): retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}

		url := c.baseURL + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://seekerstudio.app")
		req.Header.Set("X-Title", "SeekerStudio")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("ChatWithTools(OpenRouter): request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return nil, fmt.Errorf("request failed after %d attempts: %w", maxRetries, err)
		}

		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 || resp.StatusCode == 429 {
			if attempt < maxRetries-1 {
				log.Printf("ChatWithTools(OpenRouter): HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		break
	}

	// Parse OpenAI response format
	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	msg := oaiResp.Choices[0].Message
	result := &AgenticChatResult{}

	if msg.Content != "" {
		result.Text = msg.Content
	}

	for _, tc := range msg.ToolCalls {
		result.ToolUses = append(result.ToolUses, ToolUseBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	// If no tool calls and no text, treat content as text
	if result.Text == "" && len(result.ToolUses) == 0 {
		result.Text = "(no response)"
	}

	result.StopReason = "end_turn"
	if len(result.ToolUses) > 0 {
		result.StopReason = "tool_use"
	}

	log.Printf("ChatWithTools(OpenRouter): response stop_reason=%s, text_len=%d, tool_uses=%d", result.StopReason, len(result.Text), len(result.ToolUses))

	return result, nil
}

func (c *Client) chatWithToolsAnthropic(ctx context.Context, system string, history []PromptMessage, tools []ToolDefinition) (*AgenticChatResult, error) {
	messages := make([]agenticMessage, 0, len(history))
	for _, h := range history {
		content, _ := json.Marshal(h.Content)
		messages = append(messages, agenticMessage{
			Role:    h.Role,
			Content: content,
		})
	}

	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 8192,
		"system":     system,
		"messages":   messages,
	}

	if len(tools) > 0 {
		reqBody["tools"] = tools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	log.Printf("ChatWithTools(Anthropic): sending %d tools to %s", len(tools), c.baseURL)

	var respBody []byte
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("ChatWithTools(Anthropic): retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*5) * time.Second)
		}

		url := c.baseURL + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", "SeekerStudio/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("ChatWithTools(Anthropic): request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return nil, fmt.Errorf("request failed after %d attempts: %w", maxRetries, err)
		}

		respBody, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 || resp.StatusCode == 429 {
			if attempt < maxRetries-1 {
				log.Printf("ChatWithTools(Anthropic): HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
		}

		break
	}

	var agenticResp AgenticResponse
	if err := json.Unmarshal(respBody, &agenticResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	result := &AgenticChatResult{
		StopReason: agenticResp.StopReason,
	}

	for _, block := range agenticResp.Content {
		switch block.Type {
		case "text":
			result.Text += block.Text
		case "tool_use":
			result.ToolUses = append(result.ToolUses, ToolUseBlock{
				Type:  "tool_use",
				ID:    block.ID,
				Name:  block.Name,
				Input: block.Input,
			})
		}
	}

	log.Printf("ChatWithTools(Anthropic): response stop_reason=%s, text_len=%d, tool_uses=%d", result.StopReason, len(result.Text), len(result.ToolUses))

	return result, nil
}

func (c *Client) AgenticLoop(ctx context.Context, system string, initialHistory []PromptMessage, tools []ToolDefinition, toolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error), maxRounds int) (string, error) {
	history := make([]PromptMessage, len(initialHistory))
	copy(history, initialHistory)

	for round := 0; round < maxRounds; round++ {
		if ctx.Err() != nil {
			return "", fmt.Errorf("context cancelled")
		}

		result, err := c.ChatWithTools(ctx, system, history, tools)
		if err != nil {
			return "", fmt.Errorf("agentic round %d failed: %w", round+1, err)
		}

		if len(result.ToolUses) == 0 {
			return result.Text, nil
		}

		assistantContent, _ := json.Marshal(result.ToolUses)
		history = append(history, PromptMessage{
			Role:    "assistant",
			Content: string(assistantContent),
		})

		var toolResults []ToolResultBlock
		for _, toolUse := range result.ToolUses {
			output, execErr := toolExecutor(ctx, toolUse.Name, toolUse.Input)
			isError := execErr != nil
			if output == "" && execErr != nil {
				output = execErr.Error()
			}

			toolResults = append(toolResults, ToolResultBlock{
				Type:      "tool_result",
				ToolUseID: toolUse.ID,
				Content:   output,
				IsError:   isError,
			})
		}

		resultsJSON, _ := json.Marshal(toolResults)
		history = append(history, PromptMessage{
			Role:    "user",
			Content: string(resultsJSON),
		})

		if result.Text != "" {
			return result.Text, nil
		}
	}

	return "", fmt.Errorf("exceeded max agentic rounds (%d)", maxRounds)
}
