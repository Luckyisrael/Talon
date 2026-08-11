package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const systemPromptPlan = `CRITICAL RULES — VIOLATION IS FAILURE:
1. You are in PLAN mode. You must NEVER write code, generate file contents, create files, or provide implementation details.
2. You must NEVER generate HTML, CSS, JavaScript, index.html, or any web code.
3. You must NEVER generate React Native code, TypeScript code, or any code at all.
4. If the user asks you to write code, build something, create an app, or implement anything, you MUST refuse and tell them to switch to Build mode.

You are a Solana Mobile app architect helping users DESIGN apps for the Seeker device (Android, React Native/Expo).

The Seeker device specs:
- Screen: 6.36" AMOLED, 2670x1200, 460 PPI, 120Hz
- Android-based, runs React Native/Expo apps
- Has Mobile Wallet Adapter (MWA) for Solana wallet integration
- Seed Vault for secure key storage

Your job: help the user plan their app's screens, features, and navigation. NOTHING ELSE.

If the user says anything like "create me...", "build me...", "make me...", "I want a...", "I need a...", "can you create...", "add two numbers", "simple app", "calculator", "todo app" — you MUST respond:
"I'm in Plan mode, which is for designing your app — not writing code. To start building, switch to Build mode using the toggle at the top of the chat.

In Plan mode I can help you:
- Define screens and features
- Plan navigation flow
- Choose packages and architecture
- Discuss UI/UX patterns

Switch to Build mode when you're ready to generate code."

Plan mode is ONLY for:
- Discussing and refining the app idea
- Defining screens and features
- Planning navigation and architecture
- Choosing packages

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

const systemPromptBuild = `CRITICAL RULES — VIOLATION IS FAILURE:
1. You are building a REACT NATIVE / EXPO mobile app for ANDROID (Seeker device).
2. You must NEVER generate HTML, CSS, vanilla JavaScript, index.html, or any web code.
3. You must NEVER create website files. This is NOT a web app.
4. ONLY use React Native components: View, Text, StyleSheet, TouchableOpacity, ScrollView, FlatList, TextInput, Switch, etc.
5. ONLY use Expo libraries and expo-router for navigation.
6. Your output MUST be valid JSON with a "files" array containing TypeScript/TSX files.

You are a React Native engineer specializing in Solana Mobile apps for the Seeker device.

=== CRITICAL GUARDRAILS — READ EVERY ONE ===

1. REACT NATIVE ONLY: Use View, Text, StyleSheet, TouchableOpacity, ScrollView, FlatList, TextInput, Switch, SafeAreaView, etc. NEVER use div, span, p, button, input, or any HTML elements. NEVER use CSS files. NEVER use document.getElementById or any DOM API.

2. READ BEFORE EDIT: The existing project files are provided in the context below. You MUST read and understand them before generating any code. Never generate code for a file you haven't seen. If a file already exists, your output must preserve its existing structure and only add/modify what the user requested.

3. NEVER DELETE: Do not remove existing functionality, screens, components, or features unless the user explicitly says "delete" or "remove". If a file exists and the user didn't ask to change it, do NOT include it in your response.

4. PRESERVE PATTERNS: Match the existing code style exactly. If the project uses named exports, use named exports. If it uses a specific folder structure, follow it. If it uses a particular state management approach, continue using it.

5. VALIDATE PATHS: File paths must be relative to the project root (e.g., "src/screens/HomeScreen.tsx", not "MyApp/src/screens/HomeScreen.tsx"). Never use absolute paths.

6. NO CONFIG CHANGES: Do not modify package.json, app.json, tsconfig.json, or babel.config.js unless the user explicitly asks. Never add new dependencies without the user requesting them.

6. TYPE SAFETY: All components must have proper TypeScript types. Use interfaces for props. Never use 'any' type.

7. ERROR BOUNDARIES: Every screen component must be wrapped in an ErrorBoundary. If an ErrorBoundary doesn't exist yet, create it.

8. WALLET PATTERNS: All Solana wallet interactions MUST use MWA (Mobile Wallet Adapter) protocol. Never store private keys in app state. Use transact() for signing.

9. SINGLE RESPONSIBILITY: Each file should do one thing. Don't create mega-components. Split complex UIs into smaller, focused components.

10. EXPLAIN CHANGES: Your "message" field must clearly explain:
    - What files you created vs modified
    - Why you made each change
    - Any decisions or assumptions you made

The project already has these packages installed:
- expo, react-native
- @solana/web3.js, @solana/spl-token
- @solana-mobile/mobile-wallet-adapter-protocol-web3js
- @solana-mobile/mobile-wallet-adapter-protocol
- react-native-gesture-handler, react-native-reanimated
- react-native-safe-area-context, react-native-screens
- react-native-quick-crypto, js-base64
- @react-native-async-storage/async-storage
- expo-router, expo-constants, expo-linking, expo-status-bar
- react-native-svg, react-native-webview

