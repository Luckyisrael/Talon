// Package aitest serves scripted model responses to the suites that fake
// an anthropic provider over HTTP.
package aitest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Serve writes payload as the fake model's response to req: plain JSON
// when the round was opened as a buffered request, and the same content
// re-framed as anthropic SSE when the request asked to stream.
//
// The stream flag matters because of FM-ANTa: a sunk round (a sink is
// always watching on the build turn) now sends `"stream":true` and reads
// SSE. A fake that ignores that flag answers JSON to a streaming request,
// the client parses an empty stream, and a scripted tool_use round turns
// into a scriptless empty one — a failure that points at the client
// rather than at the fake. Fakes call this; the openai protocol's fakes
// serve their own shape and are untouched.
func Serve(w http.ResponseWriter, req *http.Request, payload any) {
	body, _ := io.ReadAll(req.Body)
	ServeRaw(w, body, payload)
}

// ServeRaw is Serve for handlers that already consumed the request body.
func ServeRaw(w http.ResponseWriter, reqBody []byte, payload any) {
	if frames, err := sseFrames(payload); err == nil &&
		strings.Contains(string(reqBody), `"stream":true`) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, frames)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

// aitestResponse mirrors the shape every scripted fake answers with —
// the same fields internal/ai's AgenticResponse reads off the wire.
type aitestResponse struct {
	Content []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		Input     json.RawMessage `json:"input"`
		IsError   bool            `json:"is_error"`
		ToolUseID string          `json:"tool_use_id"`
		Content   string          `json:"content"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// sseFrames converts one scripted response into the anthropic event
// stream carrying it. It fails for payloads that are not an agentic
// response (no stop_reason and no content), so those fall through to
// plain JSON and the client reports its own error instead of receiving
// invented frames.
func sseFrames(payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var resp aitestResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	if resp.StopReason == "" && len(resp.Content) == 0 {
		return "", fmt.Errorf("not an agentic response")
	}

	var b strings.Builder
	frame := func(event string, data any) {
		encoded, _ := json.Marshal(data)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", event, encoded)
	}

	frame("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":          "msg_fake",
			"type":        "message",
			"role":        "assistant",
			"model":       "fake",
			"content":     []any{},
			"stop_reason": nil,
			"usage": map[string]any{
				"input_tokens":                resp.Usage.InputTokens,
				"output_tokens":               0,
				"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
				"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
			},
		},
	})

	for i, block := range resp.Content {
		switch block.Type {
		case "thinking", "redacted_thinking":
			frame("content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": block.Type, "thinking": ""},
			})
			if block.Thinking != "" {
				frame("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": i,
					"delta": map[string]any{"type": "thinking_delta", "thinking": block.Thinking},
				})
			}
		case "tool_use":
			// Anthropic's real wire: an empty input object at start, the
			// argument as input_json_delta partials. One partial carrying
			// the whole input keeps the fake honest about the frame kinds
			// the reader must assemble.
			frame("content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{
					"type": "tool_use", "id": block.ID, "name": block.Name, "input": map[string]any{},
				},
			})
			if len(block.Input) > 0 {
				frame("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": i,
					"delta": map[string]any{
						"type": "input_json_delta", "partial_json": string(block.Input),
					},
				})
			}
		default:
			frame("content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": block.Type, "text": ""},
			})
			if block.Type == "text" && block.Text != "" {
				frame("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": i,
					"delta": map[string]any{"type": "text_delta", "text": block.Text},
				})
			}
			if block.Type != "text" {
				// tool_result and friends are not part of an assistant
				// response; keep their content visible as text so a fake
				// that scripts them does not silently lose them.
				if block.Content != "" {
					frame("content_block_delta", map[string]any{
						"type": "content_block_delta", "index": i,
						"delta": map[string]any{"type": "text_delta", "text": block.Content},
					})
				}
			}
		}
		frame("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": i,
		})
	}

	frame("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": resp.StopReason, "stop_sequence": nil},
		"usage": map[string]any{
			"input_tokens":                resp.Usage.InputTokens,
			"output_tokens":               resp.Usage.OutputTokens,
			"cache_read_input_tokens":     resp.Usage.CacheReadInputTokens,
			"cache_creation_input_tokens": resp.Usage.CacheCreationInputTokens,
		},
	})
	frame("message_stop", map[string]any{"type": "message_stop"})

	return b.String(), nil
}
