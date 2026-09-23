// Package metrics holds process-lifetime operational counters and renders
// them in Prometheus text format. It answers "is the system healthy right
// now" — request volume, error rate, latency, AI turns, token and dollar
// spend — plus, since W13, how the agent behaves: rounds-to-green, tool
// error rate, guardrail rejections, context spill/prune incidence,
// context-pressure trajectory, and cost per verified build. Per-session
// numbers already surface through the turn responses (rounds, verified)
// and the H13 token meter; this registry is the process-wide aggregate
// over the same events. Point-in-time gauges (sessions, containers,
// total spend) are read from the store and the container manager at
// scrape time by the handler, not kept here, so they can never disagree
// with the source of truth.
package metrics

import (
	"fmt"
	"io"
	"sync"
)

// Registry is the process counter set. Methods are safe for concurrent use;
// at this request volume a single mutex beats sharded atomics on clarity.
type Registry struct {
	mu sync.Mutex

	requestsTotal  int64
	requestErrors  int64
	requestLatency int64 // sum of ms, for averages
	aiTurns        int64
	aiTurnErrors   int64
	aiLatency      int64 // sum of ms
	aiInputTokens  int64
	aiOutputTokens int64
	aiCostMicroUSD int64
	verifyFailures int64
	exportsStarted int64
	exportsFailed  int64

	// W13 agent-behavior metrics — the Cursor law pointed at the loop:
	// quality becomes numbers that gate changes.
	agentRounds         int64 // rounds across every observed turn
	agentTurnsGreen     int64 // turns whose BUILD went green (outcome.Verified)
	agentRoundsGreen    int64 // rounds spent by those green builds → rounds-to-green
	agentCostGreenMicro int64 // dollars of green builds → cost per verified build
	toolCalls           int64 // dispatched tool calls
	toolErrors          int64 // tool_results with IsError (refusals included; see ObserveToolCall)
	guardrailRejects    int64 // deterministic guardrail refusals
	contextSpills       int64 // oversized outputs filed to disk (H8)
	contextPrunes       int64 // context shrinks: per-output prune + conversation compaction
	contextPressureLast int64 // input tokens of the most recent turn
	contextPressureMax  int64 // process-peak input tokens — the trajectory's high-water
}

var Default = &Registry{}

// ObserveRequest records one served HTTP request. Status >= 500 counts as an
// error; 4xx is client behavior, not system health.
func (r *Registry) ObserveRequest(status int, latencyMs int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestsTotal++
	r.requestLatency += latencyMs
	if status >= 500 {
		r.requestErrors++
	}
}

// ObserveTurn records one finished agent turn: wall time, token spend, and
// dollar cost. Verified=false means the turn failed or paused unresolved —
// the error-rate signal for the product's core loop. It also samples the
// W13 context-pressure trajectory: the last turn's prompt size, and the
// process peak (scrape the last gauge over time for the curve).
func (r *Registry) ObserveTurn(verified bool, latencyMs, inTokens, outTokens int64, costMicroUSD int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aiTurns++
	r.aiLatency += latencyMs
	r.aiInputTokens += inTokens
	r.aiOutputTokens += outTokens
	r.aiCostMicroUSD += costMicroUSD
	if !verified {
		r.aiTurnErrors++
	}
	r.contextPressureLast = inTokens
	if inTokens > r.contextPressureMax {
		r.contextPressureMax = inTokens
	}
}

// ObserveAgentTurn records one finished turn's shape: the rounds it spent,
// whether the BUILD went green (outcome.Verified — the rounds-to-green
// signal; turn success and build-green are different things), and its
// dollar cost so green builds accumulate cost-per-verified-build.
func (r *Registry) ObserveAgentTurn(rounds int64, buildVerified bool, costMicroUSD int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agentRounds += rounds
	if buildVerified {
		r.agentTurnsGreen++
		r.agentRoundsGreen += rounds
		r.agentCostGreenMicro += costMicroUSD
	}
}

