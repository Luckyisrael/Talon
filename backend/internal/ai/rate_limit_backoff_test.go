package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// FM-429a — written first (TEST_RULES §3). The failure: a retry budget
// measured in attempts cannot outlast a rate window measured in minutes.
// openStream — the path both the plan and the build turn take — backs off
// attempt*3s (3s then 6s), a ladder written for flaky 502s, so Run-5's
// "Cluster RPM rate limit exceeded" bursts burned the whole budget in
// ~15 seconds and killed a healthy turn at round 8 while the 60s window
// that would have cleared it was still open (turn FAILED_NEEDS_USER).
// Three concrete defects, one law:
//
//  1. openStream never reads Retry-After at all, even though
//     chatAnthropic/chatOpenAI parse it — the gateway's own word on when
//     the budget refills was thrown away on the only path turns use.
//  2. With no Retry-After, every path waited 3s/6s — correct for a 502,
//     futile against a per-minute RPM window.
//  3. chatAnthropic/chatOpenAI's no-Retry-After branch reports a delay
//     and then sleeps none of it — the stall the user is told about is
//     not the stall that happens.
//
// The law: a 429 waits the gateway's Retry-After when sent, else the rate
// window; every other transient keeps the 3s/6s ladder exactly as it is;
// and a wait long enough to matter must be cancellable, because a
// ctx-unaware 60s sleep turns "stop this turn" into a 60s hang.

// The exact Atria gateway 429 body that killed Run-5 at round 8. It
// carries no Retry-After header, which is the case the client has to
// reason about without the gateway's help.
const run5RateLimitBody = `{"error":{"code":"upstream_request_rejected","message":"Cluster RPM rate limit exceeded.","type":"invalid_request_error"}}`

// recordingRetryClient returns a client against baseURL whose retry hook
// records every reported backoff and then calls onDecision (so a test can
// cancel the context and observe the decision instead of sleeping it out).
// The rate window is set to a test-sized value.
func recordingRetryClient(baseURL string, window time.Duration, onDecision func()) (*Client, func() []time.Duration) {
	var mu sync.Mutex
	var delays []time.Duration
	c := &Client{
		protocol:   ProtocolOpenAI,
		baseURL:    baseURL,
		apiKey:     "k",
		model:      "m",
		httpClient: http.DefaultClient,
	}
	c = c.WithRetryHook(func(attempt, maxRetries int, reason string, after time.Duration) {
		mu.Lock()
		delays = append(delays, after)
		mu.Unlock()
		if onDecision != nil {
			onDecision()
		}
	})
	c.rateLimitWindow = window
	return c, func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		out := make([]time.Duration, len(delays))
		copy(out, delays)
		return out
	}
}

