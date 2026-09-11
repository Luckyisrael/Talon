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
