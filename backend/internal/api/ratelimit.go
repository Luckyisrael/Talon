package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter provides per-IP rate limiting using a token bucket.
type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*tokenBucket
	rate     int           // tokens per window
	window   time.Duration // window duration
	cleanup  time.Duration
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
		ip := r.RemoteAddr
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			ip = fwd
		}

		// Strip port from IP address (RemoteAddr is "ip:port")
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}

		if !rl.Allow(ip) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// AuthRateLimiter returns a rate limiter for auth endpoints (stricter).
func AuthRateLimiter() *RateLimiter {
	return NewRateLimiter(10, time.Minute) // 10 requests per minute
}

// PromptRateLimiter returns a rate limiter for AI prompt endpoints.
func PromptRateLimiter() *RateLimiter {
	return NewRateLimiter(5, time.Minute) // 5 requests per minute
}
