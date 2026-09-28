package ai

// FM-UAa — transport identity a gateway authenticates on.
//
// agentrouter.org refuses every request whose User-Agent is not exactly
// "claude-cli/2.0.0 (external, cli)" — 401 "unauthorized client detected"
// before the key is read — and the backend hardcoded Talon/1.0 at two call
// sites while sending Go's default on the rest. The second provider for the
// model comparison was therefore unreachable by a constant, not a capability.
//
// Two failure modes, two tests: (i) the identity must be configurable, and
// (ii) it must be attached where requests are built — one constructor all
// request paths share — because a partial answer is a silent 401 on the
// streaming path plan and build turns actually take.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// FM-UAa(i) — AI_USER_AGENT must reach the client. Without the knob the only
// value the product can ever send is the compiled-in default, so a gateway
// that keys on the header cannot be reached at all.
func TestUserAgentEnvKnobConfiguresTheClient(t *testing.T) {
	const want = "claude-cli/2.0.0 (external, cli)"

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("AI_API_KEY", "k")
	t.Setenv("AI_MODEL", "m")
	t.Setenv("AI_USER_AGENT", want)

	c, err := New()
	if err != nil {
		t.Fatalf("New() with a complete env: %v", err)
	}
	if got := c.ua(); got != want {
		t.Errorf("ua() = %q, want %q — FM-UAa(i): AI_USER_AGENT must configure the client, or a gateway that authenticates on this header is unreachable",
			got, want)
	}
}

// Keep: the default is unchanged, so today's gateways keep seeing the same
// identity when no knob is set.
func TestUserAgentDefaultsToTalon(t *testing.T) {
	c := &Client{}
	if got := c.ua(); got != "Talon/1.0" {
		t.Errorf("ua() = %q, want the default %q — the identity must not go blank when nothing is configured", got, "Talon/1.0")
	}
}

// TestEveryRequestPathSendsTheConfiguredUserAgent covers FM-UAa(ii): all four
// request builders — the streaming path, the retry helper (vision + buffered
// agentic), and both chat paths — funnel through the one constructor, so a
// header set there reaches every one of them. Each subtest records what the
// wire actually carried.
func TestEveryRequestPathSendsTheConfiguredUserAgent(t *testing.T) {
	const want = "claude-cli/2.0.0 (external, cli)"

	cases := []struct {
		name     string
		protocol Provider
		// body satisfies the parser of the path under test.
		body string
		call func(c *Client) error
	}{
		{
			// The path plan and build turns take. Setting no header here
			// means Go's default goes out — the exact 401 agentrouter sends.
			name:     "openStream",
			protocol: ProtocolOpenAI,
			body:     `{"ok":true}`,
			call: func(c *Client) error {
				rc, err := c.openStream(context.Background(), c.baseURL, []byte(`{}`), func(*http.Request) {})
				if rc != nil {
					_ = rc.Close()
				}
				return err
			},
		},
		{
			name:     "doWithRetry",
			protocol: ProtocolOpenAI,
			body:     `{"ok":true}`,
			call: func(c *Client) error {
				_, err := c.doWithRetry(context.Background(), c.baseURL, []byte(`{}`), func(*http.Request) {}, "testCall")
				return err
			},
		},
		{
			name:     "chatOpenAI",
			protocol: ProtocolOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`,
			call: func(c *Client) error {
				_, err := c.Chat(context.Background(), "system", nil)
				return err
			},
		},
		{
			name:     "chatAnthropic",
			protocol: ProtocolAnthropic,
			body:     `{"content":[{"type":"text","text":"ok"}]}`,
			call: func(c *Client) error {
				_, err := c.Chat(context.Background(), "system", nil)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				if seen == "" {
					seen = r.Header.Get("User-Agent")
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := &Client{
				protocol:   tc.protocol,
				baseURL:    srv.URL,
				apiKey:     "k",
				model:      "m",
				httpClient: http.DefaultClient,
				userAgent:  want,
			}
			if err := tc.call(c); err != nil {
				t.Fatalf("%s call: %v", tc.name, err)
			}

			mu.Lock()
			got := seen
			mu.Unlock()
			if got != want {
				t.Errorf("%s sent User-Agent %q, want %q — FM-UAa(ii): every request path must attach the identity, or the ones that do not are silently 401'd",
					tc.name, got, want)
			}
		})
	}
}
