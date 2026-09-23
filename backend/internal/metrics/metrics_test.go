package metrics

import (
	"strings"
	"testing"
)

func TestObserveAndRender(t *testing.T) {
	r := &Registry{}
	r.ObserveRequest(200, 100)
	r.ObserveRequest(500, 300)
	r.ObserveRequest(404, 50)
	r.ObserveTurn(true, 10000, 1000, 500, 1500)
	r.ObserveTurn(false, 20000, 2000, 1000, 2500)
	r.ObserveVerifyFailure()
	r.ObserveExport(false)
	r.ObserveExport(true)

	s := r.Snapshot()
	if s.RequestsTotal != 3 || s.RequestErrors != 1 {
		t.Errorf("requests: %+v", s)
	}
	if s.AITurns != 2 || s.AITurnErrors != 1 {
		t.Errorf("turns: %+v", s)
	}
	if s.AIInputTokens != 3000 || s.AIOutputTokens != 1500 || s.AICostMicro != 4000 {
		t.Errorf("tokens/cost: %+v", s)
	}
	if s.VerifyFailures != 1 || s.ExportsStarted != 2 || s.ExportsFailed != 1 {
		t.Errorf("misc: %+v", s)
	}

	var b strings.Builder
	r.WritePrometheus(&b)
	out := b.String()
	for _, want := range []string{
		"talon_http_requests_total 3",
		"talon_http_errors_total 1",
		"talon_ai_turns_total 2",
		"talon_ai_cost_usd_total 0.0040",
		"talon_verify_failures_total 1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

// TestAgentMetrics is W13.3: the six agent-quality numbers — rounds-to-
// green, tool error rate, guardrail reject rate, spill/prune incidence,
// context-pressure trajectory, cost per verified build — counted at
// their real call sites' semantics and rendered.
func TestAgentMetrics(t *testing.T) {
	r := &Registry{}

	// Context-pressure trajectory: last follows each turn, max holds the peak.
	r.ObserveTurn(true, 5, 900, 100, 0)
	r.ObserveTurn(true, 5, 1500, 100, 0)
	r.ObserveTurn(true, 5, 700, 100, 0)

	// Rounds-to-green + cost per verified build: only BUILD-green turns
	// feed the green sums; a long red turn still counts its rounds.
	r.ObserveAgentTurn(4, true, 2_000_000) // green, $2
	r.ObserveAgentTurn(7, false, 3_000_000)
	r.ObserveAgentTurn(2, true, 1_000_000) // green, $1

	// Tool error rate over three calls, two failed; one of those failures
	// was a guardrail refusal, also broken out separately.
	r.ObserveToolCall(false)
	r.ObserveToolCall(true)
	r.ObserveToolCall(true)
	r.ObserveGuardrailReject()

	// Context economy incidence.
	r.ObserveSpill()
	r.ObservePrune()
	r.ObservePrune()

	s := r.Snapshot()
	if s.AgentRounds != 13 || s.AgentTurnsGreen != 2 || s.AgentRoundsGreen != 6 {
		t.Errorf("rounds-to-green inputs: %+v", s)
	}
	if s.AgentCostGreenMicro != 3_000_000 {
		t.Errorf("green cost: got %d want 3000000", s.AgentCostGreenMicro)
	}
	if s.ToolCalls != 3 || s.ToolErrors != 2 || s.GuardrailRejects != 1 {
		t.Errorf("tool/guardrail: %+v", s)
	}
	if s.ContextSpills != 1 || s.ContextPrunes != 2 {
		t.Errorf("context economy: spills=%d prunes=%d", s.ContextSpills, s.ContextPrunes)
	}
	if s.ContextPressureLast != 700 || s.ContextPressureMax != 1500 {
		t.Errorf("pressure trajectory: last=%d max=%d", s.ContextPressureLast, s.ContextPressureMax)
	}

	var b strings.Builder
	r.WritePrometheus(&b)
	out := b.String()
	for _, want := range []string{
		"talon_agent_rounds_total 13",
		"talon_agent_turns_green_total 2",
		"talon_agent_rounds_to_green_avg 3.0", // 6 / 2
		"talon_agent_tool_calls_total 3",
		"talon_agent_tool_errors_total 2",
		"talon_agent_tool_error_rate 0.6667", // 2 / 3
		"talon_agent_guardrail_rejects_total 1",
		"talon_agent_guardrail_reject_rate 0.3333", // 1 / 3
		"talon_context_spills_total 1",
		"talon_context_prunes_total 2",
		"talon_context_pressure_last_tokens 700",
		"talon_context_pressure_max_tokens 1500",
		"talon_agent_cost_green_usd_total 3.0000",
		"talon_agent_cost_per_verified_build_usd 1.5000", // $3 / 2
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

// TestAgentMetricsZeroDenominator: derived rates stay at zero instead of
// dividing when nothing has been observed yet.
func TestAgentMetricsZeroDenominator(t *testing.T) {
	r := &Registry{}
	var b strings.Builder
	r.WritePrometheus(&b)
	out := b.String()
	for _, want := range []string{
		"talon_agent_rounds_to_green_avg 0.0",
		"talon_agent_tool_error_rate 0.0000",
		"talon_agent_guardrail_reject_rate 0.0000",
		"talon_agent_cost_per_verified_build_usd 0.0000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}
