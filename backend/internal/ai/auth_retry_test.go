package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// FM-AUTHa — written first (TEST_RULES §3). The exact body agentrouter
// returned when it rejected a valid key mid-run (2026-10-03, prompt 85:
// rounds 1–14 healthy, round 15 401, the immediate Continue 4 s later
// 401 again — two turns killed by a gateway-side identity flap, never a
// bad key). The law: a first refusal waits `authRetryDelay` and retries
// once at every call site; a second refusal returns the original
// status-bearing error so `friendlyAIError`'s 401 mapping stays
// byte-identical. Red today: every site returns on the first refusal —
// one request reaches the gateway.
const identityRejectedBody = `{"error":{"message":"unauthorized client detected, contact support for assistance at https://discord.gg/HgekCyHJqB"},"message":"UNAUTHENTICATED","success":false,"type":"unauthorized_client_error"}`

// rejecting401Server is a gateway that refuses every request the way
// agentrouter refused ours, counting what arrives.
func rejecting401Server(hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(identityRejectedBody))
	}))
}

// authRetryClient points at baseURL with the identity-rejection backoff
// shrunk to test size (3 s production default — the rateLimitWindow
// pattern).
func authRetryClient(baseURL string) *Client {
	return &Client{
		protocol:    ProtocolAnthropic,
		baseURL:     baseURL,
		apiKey:      "k",
		model:       "m",
		httpClient:  http.DefaultClient,
		authBackoff: 10 * time.Millisecond,
	}
}

// assertIdentityRejectionFailed is the shared contract: the turn still
// fails, the error still names the status the friendly mapping keys on,
// and the gateway saw exactly two requests — one refusal, one retry.
func assertIdentityRejectionFailed(t *testing.T, calls int32, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a permanently rejected request must still fail")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("the terminal error must still name the status (friendlyAIError maps on it), got %v", err)
	}
	if calls != 2 {
		t.Errorf("FM-AUTHa: a 401 must be retried exactly once — %d requests reached the gateway, want 2", calls)
	}
}

// openStream is the path plan and build turns take (chatWithToolsAnthropic
// and both streaming chat paths call it) — the exact site that died on
// round 15 of prompt 85.
func TestOpenStream401GetsExactlyOneRetryThenFails(t *testing.T) {
	var calls int32
	srv := rejecting401Server(&calls)
	defer srv.Close()

	c := authRetryClient(srv.URL)
	body, err := c.openStream(context.Background(), srv.URL, []byte(`{}`), func(*http.Request) {})
	if body != nil {
		_ = body.Close()
	}
	assertIdentityRejectionFailed(t, atomic.LoadInt32(&calls), err)
}

// doWithRetry serves every vision call and the buffered agentic path —
// same law, same count.
func TestDoWithRetry401GetsExactlyOneRetryThenFails(t *testing.T) {
	var calls int32
	srv := rejecting401Server(&calls)
	defer srv.Close()

	c := authRetryClient(srv.URL)
	_, err := c.doWithRetry(context.Background(), srv.URL, []byte(`{}`), func(*http.Request) {}, "test-call")
	assertIdentityRejectionFailed(t, atomic.LoadInt32(&calls), err)
}

// chatAnthropic is the buffered anthropic chat path.
func TestChatAnthropic401GetsExactlyOneRetryThenFails(t *testing.T) {
	var calls int32
	srv := rejecting401Server(&calls)
	defer srv.Close()

	c := authRetryClient(srv.URL)
	_, err := c.chatAnthropic(context.Background(), "system", nil)
	assertIdentityRejectionFailed(t, atomic.LoadInt32(&calls), err)
}

// chatOpenAI is the buffered openai chat path.
func TestChatOpenAI401GetsExactlyOneRetryThenFails(t *testing.T) {
	var calls int32
	srv := rejecting401Server(&calls)
	defer srv.Close()

	c := authRetryClient(srv.URL)
	_, err := c.chatOpenAI(context.Background(), "system", nil)
	assertIdentityRejectionFailed(t, atomic.LoadInt32(&calls), err)
}