// ObserveToolCall records one dispatched tool call. isError counts every
// failed tool_result — including deliberate refusals, which
// ObserveGuardrailReject breaks out separately — so the tool error rate
// and the guardrail reject rate share a denominator (every call passes
// the guard first) without pretending a block was a crash.
func (r *Registry) ObserveToolCall(isError bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolCalls++
	if isError {
		r.toolErrors++
	}
}

// ObserveGuardrailReject records one deterministic guardrail refusal
// (the guardrails package's rules; project-hook denials stay in the W10
// hook audit, a separate system).
func (r *Registry) ObserveGuardrailReject() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.guardrailRejects++
}

// ObserveSpill records an oversized tool output filed to disk (H8).
func (r *Registry) ObserveSpill() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contextSpills++
}

// ObservePrune records one context shrink: a large tool output pruned to
// head+tail, or the conversation compacted at the context boundary.
func (r *Registry) ObservePrune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.contextPrunes++
}

// ObserveVerifyFailure records a turn that exhausted verification repair.
func (r *Registry) ObserveVerifyFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.verifyFailures++
}

// ObserveExport records an export job terminal state.
func (r *Registry) ObserveExport(failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exportsStarted++
	if failed {
		r.exportsFailed++
	}
}

// Snapshot is a consistent copy for rendering and tests.
type Snapshot struct {
	RequestsTotal, RequestErrors, RequestLatency int64
	AITurns, AITurnErrors, AILatency             int64
	AIInputTokens, AIOutputTokens, AICostMicro   int64
	VerifyFailures                               int64
	ExportsStarted, ExportsFailed                int64

	AgentRounds, AgentTurnsGreen, AgentRoundsGreen, AgentCostGreenMicro int64
	ToolCalls, ToolErrors, GuardrailRejects                             int64
	ContextSpills, ContextPrunes                                        int64
	ContextPressureLast, ContextPressureMax                             int64
}

func (r *Registry) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Snapshot{
		RequestsTotal: r.requestsTotal, RequestErrors: r.requestErrors, RequestLatency: r.requestLatency,
		AITurns: r.aiTurns, AITurnErrors: r.aiTurnErrors, AILatency: r.aiLatency,
		AIInputTokens: r.aiInputTokens, AIOutputTokens: r.aiOutputTokens, AICostMicro: r.aiCostMicroUSD,
		VerifyFailures: r.verifyFailures,
		ExportsStarted: r.exportsStarted, ExportsFailed: r.exportsFailed,
		AgentRounds: r.agentRounds, AgentTurnsGreen: r.agentTurnsGreen,
		AgentRoundsGreen: r.agentRoundsGreen, AgentCostGreenMicro: r.agentCostGreenMicro,
		ToolCalls: r.toolCalls, ToolErrors: r.toolErrors, GuardrailRejects: r.guardrailRejects,
		ContextSpills: r.contextSpills, ContextPrunes: r.contextPrunes,
		ContextPressureLast: r.contextPressureLast, ContextPressureMax: r.contextPressureMax,
	}
}

