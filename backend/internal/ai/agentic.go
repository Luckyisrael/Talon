package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "claude-cli/2.1.195 (external, sdk-cli)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
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
