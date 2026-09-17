package sessionlog

import (
	"testing"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
)

// The meter sums every usage/record event, cumulatively, and ignores the
// compaction shadow — tokens spent before a checkpoint were still spent.
func TestTokenUsageSumsAllCalls(t *testing.T) {
	l := newLog(t, "meter")
	for _, u := range []events.UsagePayload{
		{InputTokens: 1000, OutputTokens: 200, CacheRead: 50, CostUSD: 0.01, Model: "big"},
		{InputTokens: 800, OutputTokens: 150, CacheRead: 600, CostUSD: 0.008, Model: "big"},
		{InputTokens: 1200, OutputTokens: 300, CacheRead: 0, CostUSD: 0.02, Model: "small"},
	} {
		if _, err := l.Append(events.UsageRecord, u); err != nil {
			t.Fatal(err)
		}
	}

	got := l.TokenUsage()
	if got.Calls != 3 {
		t.Errorf("Calls: got %d, want 3", got.Calls)
	}
	if got.InputTokens != 3000 {
		t.Errorf("InputTokens: got %d, want 3000", got.InputTokens)
	}
	if got.OutputTokens != 650 {
		t.Errorf("OutputTokens: got %d, want 650", got.OutputTokens)
	}
	if got.CacheRead != 650 {
		t.Errorf("CacheRead: got %d, want 650", got.CacheRead)
	}
	if got.CostUSD < 0.037 || got.CostUSD > 0.039 {
		t.Errorf("CostUSD: got %f, want ~0.038", got.CostUSD)
	}
}

// An empty log reports zero, not nil — the UI reads this unconditionally.
func TestTokenUsageEmptyLog(t *testing.T) {
	l := newLog(t, "meter-empty")
	got := l.TokenUsage()
	if got != (UsageSummary{}) {
		t.Errorf("an empty log must report a zero summary, got %+v", got)
	}
}

// Pressure is the replayed conversation against the window, capped for
// display. It must read the window the compactor uses — an occupancy bar and
// the compaction decision agreeing is the whole point of one accounting.
func TestContextPressureCappedAtFull(t *testing.T) {
	l := newLog(t, "meter-pressure")
	if _, err := l.Append(events.UserMessage, msg("user", "build the wallet")); err != nil {
		t.Fatal(err)
	}

	tiny := l.ContextPressure(10, func(messages int) int { return messages * 1000 })
	if tiny.Pressure != 100 {
		t.Errorf("an over-window conversation must read as full (100), got %d", tiny.Pressure)
	}
	if tiny.Compacted {
		t.Error("a log with no shadow must report compacted=false")
	}

	// A shadowed log reads pressure from what survived the checkpoint, and says
	// it is compacted — that is the distinction a "why is this small now"
	// question is asking about.
	if err := l.Shadow(l.Seq(), "checkpoint", "test"); err != nil {
		t.Fatal(err)
	}
	after := l.ContextPressure(10, func(messages int) int { return 0 })
	if !after.Compacted {
		t.Error("a shadowed log must report compacted=true")
	}

	// A small conversation against a big window reads its real share.
	small := l.ContextPressure(100000, func(messages int) int { return 5000 })
	if small.Pressure != 5 {
		t.Errorf("5000/100000 must read 5%%, got %d", small.Pressure)
	}
	if small.Tokens != 5000 {
		t.Errorf("Tokens: got %d, want 5000", small.Tokens)
	}
}
