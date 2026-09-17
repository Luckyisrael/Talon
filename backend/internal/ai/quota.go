package ai

import (
	"fmt"
	"strings"
	"time"
)

// Quota-exhaustion markers: provider error text meaning the budget is spent
// (daily free-tier allowance, no credits) rather than transient throttling.
// Retrying these is pure waste — the budget refills on the provider's clock,
// not on our retry clock — so every retry site checks first and fails fast.
// Matched case-insensitively against the response body.
var quotaMarkers = []string{
	"free-models-per-day",
	"free_model",
	"daily reset",
	"daily quota",
	"quota_exceeded",
	"quota exceeded",
	"insufficient credits",
	"insufficient_quota",
	"purchase credits",
	"add credits",
	"billing limit",
}

// isQuotaExhausted reports whether provider error text signals a spent quota
// rather than transient throttling.
func isQuotaExhausted(text string) bool {
	lower := strings.ToLower(text)
	for _, m := range quotaMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// quotaError builds the user-facing error for an exhausted budget. When the
// provider tells us the reset time (OpenRouter's X-RateLimit-Reset, millis
// epoch), it is included so the user knows exactly how long the wait is.
func quotaError(resetHeader string) error {
	if resetHeader != "" {
		var ms int64
		if _, err := fmt.Sscanf(strings.TrimSpace(resetHeader), "%d", &ms); err == nil && ms > 0 {
			reset := time.UnixMilli(ms)
			if wait := time.Until(reset); wait > 0 {
				return fmt.Errorf("Daily free-model quota exhausted on the AI provider — resets in %s (at %s). Add credits to continue now; retrying before then cannot help.",
					wait.Round(time.Minute), reset.Local().Format("15:04"))
			}
		}
	}
	return fmt.Errorf("Daily free-model quota exhausted on the AI provider. Add credits to continue now; retrying before the daily reset cannot help.")
}
