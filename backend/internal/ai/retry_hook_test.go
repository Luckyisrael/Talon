package ai

import (
	"strings"
	"testing"
	"time"
)

// A retry must reach the hook before the backoff, so a stall during a rate
// limit is visible rather than looking like a hung request. WithRetryHook must
// also survive WithModel — a routed model keeps gateway pacing and the hook.
func TestRetryHookFiresBeforeBackoff(t *testing.T) {
	var seen []string
	base := &Client{protocol: "openai"}
	client := base.WithRetryHook(func(attempt, maxRetries int, why string, after time.Duration) {
		seen = append(seen, why)
	})

	// The shallow copy must carry the hook.
	copied := client.WithModel("gpt-4o")
	copied.reportRetry(1, 3, "rate limited (HTTP 429)", 4*time.Second)
	client.reportRetry(2, 3, "server error (HTTP 502)", 3*time.Second)

	if len(seen) != 2 {
		t.Fatalf("the hook must fire once per report, got %d calls", len(seen))
	}
	if !strings.Contains(seen[0], "rate limited") {
		t.Errorf("call 0 reason: got %q", seen[0])
	}
	if !strings.Contains(seen[1], "server error") {
		t.Errorf("call 1 reason: got %q", seen[1])
	}
	if client.Model() == copied.Model() {
		t.Errorf("WithModel must change the model, both report %q", client.Model())
	}
}

// A client without a hook keeps the old behaviour: no panic, no output.
func TestRetryHookNilIsSilent(t *testing.T) {
	c := &Client{protocol: "anthropic"}
	c.reportRetry(1, 3, "anything", time.Second)
}
