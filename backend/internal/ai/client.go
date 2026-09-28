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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Provider string

const (
	ProtocolAnthropic Provider = "anthropic"
	ProtocolOpenAI    Provider = "openai"
)

type Client struct {
	protocol Provider
	baseURL  string
	apiKey   string
	model    string
	// providerName pins which provider this client serves (W7): "primary"
	// for the env-built client, a providers-block name after AtProvider.
	// Surfaced so usage/record and the breaker log the endpoint that
	// actually answered, not the one that was asked first.
	providerName string
	httpClient   *http.Client
	workspaceDir string

	// streamClient serves openStream — every streaming path — and nothing
	// else. Its Timeout is deliberately 0 (M2): the shared 10-minute
	// httpClient timeout killed healthy long streams, a reasoning round
	// can think silently past any fixed total. Liveness is the pair of
	// silence bounds instead: the transport's ResponseHeaderTimeout cuts
	// a provider that never sends headers, and stalledBody cuts one that
	// sends headers then goes quiet, measured between bytes. Nil on
	// hand-built test clients, which fall back to httpClient — the old
	// behavior, byte-identical.
	streamClient *http.Client
	// streamStall is the between-bytes watchdog for open stream bodies
	// (0 = off). See stallbody.go.
	streamStall time.Duration
	// streamRestarts is the sink-branch restart budget: how many times a
	// stream that died or truncated mid-turn is resent byte-identically
	// (0 = single attempt, today's behavior).
	streamRestarts int

	// rateLimitWindow is how long a gateway's requests-per-minute window
	// takes to reset when it sends no Retry-After header. A 429 waits this
	// out instead of taking the flaky-502 ladder: 3s/6s exhausts the retry
	// budget ~45s before a per-minute window clears, which killed Run-5's
	// build turn at round 8 (FM-429a). 0 selects defaultRateLimitWindow —
	// tests shrink it so a real wait stays test-sized.
	rateLimitWindow time.Duration

	// lastErr records the most recent provider failure for health checks.
	// A healthy provider never writes here; readyz degrades only on fresh
	// failures, never on ancient history.
	mu        sync.Mutex
	lastErr   string
	lastErrAt time.Time

	// Gateway pacing: many gateways (free OpenRouter tiers especially)
	// enforce requests-per-minute budgets the backend would otherwise blow
	// through — an agentic build is dozens of sequential model calls, and
	// parallel read-only tool rounds are several calls in flight at once.
	// pacer enforces a minimum interval between gateway calls across every
	// code path that shares this client. A nil pacer (the default) means no
	// pacing — unit tests and evals, which mock the gateway, stay fast.
	pacer *requestPacer

	// retryHook is called before every retry backoff (see WithRetryHook). It
	// turns a silent stall into a visible one: rate limits and transient server
	// errors otherwise look like a hung request.
	retryHook func(attempt, maxRetries int, reason string, after time.Duration)

	// toolArgs reports streaming tool-argument fragments (L8). The StreamSink
	// contract is text/thinking only, but a large write_file input generates
	// for minutes with nothing else on the wire — Run 2's true top gap,
	// 191 s. The stream reader calls this with the tool's name and each
	// fragment as it lands; nil (the default) means nobody is watching.
	toolArgs func(tool, fragment string)
}

// requestPacer serializes gateway calls to at most one per interval. It is
// deliberately simple: no queue, no burst allowance — callers already retry
// on transient failures, so a strict interval is enough to stay under a
// requests-per-minute budget.
type requestPacer struct {
	mu       sync.Mutex
	nextAt   time.Time
	interval time.Duration
}

// newRequestPacer builds a pacer from AI_MIN_REQUEST_INTERVAL (Go duration
// syntax, e.g. "8s"). Empty means nil — no pacing, behavior byte-identical
// to before.
func newRequestPacer() *requestPacer {
	raw := os.Getenv("AI_MIN_REQUEST_INTERVAL")
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return nil
	}
	return &requestPacer{interval: d}
}

