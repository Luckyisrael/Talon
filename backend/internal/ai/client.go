package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	openai "github.com/sashabaranov/go-openai"
)

const systemPromptPlan = `You are a Solana Mobile app architect helping users design apps for the Seeker device.

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

const systemPromptBuild = `You are a React Native engineer specializing in Solana Mobile apps for the Seeker device.

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

type Client struct {
	client *openai.Client
	model  string
}

func New() *Client {
	apiKey := os.Getenv("AGENTROUTER_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	model := os.Getenv("AI_MODEL")
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://agentrouter.org/v1"

	client := openai.NewClientWithConfig(config)

	return &Client{client: client, model: model}
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
	msgs := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: system},
	}

	for _, h := range history {
		role := openai.ChatMessageRoleUser
		if h.Role == "assistant" {
			role = openai.ChatMessageRoleAssistant
		}
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    role,
			Content: h.Content,
		})
	}

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: msgs,
	})
	if err != nil {
		return "", fmt.Errorf("AI request failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("no response from AI")
	}

	return resp.Choices[0].Message.Content, nil
}