// WritePrometheus renders counters, sums, and derived averages. Gauges for
// sessions, containers, and spend are appended by the handler from live
// sources — never cached here.
func (r *Registry) WritePrometheus(w io.Writer) {
	s := r.Snapshot()
	avgReq := 0.0
	if s.RequestsTotal > 0 {
		avgReq = float64(s.RequestLatency) / float64(s.RequestsTotal)
	}
	avgAI := 0.0
	if s.AITurns > 0 {
		avgAI = float64(s.AILatency) / float64(s.AITurns)
	}
	roundsToGreen := 0.0
	if s.AgentTurnsGreen > 0 {
		roundsToGreen = float64(s.AgentRoundsGreen) / float64(s.AgentTurnsGreen)
	}
	toolErrRate := 0.0
	if s.ToolCalls > 0 {
		toolErrRate = float64(s.ToolErrors) / float64(s.ToolCalls)
	}
	rejectRate := 0.0
	if s.ToolCalls > 0 {
		rejectRate = float64(s.GuardrailRejects) / float64(s.ToolCalls)
	}
	costPerGreen := 0.0
	if s.AgentTurnsGreen > 0 {
		costPerGreen = float64(s.AgentCostGreenMicro) / 1e6 / float64(s.AgentTurnsGreen)
	}
	fmt.Fprintf(w, "# HELP talon_http_requests_total Served HTTP requests.\n")
	fmt.Fprintf(w, "# TYPE talon_http_requests_total counter\n")
	fmt.Fprintf(w, "talon_http_requests_total %d\n", s.RequestsTotal)
	fmt.Fprintf(w, "# HELP talon_http_errors_total Responses with status >= 500.\n")
	fmt.Fprintf(w, "# TYPE talon_http_errors_total counter\n")
	fmt.Fprintf(w, "talon_http_errors_total %d\n", s.RequestErrors)
	fmt.Fprintf(w, "# HELP talon_http_latency_ms_avg Average served-request latency.\n")
	fmt.Fprintf(w, "# TYPE talon_http_latency_ms_avg gauge\n")
	fmt.Fprintf(w, "talon_http_latency_ms_avg %.1f\n", avgReq)
	fmt.Fprintf(w, "# HELP talon_ai_turns_total Finished agent turns.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_turns_total counter\n")
	fmt.Fprintf(w, "talon_ai_turns_total %d\n", s.AITurns)
	fmt.Fprintf(w, "# HELP talon_ai_turn_errors_total Unverified/failed agent turns.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_turn_errors_total counter\n")
	fmt.Fprintf(w, "talon_ai_turn_errors_total %d\n", s.AITurnErrors)
	fmt.Fprintf(w, "# HELP talon_ai_turn_latency_ms_avg Average turn wall time.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_turn_latency_ms_avg gauge\n")
	fmt.Fprintf(w, "talon_ai_turn_latency_ms_avg %.1f\n", avgAI)
	fmt.Fprintf(w, "# HELP talon_ai_input_tokens_total Input tokens across turns.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_input_tokens_total counter\n")
	fmt.Fprintf(w, "talon_ai_input_tokens_total %d\n", s.AIInputTokens)
	fmt.Fprintf(w, "# HELP talon_ai_output_tokens_total Output tokens across turns.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_output_tokens_total counter\n")
	fmt.Fprintf(w, "talon_ai_output_tokens_total %d\n", s.AIOutputTokens)
	fmt.Fprintf(w, "# HELP talon_ai_cost_usd_total Model spend in USD.\n")
	fmt.Fprintf(w, "# TYPE talon_ai_cost_usd_total counter\n")
	fmt.Fprintf(w, "talon_ai_cost_usd_total %.4f\n", float64(s.AICostMicro)/1e6)
	fmt.Fprintf(w, "# HELP talon_verify_failures_total Turns that exhausted repair.\n")
	fmt.Fprintf(w, "# TYPE talon_verify_failures_total counter\n")
	fmt.Fprintf(w, "talon_verify_failures_total %d\n", s.VerifyFailures)
	fmt.Fprintf(w, "# HELP talon_exports_total Export jobs started.\n")
	fmt.Fprintf(w, "# TYPE talon_exports_total counter\n")
	fmt.Fprintf(w, "talon_exports_total %d\n", s.ExportsStarted)
	fmt.Fprintf(w, "# HELP talon_exports_failed_total Export jobs failed.\n")
	fmt.Fprintf(w, "# TYPE talon_exports_failed_total counter\n")
	fmt.Fprintf(w, "talon_exports_failed_total %d\n", s.ExportsFailed)

	// W13 agent-behavior metrics.
	fmt.Fprintf(w, "# HELP talon_agent_rounds_total Rounds spent across observed turns.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_rounds_total counter\n")
	fmt.Fprintf(w, "talon_agent_rounds_total %d\n", s.AgentRounds)
	fmt.Fprintf(w, "# HELP talon_agent_turns_green_total Turns whose build went green.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_turns_green_total counter\n")
	fmt.Fprintf(w, "talon_agent_turns_green_total %d\n", s.AgentTurnsGreen)
	fmt.Fprintf(w, "# HELP talon_agent_rounds_to_green_avg Average rounds per green build.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_rounds_to_green_avg gauge\n")
	fmt.Fprintf(w, "talon_agent_rounds_to_green_avg %.1f\n", roundsToGreen)
	fmt.Fprintf(w, "# HELP talon_agent_tool_calls_total Dispatched tool calls.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_tool_calls_total counter\n")
	fmt.Fprintf(w, "talon_agent_tool_calls_total %d\n", s.ToolCalls)
	fmt.Fprintf(w, "# HELP talon_agent_tool_errors_total Tool results that failed (refusals included).\n")
	fmt.Fprintf(w, "# TYPE talon_agent_tool_errors_total counter\n")
	fmt.Fprintf(w, "talon_agent_tool_errors_total %d\n", s.ToolErrors)
	fmt.Fprintf(w, "# HELP talon_agent_tool_error_rate Failed tool results per dispatched call.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_tool_error_rate gauge\n")
	fmt.Fprintf(w, "talon_agent_tool_error_rate %.4f\n", toolErrRate)
	fmt.Fprintf(w, "# HELP talon_agent_guardrail_rejects_total Deterministic guardrail refusals.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_guardrail_rejects_total counter\n")
	fmt.Fprintf(w, "talon_agent_guardrail_rejects_total %d\n", s.GuardrailRejects)
	fmt.Fprintf(w, "# HELP talon_agent_guardrail_reject_rate Guardrail refusals per dispatched call.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_guardrail_reject_rate gauge\n")
	fmt.Fprintf(w, "talon_agent_guardrail_reject_rate %.4f\n", rejectRate)
	fmt.Fprintf(w, "# HELP talon_context_spills_total Oversized tool outputs filed to disk.\n")
	fmt.Fprintf(w, "# TYPE talon_context_spills_total counter\n")
	fmt.Fprintf(w, "talon_context_spills_total %d\n", s.ContextSpills)
	fmt.Fprintf(w, "# HELP talon_context_prunes_total Context shrinks (output prune or conversation compaction).\n")
	fmt.Fprintf(w, "# TYPE talon_context_prunes_total counter\n")
	fmt.Fprintf(w, "talon_context_prunes_total %d\n", s.ContextPrunes)
	fmt.Fprintf(w, "# HELP talon_context_pressure_last_tokens Input tokens of the most recent turn.\n")
	fmt.Fprintf(w, "# TYPE talon_context_pressure_last_tokens gauge\n")
	fmt.Fprintf(w, "talon_context_pressure_last_tokens %d\n", s.ContextPressureLast)
	fmt.Fprintf(w, "# HELP talon_context_pressure_max_tokens Process-peak input tokens.\n")
	fmt.Fprintf(w, "# TYPE talon_context_pressure_max_tokens gauge\n")
	fmt.Fprintf(w, "talon_context_pressure_max_tokens %d\n", s.ContextPressureMax)
	fmt.Fprintf(w, "# HELP talon_agent_cost_green_usd_total Model spend on green builds in USD.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_cost_green_usd_total counter\n")
	fmt.Fprintf(w, "talon_agent_cost_green_usd_total %.4f\n", float64(s.AgentCostGreenMicro)/1e6)
	fmt.Fprintf(w, "# HELP talon_agent_cost_per_verified_build_usd Average model spend per green build.\n")
	fmt.Fprintf(w, "# TYPE talon_agent_cost_per_verified_build_usd gauge\n")
	fmt.Fprintf(w, "talon_agent_cost_per_verified_build_usd %.4f\n", costPerGreen)
}
