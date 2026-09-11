// Package metrics holds process-lifetime operational counters and renders
// them in Prometheus text format. It answers "is the system healthy right
// now" — request volume, error rate, latency, AI turns, token and dollar
// spend. Point-in-time gauges (sessions, containers, total spend) are read
// from the store and the container manager at scrape time by the handler,
// not kept here, so they can never disagree with the source of truth.
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
// the error-rate signal for the product's core loop.
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
}
