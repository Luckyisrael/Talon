package api

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TrustingProxyHeaders reports whether the deployment sits behind a proxy that
// rewrites X-Forwarded-For. The server uses it to decide whether chi's RealIP
// middleware is safe to install.
func TrustingProxyHeaders() bool { return trustProxyHeaders }

// RateLimiter provides per-IP rate limiting using a token bucket.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*tokenBucket
	rate    int           // tokens per window
	window  time.Duration // window duration
	cleanup time.Duration
}

type tokenBucket struct {
	tokens    int
	lastReset time.Time
}

func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*tokenBucket),
		rate:    rate,
		window:  window,
		cleanup: window * 2,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, bucket := range rl.clients {
			if now.Sub(bucket.lastReset) > rl.cleanup {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.clients[ip]
	if !exists {
		rl.clients[ip] = &tokenBucket{tokens: rl.rate - 1, lastReset: time.Now()}
		return true
	}

	if time.Since(bucket.lastReset) > rl.window {
		bucket.tokens = rl.rate - 1
		bucket.lastReset = time.Now()
		return true
	}

	if bucket.tokens <= 0 {
		return false
	}

	bucket.tokens--
	return true
}

// Middleware returns an HTTP middleware that enforces rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(clientKey(r)) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.window.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"too many requests — slow down and try again shortly"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// trustProxyHeaders reports whether X-Forwarded-For can be believed.
//
// It could not be before: the header was used whenever present, so any client
// could send a different X-Forwarded-For on each request and get an unlimited
// number of buckets — the login limiter in particular was trivially bypassable.
// Now it is only consulted when the deployment says it sits behind a proxy.
var trustProxyHeaders = func() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRUST_PROXY"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}()

// clientKey identifies the caller for rate-limiting purposes. An authenticated
// request is keyed by user id, which is both more accurate than an IP and
// unspoofable; anonymous requests fall back to the peer address.
func clientKey(r *http.Request) string {
	if userID := GetUserID(r.Context()); userID > 0 {
		return "user:" + strconv.FormatInt(userID, 10)
	}

	addr := r.RemoteAddr
	if trustProxyHeaders {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The left-most entry is the original client; everything after it was
			// appended by intermediaries.
			if first, _, found := strings.Cut(fwd, ","); found {
				addr = strings.TrimSpace(first)
			} else {
				addr = strings.TrimSpace(fwd)
			}
		}
	}

	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	return "ip:" + addr
}

// AuthRateLimiter returns a rate limiter for auth endpoints (stricter).
func AuthRateLimiter() *RateLimiter {
	return NewRateLimiter(10, time.Minute) // 10 requests per minute
}

// PromptRateLimiter returns a rate limiter for AI prompt endpoints.
func PromptRateLimiter() *RateLimiter {
	return NewRateLimiter(5, time.Minute) // 5 requests per minute
}