// A 429 with no Retry-After must wait the rate window — not the flaky-502
// ladder — and the turn must then recover. This is Run-5's exact failure:
// the budget ran out 45s before the window would have cleared it.
func TestOpenStream429WaitsOutRateWindow(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(run5RateLimitBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	const window = 25 * time.Millisecond
	c, delays := recordingRetryClient(srv.URL, window, nil)

	body, err := c.openStream(context.Background(), srv.URL, []byte(`{}`), func(*http.Request) {})
	if err != nil {
		t.Fatalf("openStream must recover once the window has passed: %v", err)
	}
	_ = body.Close()

	got := delays()
	if len(got) != 2 {
		t.Fatalf("retry hook fired %d times, want 2 (one per failed attempt)", len(got))
	}
	for i, d := range got {
		if d != window {
			t.Errorf("retry %d waited %v — FM-429a: a 429 without Retry-After must wait the rate window (%v), not the flaky-502 ladder",
				i+1, d, window)
		}
	}
}

// When the gateway says when the budget refills, that number wins over the
// configured window — and openStream, which ignores the header today, must
// honor it like the non-streaming paths already do. The hook cancels the
// context so the test reads the decision instead of sleeping it out.
func TestOpenStreamHonorsRetryAfterHeader(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(run5RateLimitBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, delays := recordingRetryClient(srv.URL, 25*time.Millisecond, cancel)

	body, err := c.openStream(ctx, srv.URL, []byte(`{}`), func(*http.Request) {})
	if body != nil {
		_ = body.Close()
	}
	if err == nil {
		t.Error("expected the cancelled wait to surface as an error")
	}

	got := delays()
	if len(got) == 0 {
		t.Fatal("retry hook never fired — no backoff decision was reported")
	}
	if got[0] != time.Second {
		t.Errorf("429 with Retry-After: 1 reported a wait of %v — FM-429a: the gateway's own Retry-After must win (want 1s)", got[0])
	}
}

// The flaky-502 ladder must not regress: a 502 keeps its 3s backoff and
// must not pick up the rate window (which would stall every transient
// recovery by a minute).
func TestStreamKeepsFastLadderFor502(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, delays := recordingRetryClient(srv.URL, 25*time.Millisecond, cancel)

	body, err := c.openStream(ctx, srv.URL, []byte(`{}`), func(*http.Request) {})
	if body != nil {
		_ = body.Close()
	}
	_ = err

	got := delays()
	if len(got) == 0 {
		t.Fatal("retry hook never fired for a 502")
	}
	if got[0] != 3*time.Second {
		t.Errorf("502 reported a wait of %v — FM-429a: only 429 waits the rate window; a 502 keeps the 3s ladder", got[0])
	}
}

// chatOpenAI's no-Retry-After branch reports a delay and then sleeps none
// of it — the reported stall and the real stall must be the same number.
// The window is the only thing that changes; the call still recovers.
func TestChatOpenAI429ReportsTheWaitItTakes(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(run5RateLimitBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	const window = 25 * time.Millisecond
	c, delays := recordingRetryClient(srv.URL, window, nil)

	text, err := c.Chat(context.Background(), "system", nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q, want %q", text, "ok")
	}

	got := delays()
	if len(got) != 1 {
		t.Fatalf("retry hook fired %d times, want 1", len(got))
	}
	if got[0] != window {
		t.Errorf("reported wait %v — FM-429a: a 429 with no Retry-After must report (and take) the rate window %v",
			got[0], window)
	}
}

// FM-429a(iii) — quota exhaustion is not throttling, so it is never a wait.
// chatAnthropic fails fast on a spent daily budget (isQuotaExhausted);
// chatOpenAI has no guard, and lengthening its 429 wait would turn an
// instant failure into two full rate windows before the same failure. The
// guard ships with the fix or the fix is a regression.
func TestChatOpenAIQuotaExhaustionFailsFast(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(e2eQuotaBody)) // the spent-budget body, quota_test.go
	}))
	defer srv.Close()

	c, delays := recordingRetryClient(srv.URL, 25*time.Millisecond, nil)

	_, err := c.Chat(context.Background(), "system", nil)
	if err == nil {
		t.Fatal("a spent daily budget must surface as an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "retry") {
		t.Errorf("err = %q — FM-429a(iii): quota must fail fast with quotaError's guidance instead of retrying as if throttled", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Errorf("server hit %d times — a spent budget never clears, so exactly one call", n)
	}
	if got := len(delays()); got != 0 {
		t.Errorf("%d backoffs reported — quota exhaustion is never a wait", got)
	}
}

// A 502 on the non-streaming path must keep the loop-top ladder. This pins
// the wait-ownership split from both sides: only a 429 moves its wait into
// the status branch (and suppresses the ladder for that round), and the
// suppression must never leak onto a plain transient retry — where it would
// take the rate window, stalling a 502 recovery by a minute.
func TestChatOpenAI502KeepsLoopLadderNotWindow(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	const window = 25 * time.Millisecond
	c, delays := recordingRetryClient(srv.URL, window, nil)

	text, err := c.Chat(context.Background(), "system", nil)
	if err != nil {
		t.Fatalf("Chat must recover after the 502: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q, want %q", text, "ok")
	}

	got := delays()
	if len(got) < 2 {
		t.Fatalf("retry hook fired %d times, want the status branch and the loop top to report", len(got))
	}
	for i, d := range got {
		if d == window {
			t.Errorf("reported wait %d = %v — FM-429a: only a 429 takes the rate window; a 502 must not", i, d)
		}
	}
	if last := got[len(got)-1]; last != 3*time.Second {
		t.Errorf("last reported wait = %v, want 3s — the loop-top ladder must still deliver a 502's backoff", last)
	}
}

// FM-429a(iv) — doWithRetry is the fourth gateway call site: every vision call
// and the buffered agentic path. With no Retry-After its own ladder is
// 2s/4s/8s ≈ 14s, futility against a per-minute window (the same defect a
// third time), so a 429 here must wait the rate window too. Asserted as wall
// time because the wait is what the turn actually pays: the ladder it must not
// pay is 2s, the window it must take is 25ms, and the assertion sits far
// enough from both to be unambiguous.
func TestDoWithRetry429WaitsRateWindow(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	const window = 25 * time.Millisecond
	c, delays := recordingRetryClient(srv.URL, window, nil)

	start := time.Now()
	body, err := c.doWithRetry(context.Background(), srv.URL+"/v1/messages", []byte(`{}`),
		func(*http.Request) {}, "testCall")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("doWithRetry must recover after a throttled attempt: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("empty response body")
	}
	if elapsed >= time.Second {
		t.Errorf("recovered in %v — FM-429a(iv): a 429 with no Retry-After must wait the rate window (%v), not the 2s/4s/8s ladder",
			elapsed, window)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("server hit %d times, want 2 (throttled once, then succeeded)", n)
	}
	if got := delays(); len(got) != 1 || got[0] != window {
		t.Errorf("reported waits %v — FM-429a(iv): the window wait must be reported so a minute-long stall is not a hung request", got)
	}
}
