package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// VisionCheckResult is the verdict on a screenshot of the running app.
type VisionCheckResult struct {
	Matches  bool   `json:"matches"`
	Reason   string `json:"reason"`
	HasError bool   `json:"has_error"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

const visionPrompt = `You are looking at a screenshot from an Android phone running a React Native app that was just built.

The screen is supposed to be: %s

Report what you actually see:
1. Does the screen match that description?
2. Is anything visibly broken — a red error screen, a stack trace, a blank white or black screen, "Unable to resolve module", a stuck loading spinner?
3. Does the layout hold up on a tall phone screen — nothing clipped off the edges, no text overlapping, nothing hidden behind the status bar or the navigation bar?
4. Design craft — be strict, this is what separates a finished screen from a generated one:
   - Hierarchy: is the most important thing on the screen the most prominent?
   - Spacing: is there a consistent rhythm, or do gaps and margins look arbitrary or cramped?
   - Colour discipline: one accent colour (multiple competing hues is a failure), one grey family, contrast readable in the theme shown.
   - Typography: a coherent type scale with clear size/weight steps, no orphaned bold words, no tiny text.
   - Iconography: vector icons from one consistent family — no emoji standing in for icons, no mismatched icon styles on one screen.
   - Surfaces: separation by tone and elevation, not a stack of hairline borders on every nested box.

Reply with JSON only:
{
  "matches": true or false,
  "reason": "what you see, specifically — name the craft problems you found, not only the broken ones",
  "has_error": true or false,
  "error_msg": "the error text if one is visible"
}`

// VisionCheck asks the model whether a screenshot shows the screen that was
// intended. It speaks whichever protocol the client is configured for, since the
// image block shape differs between them.
func (c *Client) VisionCheck(ctx context.Context, screenshot []byte, intendedScreen string) (*VisionCheckResult, error) {
	if len(screenshot) == 0 {
		return nil, fmt.Errorf("screenshot is empty")
	}

	encoded := base64.StdEncoding.EncodeToString(screenshot)
	prompt := fmt.Sprintf(visionPrompt, intendedScreen)

	var (
		url  string
		body []byte
		err  error
	)

	if c.protocol == ProtocolOpenAI {
		url = c.baseURL + "/chat/completions"
		body, err = json.Marshal(map[string]any{
			"model":      c.model,
			"max_tokens": 1024,
			"messages": []map[string]any{{
				"role": "user",
				"content": []map[string]any{
					{"type": "image_url", "image_url": map[string]string{
						"url": "data:image/png;base64," + encoded,
					}},
					{"type": "text", "text": prompt},
				},
			}},
		})
	} else {
		url = c.baseURL + "/messages"
		body, err = json.Marshal(map[string]any{
			"model":      c.model,
			"max_tokens": 1024,
			"messages": []map[string]any{{
				"role": "user",
				"content": []map[string]any{
					{"type": "image", "source": map[string]string{
						"type":       "base64",
						"media_type": "image/png",
						"data":       encoded,
					}},
					{"type": "text", "text": prompt},
				},
			}},
		})
	}
	if err != nil {
		return nil, fmt.Errorf("could not encode the vision request: %w", err)
	}

	respBody, err := c.doWithRetry(ctx, url, body, func(req *http.Request) {
		if c.protocol == ProtocolOpenAI {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
			if strings.Contains(c.baseURL, "openrouter") {
				req.Header.Set("HTTP-Referer", "https://talon.dev")
				req.Header.Set("X-Title", "Talon Studio")
			}
		} else {
			req.Header.Set("x-api-key", c.apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	}, "vision_check")
	if err != nil {
		c.noteFailure(err)
		return nil, err
	}

	text, err := extractVisionText(respBody, c.protocol)
	if err != nil {
		return nil, err
	}

	var result VisionCheckResult
	if json.Unmarshal([]byte(StripCodeBlocks(text)), &result) != nil {
		// The model answered in prose. Its description is still useful, but a
		// verdict we could not read must not be reported as a pass.
		return &VisionCheckResult{Matches: false, Reason: text}, nil
	}
	return &result, nil
}

func extractVisionText(respBody []byte, protocol Provider) (string, error) {
	if protocol == ProtocolOpenAI {
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(respBody, &r); err != nil {
			return "", fmt.Errorf("could not parse the vision response: %w", err)
		}
		if r.Error != nil {
			return "", fmt.Errorf("vision check failed: %s", r.Error.Message)
		}
		if len(r.Choices) == 0 || r.Choices[0].Message.Content == "" {
			return "", fmt.Errorf("the model returned no description of the screenshot")
		}
		return r.Choices[0].Message.Content, nil
	}

	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &r); err != nil {
		return "", fmt.Errorf("could not parse the vision response: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("vision check failed: %s", r.Error.Message)
	}
	for _, b := range r.Content {
		if b.Type == "text" && b.Text != "" {
			return b.Text, nil
		}
	}
	return "", fmt.Errorf("the model returned no description of the screenshot")
}
