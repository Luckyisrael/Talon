package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type VisionCheckResult struct {
	Matches  bool   `json:"matches"`
	Reason   string `json:"reason"`
	HasError bool   `json:"has_error"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

func (c *Client) VisionCheck(ctx context.Context, screenshot []byte, intendedScreen string) (*VisionCheckResult, error) {
	prompt := fmt.Sprintf(`You are verifying if a mobile app screenshot matches the intended screen.

Intended screen: %s

Look at this screenshot and determine:
1. Does it show the intended screen?
2. Are there any visible errors (error boundaries, blank screens, crash messages)?
3. Does the layout look correct for a mobile app?

Respond with JSON:
{
  "matches": true/false,
  "reason": "explanation of what you see",
  "has_error": true/false,
  "error_msg": "if there's an error, describe it"
}`, intendedScreen)

	content := []map[string]interface{}{
		{
			"type": "image",
			"source": map[string]string{
				"type":      "base64",
				"media_type": "image/png",
				"data":      encodeBase64(screenshot),
			},
		},
		{
			"type": "text",
			"text": prompt,
		},
	}

	contentJSON, _ := json.Marshal(content)

	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 1024,
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": json.RawMessage(contentJSON),
			},
		},
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
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "SeekerStudio/1.0")

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

	var anthResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &anthResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if anthResp.Error != nil {
		return nil, fmt.Errorf("API error: %s", anthResp.Error.Message)
	}

	for _, block := range anthResp.Content {
		if block.Type == "text" && block.Text != "" {
			var result VisionCheckResult
			if err := json.Unmarshal([]byte(StripCodeBlocks(block.Text)), &result); err != nil {
				return &VisionCheckResult{
					Matches:  false,
					Reason:   block.Text,
					HasError: false,
				}, nil
			}
			return &result, nil
		}
	}

	return nil, fmt.Errorf("no text content in vision check response")
}

func encodeBase64(data []byte) string {
	import_b64 := make([]byte, 0, len(data)*4/3+4)
	_ = import_b64

	result := ""
	for i := 0; i < len(data); i += 3 {
		var b [4]byte
		b[0] = data[i] >> 2
		b[1] = (data[i]&3)<<4 | data[i+1]>>4
		if i+1 < len(data) {
			b[2] = (data[i+1]&15)<<2 | data[i+2]>>6
		}
		if i+2 < len(data) {
			b[3] = data[i+2] & 63
		}
		for _, c := range b {
			switch {
			case c < 26:
				result += string(rune('A' + c))
			case c < 52:
				result += string(rune('a' + c - 26))
			case c < 62:
				result += string(rune('0' + c - 52))
			case c == 62:
				result += "+"
			default:
				result += "/"
			}
		}
	}
	return result
}
