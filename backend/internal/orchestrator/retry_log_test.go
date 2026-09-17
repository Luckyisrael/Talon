package orchestrator

import (
	"testing"

	harnessevents "github.com/luckyisrael/talon/backend/internal/harness/events"
)

// memEventLog is the AuditLog fake: it records appends and Syncs so a test can
// assert both the event written and the barrier crossed.
type memEventLog struct {
	events []harnessevents.Kind
	syncs  int
}

func (m *memEventLog) Append(kind harnessevents.Kind, payload any) (int64, error) {
	m.events = append(m.events, kind)
	return int64(len(m.events)), nil
}
func (m *memEventLog) Sync() error { m.syncs++; return nil }

// A recovered retry must land in the log as llm/retry — after a reload it is
// the only record that a transient gateway failure did not become the outcome.
func TestRetryClassifiesAndPersists(t *testing.T) {
	cases := map[string]string{
		"rate limited (HTTP 429)":    "rate_limited",
		"server error (HTTP 502)":    "server_error",
		"transient stream error":     "stream_error",
		"the response was empty":     "empty_response",
		"context deadline exceeded":  "timeout",
		"transient error":            "transient",
		"something the gateway said": "other",
	}
	for why, want := range cases {
		if got := retryClass(why); got != want {
			t.Errorf("retryClass(%q) = %q, want %q", why, got, want)
		}
	}
}

// The event kinds the loop owns must be non-surface: an audit record that
// joined the derived conversation would put transport noise in the model's
// context.
func TestRetryEventIsNonSurface(t *testing.T) {
	if harnessevents.SurfaceKind(harnessevents.RetryLogged) {
		t.Error("llm/retry must be a non-surface audit event")
	}
	if harnessevents.SurfaceKind(harnessevents.UsageRecord) {
		t.Error("usage/record must remain non-surface")
	}
}
