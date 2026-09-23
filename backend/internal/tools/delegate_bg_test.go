package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
)

// W12.3 / gate clause 4, at the tool surface: delegate defaults to
// background (acknowledge now), and list_children shows the running → done
// transition with the durable report collected through the tool.
func TestDelegateDefaultsToBackgroundAndListCollectsReport(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	runner := &bgRunnerTool{release: release, done: done}
	svc := subagent.NewService(runner, 2)
	d := &Delegate{Service: svc, LabelPrefix: "project-1"}
	lc := &ListChildren{Service: svc}

	// Default spawn: background — no report inline, acknowledgement instead.
	res, err := d.Execute(context.Background(), json.RawMessage(
		`{"task":"survey the repo","label":"survey"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("background spawn refused: %s", res.Content)
	}
	if strings.Contains(res.Content, "BEGIN CHILD REPORT") {
		t.Errorf("background delegate must not return the report inline: %s", res.Content)
	}
	if !strings.Contains(res.Content, "background") || !strings.Contains(res.Content, "list_children") {
		t.Errorf("acknowledgement must teach collection: %s", res.Content)
	}

	// Running state visible immediately through the tool.
	out, err := lc.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Content, "survey") || !strings.Contains(out.Content, "running") {
		t.Errorf("running state must be visible in list_children: %s", out.Content)
	}

	// Release the child; poll the tool until done + report collected.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, err = lc.Execute(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Content, "done") && strings.Contains(out.Content, "BEGIN CHILD REPORT") &&
			strings.Contains(out.Content, "survey report") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("report never became durable through list_children: %s", out.Content)
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-done
}

type bgRunnerTool struct {
	release chan struct{}
	done    chan struct{}
}

func (r *bgRunnerTool) RunChild(ctx context.Context, _ subagent.Spec) subagent.Result {
	<-r.release
	close(r.done)
	return subagent.Result{FinalText: "survey report", Rounds: 2}
}

// background:false keeps the old blocking contract — the framed report
// arrives inline in the tool result.
func TestDelegateSyncWhenBackgroundFalse(t *testing.T) {
	svc := subagent.NewService(&syncRunner{}, 2)
	d := &Delegate{Service: svc}
	res, err := d.Execute(context.Background(), json.RawMessage(
		`{"task":"do the thing","background":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("sync delegate failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "BEGIN CHILD REPORT") ||
		!strings.Contains(res.Content, "inline result") {
		t.Errorf("sync path must return the framed report: %s", res.Content)
	}
}

type syncRunner struct{}

func (r *syncRunner) RunChild(context.Context, subagent.Spec) subagent.Result {
	return subagent.Result{FinalText: "inline result", Rounds: 1}
}

// The mutating declaration rides the spec to the service seam: under the
// readonly posture the refusal surfaces as an is_error naming the flag.
func TestDelegateMutatingRefusalSurfaces(t *testing.T) {
	svc := subagent.NewService(&syncRunner{}, 2) // readonly posture (default)
	d := &Delegate{Service: svc}
	res, err := d.Execute(context.Background(), json.RawMessage(
		`{"task":"write files","mutating":true,"background":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("mutating spawn must refuse under readonly posture: %s", res.Content)
	}
	if !strings.Contains(res.Content, "DELEGATION_ISOLATION") ||
		!strings.Contains(res.Content, "TALONSTUDIO_CHILD_ISOLATION=worktree") {
		t.Errorf("refusal must name the posture and the escape hatch: %s", res.Content)
	}
}