// wait blocks until the next call slot. ctx cancellation propagates.
// The slot is reserved under the lock BEFORE sleeping — two waiters that
// arrive together must be spaced by the interval, not wake together.
func (p *requestPacer) wait(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	now := time.Now()
	if p.nextAt.After(now) {
		// Reserve the next free slot for this caller, so a caller arriving
		// while we sleep books the slot after ours, not alongside ours.
		sleepFor := p.nextAt.Sub(now)
		p.nextAt = p.nextAt.Add(p.interval)
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleepFor):
			return nil
		}
	}
	p.nextAt = now.Add(p.interval)
	p.mu.Unlock()
	return nil
}

// Health reports whether the provider looks usable: configured, and with no
// failure in the recent window. Ancient failures age out — a blip last week
// is not today's health.
func (c *Client) Health() (ok bool, detail string) {
	if c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return false, "AI provider not configured"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != "" && time.Since(c.lastErrAt) < 5*time.Minute {
		return false, "last provider error: " + c.lastErr
	}
	return true, "ok"
}

// noteFailure records a provider failure for health checks. The message is
// truncated: health detail names the problem, never dumps bodies or keys.
func (c *Client) noteFailure(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = msg
	c.lastErrAt = time.Now()
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

	// M2 stream-liveness knobs: one bound for silence before headers,
	// one for silence between bytes, one for the restart budget. An
	// explicit stall budget doubles as the header budget (one silence
	// limit to reason about); an explicit AI_STREAM_HEADER_TIMEOUT wins
	// outright, and 0 disables that bound deliberately.
	stall, stallOK := durationKnob("AI_STREAM_STALL", defaultStreamStall)
	headerTimeout := defaultStreamHeaderTimeout
	if stallOK && stall > 0 {
		headerTimeout = stall
	}
	if h, hOK := durationKnob("AI_STREAM_HEADER_TIMEOUT", defaultStreamHeaderTimeout); hOK {
		headerTimeout = h
	}

	return &Client{
		protocol: Provider(cfg.Protocol),
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		// The env-built client is the primary provider (W7 naming).
		providerName: DefaultProviderName,
		// Generous: an agentic turn asks for up to 16k output tokens and a
		// reasoning model can spend minutes on one before the first byte.
		httpClient: &http.Client{Timeout: 10 * time.Minute},
		// Timeout-free by design — the silence bounds are the only clock
		// (see the streamClient field doc).
		streamClient:   &http.Client{Transport: newStreamTransport(headerTimeout)},
		streamStall:    stall,
		streamRestarts: restartKnob(),
		workspaceDir:   workspaceDir,
		pacer:          newRequestPacer(),
	}, nil
}

// Model reports the configured model id, for logging and usage accounting.
func (c *Client) Model() string { return c.model }

// ProviderName reports which provider this client serves — "primary" for
// the env-built client, a providers-block name after ClientForProvider.
// Usage/record and the breaker pin this value per round (W7).
func (c *Client) ProviderName() string {
	if c == nil || c.providerName == "" {
		return DefaultProviderName
	}
	return c.providerName
}

// PacingInterval reports the enforced minimum interval between gateway calls
// (0 when unset). The usage endpoint surfaces it so a long turn reads as
// pacing, not a hang.
func (c *Client) PacingInterval() time.Duration {
	if c == nil || c.pacer == nil {
		return 0
	}
	return c.pacer.interval
}

// clone returns an independent Client sharing the read-only pieces (http
// client, stream client and its liveness knobs, workspace dir, pacer,
// retry hook) and a *snapshot* of the health state, taken under the lock
// so a clone racing a noteFailure reads a consistent pair instead of a
// torn one. Built field-by-field, never struct-copied: a struct copy
// duplicates the health mutex — rejected by copylocks vet (W0), and two
// mutexes around one client's lastErr is exactly the split-brain the
// single mutex exists to prevent.
func (c *Client) clone() Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Client{
		protocol:        c.protocol,
		baseURL:         c.baseURL,
		apiKey:          c.apiKey,
		model:           c.model,
		providerName:    c.providerName,
		httpClient:      c.httpClient,
		streamClient:    c.streamClient,
		streamStall:     c.streamStall,
		streamRestarts:  c.streamRestarts,
		rateLimitWindow: c.rateLimitWindow,
		workspaceDir:    c.workspaceDir,
		lastErr:         c.lastErr,
		lastErrAt:       c.lastErrAt,
		pacer:           c.pacer,
		retryHook:       c.retryHook,
		toolArgs:        c.toolArgs,
	}
}

