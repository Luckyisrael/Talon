package ai

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// M2 — stream liveness.
//
// Two proven ways a healthy turn used to die: the shared http.Client's
// 10-minute Timeout killed long-but-flowing streams (a reasoning round
// can think silently past any fixed total), and a provider that accepted
// the request then went quiet left the reader blocked with no bound at
// all until that same timeout fired minutes later. The replacement is a
// pair of silence bounds plus a restart budget:
//
//   - ResponseHeaderTimeout (streamClient's transport) bounds the wait
//     for response headers;
//   - stalledBody bounds the silence BETWEEN bytes on an open stream —
//     gap-based, so a slow-but-flowing stream is never killed;
//   - the sink branch of chatWithToolsOpenAI resends a stream that died
//     or truncated mid-turn, byte-identically, up to streamRestarts
//     times (agentic.go).
//
// The streaming client's Timeout is deliberately 0: the watchdogs are
// the only clock, so a stream that keeps producing bytes is never cut
// for being long.

const (
	// defaultStreamStall is how long an open stream may go without a
	// single byte before the watchdog kills the body. Between-bytes, not
	// total: reasoning models pause, and a fixed total-time cap is what
	// killed healthy turns before.
	defaultStreamStall = 3 * time.Minute

	// defaultStreamHeaderTimeout bounds waiting for response headers —
	// the silence before the stream exists at all. Headers should be
	// near-instant, so this is tighter than the stall bound.
	defaultStreamHeaderTimeout = 2 * time.Minute

	// defaultStreamRestarts is how many times a stream that died or
	// truncated mid-turn is resent before the turn fails (so a turn
	// makes at most 1+defaultStreamRestarts requests for one round).
	defaultStreamRestarts = 2

	// maxStreamRestarts caps AI_STREAM_RESTARTS: a typo must not turn a
	// flapping provider into an unbounded retry loop.
	maxStreamRestarts = 5

	// streamRestartBackoff is the pause before a byte-identical resend —
	// long enough that a gateway dropping us gets a moment, short enough
	// that a transient blip doesn't read as a stall to the user.
	streamRestartBackoff = 500 * time.Millisecond
)

// errStreamStalled is what a watchdog-killed read returns. The wording
// is load-bearing: friendlyAIError maps "timeout"/"deadline"/"cancelled"
// to generic copy, and none of those appear here — the caller (and the
// user) should see exactly which bound fired.
func errStreamStalled(after time.Duration) error {
	return fmt.Errorf("stream stalled: no data from the provider for %s", after)
}

// stalledBody wraps a streaming response body with the between-bytes
// watchdog. The timer is armed before each potentially blocking Read and
// disarmed when it returns, so only silence counts — bytes in flight
// reset the budget by definition. A firing timer closes the underlying
// body (unblocking the Read) and the Read returns errStreamStalled.
type stalledBody struct {
	rc    io.ReadCloser
	stall time.Duration

	mu sync.Mutex
	// gen invalidates a pending kill: any Close or completed Read bumps
	// it, and kill only fires for the generation it was armed under.
	gen uint64
	// killedAt records the generation a kill actually fired for, so the
	// Read whose silence caused it reports the stall reason instead of
	// whatever close error the body produced.
	killedAt uint64
}

func newStalledBody(rc io.ReadCloser, stall time.Duration) *stalledBody {
	return &stalledBody{rc: rc, stall: stall}
}

func (b *stalledBody) Read(p []byte) (int, error) {
	if b.stall <= 0 {
		return b.rc.Read(p)
	}
	b.mu.Lock()
	b.gen++
	g := b.gen
	b.mu.Unlock()

	timer := time.AfterFunc(b.stall, func() { b.kill(g) })
	n, err := b.rc.Read(p)
	timer.Stop()

	b.mu.Lock()
	killed := b.killedAt == g
	b.gen++ // this Read is over: a kill for g is no longer wanted
	b.mu.Unlock()

	if killed {
		// The watchdog already closed the body; drop whatever bytes came
		// in and report the stall — the stream is dead either way.
		return 0, errStreamStalled(b.stall)
	}
	return n, err
}

// kill fires for generation g only: if the Read returned (or Close ran)
// first, gen has moved on and the timer's late fire is a no-op.
func (b *stalledBody) kill(g uint64) {
	b.mu.Lock()
	if b.gen != g {
		b.mu.Unlock()
		return
	}
	b.killedAt = g
	b.mu.Unlock()
	_ = b.rc.Close()
}

func (b *stalledBody) Close() error {
	b.mu.Lock()
	b.gen++ // invalidate any pending kill — this body is done by choice
	b.mu.Unlock()
	return b.rc.Close()
}

// newStreamTransport clones the default transport so proxy, dial, TLS
// and HTTP/2 defaults carry over, with only ResponseHeaderTimeout set.
// The comma-ok fallback covers scaffolding that replaced
// http.DefaultTransport wholesale (some test binaries do): a fresh
// transport with sane defaults, still bounded on headers.
func newStreamTransport(headerTimeout time.Duration) http.RoundTripper {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		cp := tr.Clone()
		cp.ResponseHeaderTimeout = headerTimeout
		return cp
	}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: headerTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// durationKnob reads a Go-duration env knob (e.g. "400ms"). ok reports
// the value is present AND valid — callers use it to decide whether an
// explicit (possibly zero) value overrides a related default. Invalid
// values log and fall back rather than silently disabling a bound.
func durationKnob(name string, fallback time.Duration) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, false
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed < 0 {
		log.Printf("ai: ignoring invalid %s=%q, using %s", name, raw, fallback)
		return fallback, false
	}
	return parsed, true
}

// restartKnob reads AI_STREAM_RESTARTS (0 = restarts off). Unset keeps
// the product default; anything invalid or out of range falls back to
// it — a typo must not disable recovery or enable a retry storm.
func restartKnob() int {
	raw := strings.TrimSpace(os.Getenv("AI_STREAM_RESTARTS"))
	if raw == "" {
		return defaultStreamRestarts
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > maxStreamRestarts {
		log.Printf("ai: ignoring invalid AI_STREAM_RESTARTS=%q, using %d", raw, defaultStreamRestarts)
		return defaultStreamRestarts
	}
	return n
}
