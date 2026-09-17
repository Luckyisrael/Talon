package ai

import (
	"strings"
	"testing"
)

// The exact OpenRouter daily-quota body that killed the TipJar E2E (4
// retries × 429, all quota). The detector must catch it; transient
// throttling text must not trip it.
const e2eQuotaBody = `{"error":{"message":"Rate limit exceeded: free-models-per-day. Add 5 credits to unlock 1000 free model requests per day","code":429,"metadata":{"limit_source":"openrouter_free_tier_daily","remedy_hint":"Wait for the daily reset (see X-RateLimit-Reset), or purchase credits to raise your free-model daily limit."}}}`

func TestIsQuotaExhausted(t *testing.T) {
	if !isQuotaExhausted(e2eQuotaBody) {
		t.Error("E2E quota body must classify as exhausted")
	}
	transient := []string{
		`{"error":{"message":"Rate limit exceeded, please slow down","code":429}}`,
		`{"error":{"message":"Provider returned error 503","code":503}}`,
		`{"error":{"message":"Upstream timeout","code":504}}`,
		``,
	}
	for _, b := range transient {
		if isQuotaExhausted(b) {
			t.Errorf("transient body must NOT classify as exhausted: %q", b)
		}
	}
}

func TestQuotaErrorMentionsReset(t *testing.T) {
	// X-RateLimit-Reset is millis epoch; far-future value must render a wait.
	err := quotaError("9999999999999")
	if err == nil || !strings.Contains(err.Error(), "resets in") {
		t.Errorf("expected reset timing in error, got %v", err)
	}
	if err := quotaError(""); err == nil || strings.Contains(err.Error(), "resets in") {
		t.Errorf("no header means no reset timing, got %v", err)
	}
	if err := quotaError(""); err == nil || !strings.Contains(strings.ToLower(err.Error()), "retry") {
		t.Errorf("error must tell the user retrying cannot help, got %v", err)
	}
}
