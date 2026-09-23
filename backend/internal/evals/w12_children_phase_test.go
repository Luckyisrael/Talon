package evals

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/harness/workflow"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// w12FanoutRunner is a scriptable child backend for the W12 fan-out gates:
// fixed per-child cost (the ROUTING_PRICING_JSON-priced production path
// fills the same CostUSD field) and an optional block for background tests.
type w12FanoutRunner struct {
	cost  float64
	block chan struct{} // nil = instant
}

func (r *w12FanoutRunner) RunChild(ctx context.Context, _ subagent.Spec) subagent.Result {
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return subagent.Result{Errored: true, Err: "child cancelled"}
		}
	}
	return subagent.Result{FinalText: "item report", Rounds: 1, CostUSD: r.cost}
}

// W12 eval gate clause 2: the spend cap stops a runaway fan-out with the
// partial report in the parent result. Phase 1's children fill the budget
// ($0.30 × 2 against a $0.50 cap); phase 2 refuses every spawn with the
// structured DELEGATION_SPEND, the run_workflow result keeps both phases
// (what finished, what dropped), and the next spawn still refuses.
func TestPhaseW12SpendCapStopsRunawayFanout(t *testing.T) {
	svc := subagent.NewService(&w12FanoutRunner{cost: 0.30}, 2)
	svc.SetSpendCap(0.5)
	tool := &tools.RunWorkflow{Engine: workflow.NewEngine(svc, nil)}

	spec := `{"name":"runaway","phases":[` +
		`{"title":"checks","parallel":[{"prompt":"check one"},{"prompt":"check two"}]},` +
		`{"title":"more","parallel":[{"prompt":"check three"},{"prompt":"check four"}]}]}`
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"spec":`+spec+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError {
		t.Fatalf("the capped run must still return its partial report: %s", out.Content)
	}

	// The partial report: phase 1 landed, phase 2 dropped under the cap.
	if !strings.Contains(out.Content, `Workflow "runaway" completed: 2 phases`) {
		t.Errorf("parent result lost the run summary:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "checks (2 results, 0 dropped)") {
		t.Errorf("phase-1 partial results missing:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "more (2 results, 2 dropped)") {
		t.Errorf("cap-stopped phase missing from the parent result:\n%s", out.Content)
	}

	// The cap is what stopped it — the accumulator is at/over budget and
	// the next spawn refuses with the structured, figure-carrying refusal.
	if got := svc.Spent(); got < 0.5 {
		t.Fatalf("spent = %v, want >= 0.5", got)
	}
	_, err = svc.Spawn(context.Background(), subagent.Spec{Prompt: "one more"})
	if !errors.Is(err, subagent.ErrSpendCap) {
		t.Fatalf("post-run spawn must hit DELEGATION_SPEND, got %v", err)
	}
	if !strings.Contains(err.Error(), "spent $") {
		t.Errorf("cap refusal must carry the figures: %v", err)
	}
}

// W12 eval gate clause 4: background child status transitions are visible
// in list_children — delegate acknowledges without a report, the running
// state shows at once, and the durable report is collected through the
// tool once the child finishes.
func TestPhaseW12BackgroundStatusVisibleInListChildren(t *testing.T) {
	release := make(chan struct{})
	svc := subagent.NewService(&w12FanoutRunner{block: release}, 2)
	d := &tools.Delegate{Service: svc}
	lc := &tools.ListChildren{Service: svc}

	// Delegate defaults to background: acknowledgement, not a report.
	out, err := d.Execute(context.Background(), json.RawMessage(
		`{"task":"background survey","label":"survey"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out.IsError {
		t.Fatalf("background spawn refused: %s", out.Content)
	}
	if strings.Contains(out.Content, "BEGIN CHILD REPORT") {
		t.Errorf("background delegate must not return the report inline: %s", out.Content)
	}

	// Running state visible immediately through list_children.
	list, err := lc.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.Content, "survey — running") {
		t.Fatalf("running state not visible: %s", list.Content)
	}

	// Release; the done transition and durable report arrive through the
	// same tool.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		list, err = lc.Execute(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(list.Content, "survey — done") &&
			strings.Contains(list.Content, "BEGIN CHILD REPORT") &&
			strings.Contains(list.Content, "item report") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("done transition/report never collected:\n%s", list.Content)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
