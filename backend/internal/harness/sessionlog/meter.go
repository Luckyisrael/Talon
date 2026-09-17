package sessionlog

// meter.go — the token meter: one accounting of what a session cost and how
// full its context window is, derived by folding the log.
//
// Before this, token accounting lived in three places that did not agree: the
// prompts table (per-turn columns summed by ProjectTokenUsage), the SSE
// stream (EmitUsage, gone on reload), and ai.EstimateTokens (used only to
// decide when to compact). A UI occupancy bar and the compaction decision
// could not read the same number. This is the DSH token-meter shape: a replay
// fold over the log's usage/record events for cumulative spend, and a pressure
// reading from the derived conversation for window occupancy.
//
// Spend is cumulative and ignores the compaction shadow — tokens spent before
// a checkpoint were still spent. Context pressure is derived *after* the
// shadow, because the checkpoint is what the window now actually holds.

import (
	"encoding/json"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
)

// UsageSummary is the cumulative token spend over the events recorded so far.
type UsageSummary struct {
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read_tokens"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	// Calls is the number of model calls recorded — the divisor for any
	// per-call average, and the count a "how many requests did this take"
	// column reads.
	Calls int `json:"calls"`
}

// TokenUsage folds every usage/record event into one cumulative summary. It
// walks the whole log once and deliberately ignores the compaction shadow:
// spend before a checkpoint is still spend, and the meter's job is to report
// what the session cost, not what it currently holds.
func (l *Log) TokenUsage() UsageSummary {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out UsageSummary
	for _, e := range l.rows {
		if e.Kind != events.UsageRecord {
			continue
		}
		var p events.UsagePayload
		if json.Unmarshal(e.Payload, &p) != nil {
			continue
		}
		out.InputTokens += int64(p.InputTokens)
		out.OutputTokens += int64(p.OutputTokens)
		out.CacheRead += int64(p.CacheRead)
		out.CostUSD += p.CostUSD
		out.Calls++
	}
	return out
}

// PressureReport is the occupancy reading for one context window.
type PressureReport struct {
	// Tokens is the estimate for the conversation as it will be replayed —
	// after the compaction shadow, so a checkpointed session reports its real
	// current cost, not the cost of the history it replaced.
	Tokens int `json:"tokens"`
	// MaxTokens is the window the reading was taken against.
	MaxTokens int `json:"max_tokens"`
	// Pressure is Tokens/MaxTokens as a percentage, capped at 100 for display
	// (an over-window conversation is still just "full" to a bar).
	Pressure int `json:"pressure"`
	// Compacted is true when the conversation carries a checkpoint: the number
	// is post-compaction, which is the distinction a "why is this small now"
	// question is asking about.
	Compacted bool `json:"compacted"`
}

// ContextPressure measures the replayed conversation against a window. The
// estimator is passed in so the log does not reach up into the provider layer
// for a token approximation it does not own.
func (l *Log) ContextPressure(maxTokens int, estimator func(messages int) int) PressureReport {
	derived := l.DeriveMessages()
	out := PressureReport{
		MaxTokens: maxTokens,
		Compacted: l.shadowSeq > 0 || l.shadowSummary != "",
	}
	if estimator != nil {
		out.Tokens = estimator(len(derived))
	}
	if maxTokens > 0 {
		out.Pressure = out.Tokens * 100 / maxTokens
		if out.Pressure > 100 {
			out.Pressure = 100
		}
	}
	return out
}