Your response MUST be valid JSON:

{
  "message": "explanation of what you generated and why",
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

func New() *Client {
	cfg := loadConfig()
	if err := cfg.validate(); err != nil {
		log.Fatalf("AI provider configuration error: %v", err)
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir = os.TempDir()
	}

	return &Client{
		protocol:     Provider(cfg.Protocol),
		baseURL:      cfg.BaseURL,
		apiKey:       cfg.APIKey,
		model:        cfg.Model,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
		workspaceDir: workspaceDir,
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
		Type  string `json:"type"`
		Text  string `json:"text"`
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

func (c *Client) Plan(ctx context.Context, history []PromptMessage) (*PlanResponse, error) {
	resp, err := c.chatWithRetry(ctx, systemPromptPlan, history, 2)
	if err != nil {
		return nil, err
	}

	var planResp PlanResponse
	if err := json.Unmarshal([]byte(resp), &planResp); err != nil {
		planResp = PlanResponse{Message: resp}
	}

	return &planResp, nil
}

func (c *Client) Build(ctx context.Context, history []PromptMessage, workspacePath string) (*BuildResponse, error) {
	// Read existing files and inject into context
	existingFiles := c.ReadProjectFiles(workspacePath)
	if len(existingFiles) > 0 {
		contextMsg := PromptMessage{
			Role:    "user",
			Content: "Here are the existing project files. Read them carefully before making any changes:\n\n" + existingFiles,
		}
		history = append([]PromptMessage{contextMsg}, history...)
	}

	resp, err := c.chatWithRetry(ctx, systemPromptBuild, history, 2)
	if err != nil {
		return nil, err
	}

	var buildResp BuildResponse
	if err := json.Unmarshal([]byte(resp), &buildResp); err != nil {
		buildResp = BuildResponse{Message: resp}
	}

	return &buildResp, nil
}

// chatWithRetry sends the request and retries once if the response is invalid JSON
func (c *Client) chatWithRetry(ctx context.Context, system string, history []PromptMessage, maxRetries int) (string, error) {
	resp, err := c.chat(ctx, system, history)
	if err != nil {
		return "", err
	}

	// Strip markdown code blocks if present
	resp = StripCodeBlocks(resp)

	// Check if response is valid JSON (for build mode, it must be)
	if strings.Contains(system, "You generate COMPLETE") {
		var testResp map[string]interface{}
		if err := json.Unmarshal([]byte(resp), &testResp); err != nil {
			// Invalid JSON — retry with error context
			if maxRetries > 0 {
				retryHistory := make([]PromptMessage, len(history))
				copy(retryHistory, history)
				retryHistory = append(retryHistory, PromptMessage{Role: "assistant", Content: resp})
				retryHistory = append(retryHistory, PromptMessage{
					Role:    "user",
					Content: "Your response was not valid JSON. You MUST respond with valid JSON only. No markdown, no code blocks, just raw JSON. The JSON must have 'message', 'files', and 'commands' fields. Try again.",
				})
				return c.chatWithRetry(ctx, system, retryHistory, maxRetries-1)
			}
		}
	}

	return resp, nil
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

// readProjectFiles reads key files from the project workspace
func (c *Client) ReadProjectFiles(workspacePath string) string {
	if workspacePath == "" {
		return ""
	}

	var files []string
	keyFiles := []string{
		"App.tsx",
		"index.js",
		"package.json",
		"tsconfig.json",
		"babel.config.js",
		"polyfill.js",
		"app.json",
	}

	// Read root-level key files
	for _, f := range keyFiles {
		path := filepath.Join(workspacePath, f)
		content, err := os.ReadFile(path)
		if err == nil {
			files = append(files, fmt.Sprintf("=== %s ===\n%s", f, string(content)))
		}
	}

	// Read src/ directory recursively
	srcDir := filepath.Join(workspacePath, "src")
	if _, err := os.Stat(srcDir); err == nil {
		filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			// Skip node_modules and other non-essential files
			if strings.Contains(path, "node_modules") || strings.Contains(path, ".expo") {
				return nil
			}
			relPath, _ := filepath.Rel(workspacePath, path)
			content, err := os.ReadFile(path)
			if err == nil {
				files = append(files, fmt.Sprintf("=== %s ===\n%s", relPath, string(content)))
			}
			return nil
		})
	}

	return strings.Join(files, "\n\n")
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

// ChatStream sends a streaming request to the AI and returns a channel of events.
// The channel is closed when the stream ends or an error occurs.
func (c *Client) ChatStream(ctx context.Context, system string, history []PromptMessage) <-chan StreamEvent {
	events := make(chan StreamEvent, 64)

	go func() {
		defer close(events)

		if c.protocol == ProtocolOpenAI {
			c.streamOpenRouter(ctx, system, history, events)
			return
		}

		c.streamAnthropic(ctx, system, history, events)
	}()

	return events
}

func (c *Client) streamOpenRouter(ctx context.Context, system string, history []PromptMessage, events chan<- StreamEvent) {
	messages := []openaiMessage{
		{Role: "system", Content: system},
	}
	for _, h := range history {
		messages = append(messages, openaiMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	reqBody := map[string]interface{}{
		"model":    c.model,
		"messages": messages,
		"stream":   true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		events <- StreamEvent{Type: "error", Error: "failed to marshal request: " + err.Error()}
		return
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("streamOpenRouter: retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		url := c.baseURL + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			events <- StreamEvent{Type: "error", Error: "failed to create request: " + err.Error()}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://seekerstudio.app")
		req.Header.Set("X-Title", "SeekerStudio")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("streamOpenRouter: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			events <- StreamEvent{Type: "error", Error: "Connection failed. The AI service may be temporarily unavailable. Please try again."}
			return
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			if attempt < maxRetries-1 {
				log.Printf("streamOpenRouter: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
			var errMsg string
			switch resp.StatusCode {
			case 429:
				errMsg = "Rate limit exceeded. Please wait a moment and try again."
			case 502:
				errMsg = "AI service temporarily unavailable. Please try again."
			case 503:
				errMsg = "AI service is currently overloaded. Please try again."
			case 504:
				errMsg = "AI service request timed out. Please try again."
			default:
				errMsg = fmt.Sprintf("AI service error (HTTP %d). Please try again.", resp.StatusCode)
			}
			events <- StreamEvent{Type: "error", Error: errMsg}
			return
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			events <- StreamEvent{Type: "error", Error: fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(respBody))}
			return
		}

		events <- StreamEvent{Type: "thinking.start"}
		events <- StreamEvent{Type: "text.start"}

		scanner := newSSEScanner(resp.Body)
		for scanner.Next() {
			event := scanner.Event()
			if event.Type == "" || event.Type == "message" {
				continue
			}
			if event.Type == "done" {
				break
			}

			// OpenAI SSE format: data: {"choices":[{"delta":{"content":"..."}}]}
			if event.Type == "message.stop" || event.Type == "message.complete" {
				break
			}

			var data struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if json.Unmarshal(event.Data, &data) == nil && len(data.Choices) > 0 {
				if content := data.Choices[0].Delta.Content; content != "" {
					events <- StreamEvent{Type: "text.delta", Data: StreamTextDelta{Text: content}}
				}
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			events <- StreamEvent{Type: "error", Error: "stream read error: " + err.Error()}
			return
		}

		events <- StreamEvent{Type: "text.end"}
		events <- StreamEvent{Type: "done"}
		return
	}
}

func (c *Client) streamAnthropic(ctx context.Context, system string, history []PromptMessage, events chan<- StreamEvent) {
	messages := make([]anthropicMessage, 0, len(history))
	for _, h := range history {
		messages = append(messages, anthropicMessage{
			Role:    h.Role,
			Content: h.Content,
		})
	}

	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 8192,
		"system":     system,
		"messages":   messages,
		"stream":     true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		events <- StreamEvent{Type: "error", Error: "failed to marshal request: " + err.Error()}
		return
	}

	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("streamAnthropic: retry attempt %d/%d", attempt+1, maxRetries)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		url := c.baseURL + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			events <- StreamEvent{Type: "error", Error: "failed to create request: " + err.Error()}
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", "SeekerStudio/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("streamAnthropic: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			events <- StreamEvent{Type: "error", Error: "Connection failed. The AI service may be temporarily unavailable. Please try again."}
			return
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			resp.Body.Close()
			if attempt < maxRetries-1 {
				log.Printf("streamAnthropic: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				continue
			}
			var errMsg string
			switch resp.StatusCode {
			case 429:
				errMsg = "Rate limit exceeded. The AI service is receiving too many requests. Please wait a moment and try again."
			case 502:
				errMsg = "AI service temporarily unavailable. This usually resolves quickly — please try again."
			case 503:
				errMsg = "AI service is currently overloaded. Please wait a moment and try again."
			case 504:
				errMsg = "AI service request timed out. The service may be under heavy load — please try again."
			default:
				errMsg = fmt.Sprintf("AI service error (HTTP %d). Please try again.", resp.StatusCode)
			}
			events <- StreamEvent{Type: "error", Error: errMsg}
			return
		}

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			events <- StreamEvent{Type: "error", Error: fmt.Sprintf("API error (status %d): %s", resp.StatusCode, string(respBody))}
			return
		}

		// Parse SSE stream
		scanner := newSSEScanner(resp.Body)
		for scanner.Next() {
			event := scanner.Event()

			switch event.Type {
			case "content_block_start":
				var data struct {
					ContentBlock struct {
						Type string `json:"type"`
					} `json:"content_block"`
				}
				if json.Unmarshal(event.Data, &data) == nil {
					switch data.ContentBlock.Type {
					case "thinking":
						events <- StreamEvent{Type: "thinking.start"}
					case "text":
						events <- StreamEvent{Type: "text.start"}
					}
				}

			case "content_block_delta":
				var data struct {
					Delta struct {
						Type     string `json:"type"`
						Text     string `json:"text"`
						Thinking string `json:"thinking"`
					} `json:"delta"`
				}
				if json.Unmarshal(event.Data, &data) == nil {
					switch data.Delta.Type {
					case "thinking_delta":
						events <- StreamEvent{Type: "thinking.delta", Data: StreamTextDelta{Text: data.Delta.Thinking}}
					case "text_delta":
						events <- StreamEvent{Type: "text.delta", Data: StreamTextDelta{Text: data.Delta.Text}}
					}
				}

			case "content_block_stop":
				// We don't know which block type stopped without tracking index.
				// The text.end / thinking.end will be inferred by the caller
				// when it sees text.start followed by the next content_block_start or message_stop.

			case "message_delta":
				events <- StreamEvent{Type: "text.end"}

			case "message_stop":
				events <- StreamEvent{Type: "done"}

			case "error":
				var data struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(event.Data, &data) == nil {
					events <- StreamEvent{Type: "error", Error: data.Error.Message}
				}
			}
		}
		resp.Body.Close()

		if err := scanner.Err(); err != nil {
			events <- StreamEvent{Type: "error", Error: "stream read error: " + err.Error()}
		}
		return
	}
}

// fallbackStream handles non-streaming providers by making a regular request
// and emitting events to simulate streaming.
func (c *Client) fallbackStream(ctx context.Context, system string, history []PromptMessage, events chan<- StreamEvent) {
	events <- StreamEvent{Type: "thinking.start"}

	resp, err := c.chat(ctx, system, history)
	if err != nil {
		events <- StreamEvent{Type: "error", Error: err.Error()}
		return
	}

	events <- StreamEvent{Type: "thinking.end"}
	events <- StreamEvent{Type: "text.start"}

	// Emit text in chunks to simulate streaming
	chunkSize := 20
	for i := 0; i < len(resp); i += chunkSize {
		end := i + chunkSize
		if end > len(resp) {
			end = len(resp)
		}
		events <- StreamEvent{Type: "text.delta", Data: StreamTextDelta{Text: resp[i:end]}}
	}

	events <- StreamEvent{Type: "text.end"}
	events <- StreamEvent{Type: "done"}
}

// ── SSE Scanner ───────────────────────────────────────

type sseEvent struct {
	Type string
	Data json.RawMessage
}

type sseScanner struct {
	reader  io.Reader
	err     error
	event   sseEvent
	buf     []byte
}

func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{reader: r, buf: make([]byte, 0, 4096)}
}

func (s *sseScanner) Err() error { return s.err }

func (s *sseScanner) Event() sseEvent { return s.event }

func (s *sseScanner) Next() bool {
	tmp := make([]byte, 1024)
	for {
		n, err := s.reader.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[:n]...)
		}
		if err != nil {
			if err != io.EOF {
				s.err = err
			}
			// Process any remaining data
			if len(s.buf) == 0 {
				return false
			}
		}

		// Try to parse an SSE event from the buffer
		for {
			// Look for double newline (event separator)
			idx := bytes.Index(s.buf, []byte("\n\n"))
			if idx < 0 {
				break
			}

			rawEvent := s.buf[:idx]
			s.buf = s.buf[idx+2:]

			event := s.parseSSEBlock(rawEvent)
			if event.Type != "" {
				s.event = event
				return true
			}
		}

		if err != nil {
			return false
		}
	}
}

func (s *sseScanner) parseSSEBlock(block []byte) sseEvent {
	var event sseEvent
	lines := bytes.Split(block, []byte("\n"))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		if bytes.HasPrefix(line, []byte("event:")) {
			event.Type = string(bytes.TrimSpace(line[6:]))
		} else if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(line[5:])
			event.Data = json.RawMessage(data)
		}
	}

	return event
}