// WithModel returns a client pointed at a different model on the same
// endpoint with the same credentials. Routing picks models per turn; the copy
// shares the (read-only) http client, the workspace, the pacer and any retry
// hook, and starts from the original's health snapshot. Constructed
// field-by-field via clone, never struct-copied, so the health mutex is
// never duplicated.
func (c *Client) WithModel(model string) *Client {
	cp := c.clone()
	cp.model = model
	return &cp
}

// WithRetryHook attaches a listener the retry loops call before every backoff.
// It is how a stall during a rate limit or a transient server error stops being
// silent: the hook is told which attempt failed, why, and how long the backoff
// is, so the caller can surface it instead of looking hung. A nil hook keeps the
// existing stdout-only behaviour.
func (c *Client) WithRetryHook(fn func(attempt, maxRetries int, reason string, after time.Duration)) *Client {
	cp := c.clone()
	cp.retryHook = fn
	return &cp
}

// WithToolArgs returns a copy that reports streaming tool-argument fragments
// to fn as they arrive from the provider (L8). The callback runs on the
// stream-reader goroutine: callers throttle and must not block.
func (c *Client) WithToolArgs(fn func(tool, fragment string)) *Client {
	cp := c.clone()
	cp.toolArgs = fn
	return &cp
}

// reportRetry calls the hook when one is attached. Called before the backoff
// sleep so the person learns the stall is deliberate and bounded.
func (c *Client) reportRetry(attempt, maxRetries int, reason string, after time.Duration) {
	if c.retryHook != nil {
		c.retryHook(attempt, maxRetries, reason, after)
	}
}

// defaultRateLimitWindow is how long to wait on a 429 the gateway did not
// time for us: one minute, the shape of every requests-per-minute budget seen
// in production (Run-5's "Cluster RPM rate limit exceeded" reset inside it).
const defaultRateLimitWindow = time.Minute

// rateLimitDelay is how long to wait after an HTTP 429: the gateway's own
// Retry-After when it sends one — it knows when its budget refills — else the
// rate window (FM-429a). Deliberately not the flaky-502 ladder: that budget
// is measured in seconds and a rate window is measured in minutes, so 3s/6s
// spends itself before the window that would have cleared the request even
// resets (Run-5 lost a healthy turn at round 8 this way).
func (c *Client) rateLimitDelay(retryAfter string) time.Duration {
	if d := parseRetryAfter(retryAfter); d > 0 {
		return d
	}
	if c.rateLimitWindow > 0 {
		return c.rateLimitWindow
	}
	return defaultRateLimitWindow
}

// parseRetryAfter reads the Retry-After header (delta-seconds or an HTTP
// date). Anything unparseable returns 0 — "the gateway did not say" — so the
// caller falls back to its own window instead of guessing in the gateway's
// name.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t) + time.Second; d > 0 {
			return d
		}
	}
	return 0
}

// waitBackoff sleeps for d and reports cancellation. The rate-window wait is
// long enough that an unstoppable sleep would turn a user stopping the turn
// into a full-window hang (FM-429a), so every backoff this change lengthens
// waits on the context too. A nil result means the wait completed.
func waitBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type PlanResponse struct {
	Message string   `json:"message"`
	Plan    *AppPlan `json:"plan,omitempty"`
}

// PlanStructure states what the plan does with the project template: which
// shipped parts survive, which are demo scaffolding to remove, and what gets
// written fresh. The template is guidance, not a scaffold — a plan that writes
// its screens and leaves the demo home behind produces a broken app, and this
// field is how the planner commits to the decision instead of forgetting it.
type PlanStructure struct {
	Keep    []string `json:"keep"`
	Prune   []string `json:"prune"`
	Write   []string `json:"write"`
	Folders string   `json:"folders"`
}

type AppPlan struct {
	Screens    []Screen       `json:"screens"`
	Features   []PlanFeature  `json:"features"`
	Navigation string         `json:"navigation"`
	Packages   []string       `json:"packages"`
	Structure  *PlanStructure `json:"structure,omitempty"`
}

