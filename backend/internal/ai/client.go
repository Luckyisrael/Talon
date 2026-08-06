package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const systemPromptPlan = `IMPORTANT: Do NOT use any tools. Do NOT call any functions. Respond ONLY with plain text JSON. No tool calls.

You are a Solana Mobile app architect helping users design apps for the Seeker device.

The Seeker device specs:
- Screen: 6.36" AMOLED, 2670x1200, 460 PPI, 120Hz
- Android-based, runs React Native/Expo apps
- Has Mobile Wallet Adapter (MWA) for Solana wallet integration
- Seed Vault for secure key storage

Your job: help the user plan their app's screens, features, and navigation.

When the user describes their app idea:
1. Ask clarifying questions if needed
2. Propose a screen list with descriptions
3. Suggest navigation flow
4. List key features

Respond in this JSON format:
{
  "message": "your conversational response to the user",
  "plan": {
    "screens": [
      {"name": "ScreenName", "description": "What this screen shows/does"}
    ],
    "features": ["feature1", "feature2"],
    "navigation": "Description of how screens connect",
    "packages": ["any extra npm packages needed"]
  }
}

If you're still discussing (not ready to finalize), set plan to null.`

const systemPromptBuild = `IMPORTANT: Do NOT use any tools. Do NOT call any functions. Respond ONLY with plain text JSON. No tool calls.

You are a React Native engineer specializing in Solana Mobile apps for the Seeker device.

The project already has these packages installed:
- expo, react-native
- @solana/web3.js
- @solana-mobile/mobile-wallet-adapter-protocol-web3js
- @solana-mobile/mobile-wallet-adapter-protocol
- react-native-get-random-values, @craftzdog/react-native-buffer, react-native-quick-base64
- @react-native-async-storage/async-storage
- react-native-gesture-handler, react-native-reanimated
- react-native-safe-area-context

You generate COMPLETE, working code. Your response MUST be valid JSON:

{
  "message": "explanation of what you generated",
  "files": [
    {"path": "src/screens/HomeScreen.tsx", "content": "full file content"}
  ],
  "commands": ["cd MyApp && npm install extra-package"]
}

Rules:
- Use TypeScript
- Use named default exports for all components
- Wrap wallet calls in transact() from mobile-wallet-adapter-protocol-web3js
- Target 1080x2400 resolution, dark theme
- Import polyfills from '../polyfill' if needed
- Never use Buffer without importing it
- Always wrap app root in ErrorBoundary
- Keep files focused and clean`

type Provider string

const (
	ProviderAgentRouter Provider = "agentrouter"
	ProviderOpenRouter  Provider = "openrouter"
)

type Client struct {
	provider   Provider
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func New() *Client {
	provider := Provider(os.Getenv("AI_PROVIDER"))
	if provider == "" {
		provider = ProviderAgentRouter
	}

	var baseURL, apiKey, model string

	switch provider {
	case ProviderOpenRouter:
		baseURL = os.Getenv("OPENROUTER_BASE_URL")
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		apiKey = os.Getenv("OPENROUTER_API_KEY")
		model = os.Getenv("AI_MODEL")
		if model == "" {
			model = "anthropic/claude-sonnet-4"
		}
	default: // agentrouter
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
		if baseURL == "" {
			baseURL = "https://agentrouter.org"
		}
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		if apiKey == "" {
			apiKey = os.Getenv("AGENTROUTER_API_KEY")
		}
		model = os.Getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = "claude-opus-5"
		}
	}

	return &Client{
		provider:   provider,
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{},
	}
}

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

type BuildResponse struct {
	Message  string   `json:"message"`
	Files    []File   `json:"files"`
	Commands []string `json:"commands"`
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
		Type string `json:"type"`
		Text string `json:"text"`
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

func (c *Client) Plan(ctx context.Context, history []PromptMessage) (*PlanResponse, error) {
	resp, err := c.chat(ctx, systemPromptPlan, history)
	if err != nil {
		return nil, err
	}

	var planResp PlanResponse
	if err := json.Unmarshal([]byte(resp), &planResp); err != nil {
		planResp = PlanResponse{Message: resp}
	}

	return &planResp, nil
}

func (c *Client) Build(ctx context.Context, history []PromptMessage) (*BuildResponse, error) {
	resp, err := c.chat(ctx, systemPromptBuild, history)
	if err != nil {
		return nil, err
	}

	var buildResp BuildResponse
	if err := json.Unmarshal([]byte(resp), &buildResp); err != nil {
		buildResp = BuildResponse{Message: resp}
	}

	return &buildResp, nil
}

func (c *Client) chat(ctx context.Context, system string, history []PromptMessage) (string, error) {
	if c.provider == ProviderOpenRouter {
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
		MaxTokens: 4096,
		System:    system,
		Messages:  messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("User-Agent", "claude-cli/2.1.195 (external, sdk-cli)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
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

	return "", fmt.Errorf("no text content in AI response")
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
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
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