// PlanFeature is one capability in the build plan — L15's slice contract.
// Legacy plans stored features as plain strings; one unmarshal path accepts
// both shapes (FM-C1a), so every plan written before this contract still
// loads. Files is the build's honest verification bar for the feature
// (FM-C1b): the checklist seeds them onto the task, the heuristic marks the
// feature only when every declared file is written and the project
// compiles, and the audit flips it back when one is missing. Acceptance is
// prose the checklist renders — never parsed, never enforced.
type PlanFeature struct {
	Text       string   `json:"text"`
	Files      []string `json:"files,omitempty"`
	Acceptance string   `json:"acceptance,omitempty"`
}

// UnmarshalJSON accepts the legacy plain-string feature and the shaped
// object through the same path (FM-C1a).
func (f *PlanFeature) UnmarshalJSON(b []byte) error {
	var legacy string
	if err := json.Unmarshal(b, &legacy); err == nil {
		f.Text = legacy
		return nil
	}
	type shaped PlanFeature
	var s shaped
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*f = PlanFeature(s)
	return nil
}

// Validate checks the shaped plan at the plan doors (FM-C1a): every feature
// carries text, and every declared file is an exact workspace-relative path
// — no absolute paths, no traversal, no node_modules, no globs, no "./"
// prefix or backslashes (a declared path the workspace can never produce
// would make the FM-C1b audit flip the feature forever). Errors name the
// offending feature, so a rejection tells the plan model what to fix instead
// of failing as envelope theatre. A feature without files stays legal:
// model-only marking is the honest legacy path.
func (p *AppPlan) Validate() error {
	if p == nil {
		return nil
	}
	for i, f := range p.Features {
		label := strings.TrimSpace(f.Text)
		if label == "" {
			return fmt.Errorf("feature %d: empty text — every feature is one capability line", i+1)
		}
		if len(label) > 70 {
			label = label[:70] + "…"
		}
		for _, declared := range f.Files {
			file := strings.TrimSpace(declared)
			switch {
			case file == "":
				return fmt.Errorf("feature %q: empty file entry", label)
			case file == "." || strings.HasPrefix(file, "./"):
				return fmt.Errorf("feature %q: file %q — write paths as app/… with no ./ prefix", label, declared)
			case filepath.IsAbs(file) || strings.Contains(file, "\\"):
				return fmt.Errorf("feature %q: file %q must be a workspace-relative path with forward slashes", label, declared)
			case file == ".." || strings.HasPrefix(file, "../") ||
				strings.HasSuffix(file, "/..") || strings.Contains(file, "/../"):
				return fmt.Errorf("feature %q: file %q escapes the workspace", label, declared)
			case file == "node_modules" || strings.HasPrefix(file, "node_modules/"):
				return fmt.Errorf("feature %q: file %q is a dependency, not source", label, declared)
			case strings.ContainsAny(file, "*?["):
				return fmt.Errorf("feature %q: file %q must be an exact path — no globs", label, declared)
			}
		}
	}
	return nil
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
func (c *Client) Plan(ctx context.Context, systemPrompt string, history []PromptMessage) (*PlanResponse, error) {
	resp, err := c.chat(ctx, systemPrompt, history)
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

	resp2, err := c.chat(ctx, systemPrompt, retry)
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

// ParsePlan parses a final plan-mode reply's envelope — the same contract
// ai.Plan applies internally, exported for callers that finalise a reply
// themselves (the W3.1 plan tool loop decides its own corrective round, then
// hands the raw text to the doors exactly as the stream door has always
// received it). Anything that isn't the envelope reports false.
func ParsePlan(resp string) (*PlanResponse, bool) {
	return parsePlan(resp)
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

// Chat is the one-shot non-streaming completion: a single prompt, a single
// answer, no tools. Session titles and other one-liner calls use it; the
// loop never does — the loop needs tools and streaming.
func (c *Client) Chat(ctx context.Context, system string, history []PromptMessage) (string, error) {
	return c.chat(ctx, system, history)
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
	// waitApplied marks a round that already waited out a rate window in the
	// status branch below, so the loop-top ladder does not stack its own
	// 3s/6s on top of a minute (FM-429a). Reset every round: a later plain
	// retry still ladders exactly as before.
	waitApplied := false
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 && !waitApplied {
			log.Printf("chatAnthropic: retry attempt %d/%d", attempt+1, maxRetries)
			c.reportRetry(attempt+1, maxRetries, "transient error", time.Duration(attempt*3)*time.Second)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}
		waitApplied = false

		// Gateway pacing: reserve the slot before the request leaves. On a
		// rate-limited gateway the slot was paid for either way; the pacer
		// is what keeps parallel read-only tool rounds from stampeding.
		if err := c.pacer.wait(ctx); err != nil {
			return "", fmt.Errorf("request canceled while pacing: %w", err)
		}

		url := c.baseURL + "/v1/messages"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("User-Agent", "Talon/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("chatAnthropic: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return "", fmt.Errorf("Connection failed. The AI service may be temporarily unavailable. Please try again.")
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			// Quota exhaustion is not throttling: read the body first and fail
			// fast when the budget is spent — retrying a daily quota burns
			// requests for nothing. Body is capped; only the markers matter.
			if resp.StatusCode == 429 {
				peek, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if isQuotaExhausted(string(peek)) {
					err := quotaError(resp.Header.Get("X-RateLimit-Reset"))
					c.noteFailure(err)
					return "", err
				}
			}
			// A 429's wait is owned right here — the gateway's Retry-After
			// when it sends one, else the rate window (FM-429a) — and this
			// round's loop-top ladder is suppressed so the window is not
			// followed by a second, redundant wait. Every other status keeps
			// that ladder untouched: wait and report both stay where they
			// were, because a window-sized backoff would stall a 502 recovery.
			resp.Body.Close()
			if resp.StatusCode == 429 {
				if attempt < maxRetries-1 {
					delay := c.rateLimitDelay(resp.Header.Get("Retry-After"))
					log.Printf("chatAnthropic: HTTP 429 (attempt %d), waiting %v", attempt+1, delay)
					c.reportRetry(attempt+1, maxRetries, "rate limited (HTTP 429)", delay)
					if err := waitBackoff(ctx, delay); err != nil {
						return "", fmt.Errorf("cancelled while waiting to retry: %w", err)
					}
					waitApplied = true
					continue
				}
				return "", fmt.Errorf("Rate limit exceeded. The AI service is receiving too many requests. Please wait a moment and try again.")
			}
			if attempt < maxRetries-1 {
				log.Printf("chatAnthropic: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				c.reportRetry(attempt+1, maxRetries, fmt.Sprintf("server error (HTTP %d)", resp.StatusCode), time.Duration(attempt*3)*time.Second)
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
	// waitApplied marks a round that already waited out a rate window in the
	// status branch below, so the loop-top ladder does not stack its own
	// 3s/6s on top of a minute (FM-429a). Reset every round: a later plain
	// retry still ladders exactly as before.
	waitApplied := false
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 && !waitApplied {
			log.Printf("chatOpenAI: retry attempt %d/%d", attempt+1, maxRetries)
			c.reportRetry(attempt+1, maxRetries, "transient error", time.Duration(attempt*3)*time.Second)
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}
		waitApplied = false

		// Gateway pacing: same law as the anthropic path — every gateway
		// call, from any code path, reserves its slot first.
		if err := c.pacer.wait(ctx); err != nil {
			return "", fmt.Errorf("request canceled while pacing: %w", err)
		}

		url := c.baseURL + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("HTTP-Referer", "https://talon.app")
		req.Header.Set("X-Title", "Talon")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("chatOpenAI: request error (attempt %d): %v", attempt+1, err)
				continue
			}
			return "", fmt.Errorf("Connection failed. The AI service may be temporarily unavailable. Please try again.")
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			// Quota exhaustion is not throttling: read the body first and
			// fail fast when the budget is spent — the guard chatAnthropic
			// already carries, required now that a 429 waits a whole window
			// (FM-429a(iii): retrying a spent daily budget is a wait that
			// can never end in success).
			if resp.StatusCode == 429 {
				peek, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if isQuotaExhausted(string(peek)) {
					err := quotaError(resp.Header.Get("X-RateLimit-Reset"))
					c.noteFailure(err)
					return "", err
				}
			}
			// A 429's wait is owned right here — the gateway's Retry-After
			// when it sends one, else the rate window (FM-429a) — and this
			// round's loop-top ladder is suppressed so the window is not
			// followed by a second, redundant wait. Every other status keeps
			// that ladder untouched: wait and report both stay where they
			// were, because a window-sized backoff would stall a 502 recovery.
			resp.Body.Close()
			if resp.StatusCode == 429 {
				if attempt < maxRetries-1 {
					delay := c.rateLimitDelay(resp.Header.Get("Retry-After"))
					log.Printf("chatOpenAI: HTTP 429 (attempt %d), waiting %v", attempt+1, delay)
					c.reportRetry(attempt+1, maxRetries, "rate limited (HTTP 429)", delay)
					if err := waitBackoff(ctx, delay); err != nil {
						return "", fmt.Errorf("cancelled while waiting to retry: %w", err)
					}
					waitApplied = true
					continue
				}
				return "", fmt.Errorf("Rate limit exceeded. The AI service is receiving too many requests. Please wait a moment and try again.")
			}
			if attempt < maxRetries-1 {
				log.Printf("chatOpenAI: HTTP %d (attempt %d), retrying...", resp.StatusCode, attempt+1)
				c.reportRetry(attempt+1, maxRetries, fmt.Sprintf("server error (HTTP %d)", resp.StatusCode), time.Duration(attempt*3)*time.Second)
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
	// The streaming path is the one the plan and build turns take, so a
	// single flaky 502 from a free-tier route kills the whole turn — the
	// non-streaming callers below retry, and this used to not. Retry the
	// same transient codes they do, on the same backoff, so a gateway
	// hiccup costs one retry instead of the turn.
	const maxRetries = 3
	var lastErr error
	var lastRateLimited bool
	var lastRetryAfter string
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// A 429 waits the rate window (or the gateway's Retry-After);
			// everything else keeps the flaky-502 ladder (FM-429a).
			delay := time.Duration(attempt*3) * time.Second
			if lastRateLimited {
				delay = c.rateLimitDelay(lastRetryAfter)
			}
			log.Printf("openStream: retry attempt %d/%d after %v (waiting %v)", attempt+1, maxRetries, lastErr, delay)
			reason := "transient stream error"
			if lastErr != nil {
				reason = lastErr.Error()
			}
			c.reportRetry(attempt+1, maxRetries, reason, delay)
			if err := waitBackoff(ctx, delay); err != nil {
				return nil, fmt.Errorf("cancelled while waiting to retry: %w", err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("could not build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		setHeaders(req)

		// M2: streams go through the timeout-free client whose transport
		// bounds only the header wait — the between-bytes watchdog wraps
		// the body below. Hand-built clients (tests) keep the shared
		// httpClient, timeout and all, exactly as before.
		client := c.streamClient
		if client == nil {
			client = c.httpClient
		}
		resp, err := client.Do(req)
		if err != nil {
			// A dead connection is transient on every gateway.
			lastErr = fmt.Errorf("could not reach the AI service: %w", err)
			lastRateLimited = false
			lastRetryAfter = ""
			c.noteFailure(lastErr)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			if c.streamStall > 0 {
				return newStalledBody(resp.Body, c.streamStall), nil
			}
			return resp.Body, nil
		}
		// 200 with an error body arrives as an SSE frame and is handled by
		// the reader; a non-200 here is a hard rejection. Only the codes a
		// gateway recovers from on their own are worth another attempt.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch resp.StatusCode {
		case 429, 502, 503, 504:
			lastErr = fmt.Errorf("AI service returned HTTP %d: %s", resp.StatusCode, truncate(string(detail), 500))
			// Only a 429 is a rate window; 502/503/504 keep the ladder.
			lastRateLimited = resp.StatusCode == http.StatusTooManyRequests
			lastRetryAfter = resp.Header.Get("Retry-After")
			c.noteFailure(lastErr)
			continue
		default:
			err := fmt.Errorf("AI service returned HTTP %d: %s", resp.StatusCode, truncate(string(detail), 500))
			c.noteFailure(err)
			return nil, err
		}
	}
	return nil, lastErr
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
			req.Header.Set("HTTP-Referer", "https://talon.dev")
			req.Header.Set("X-Title", "Talon Studio")
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
