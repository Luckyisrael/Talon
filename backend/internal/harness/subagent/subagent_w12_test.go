package subagent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// w12Runner is a scriptable child backend for the W12 gates.
type w12Runner struct {
	fn    func(ctx context.Context, spec Spec) Result
	calls atomic.Int32
}

func (r *w12Runner) RunChild(ctx context.Context, spec Spec) Result {
	r.calls.Add(1)
	if r.fn != nil {
		return r.fn(ctx, spec)
	}
	return Result{FinalText: "ok", Rounds: 1}
}

// W12.1: a mutating child refuses under the readonly posture — before any
// slot, run, or record — and runs once the worktree posture is selected.
// Non-mutating children are unaffected either way.
func TestMutatingChildRefusedUnderReadonlyPosture(t *testing.T) {
	r := &w12Runner{}
	svc := NewService(r, 2)

	res, err := svc.Spawn(context.Background(), Spec{Prompt: "edit files", Mutating: true})
	if !errors.Is(err, ErrIsolation) {
		t.Fatalf("want ErrIsolation, got %v", err)
	}
	if !res.Errored || !strings.Contains(res.Err, "TALONSTUDIO_CHILD_ISOLATION=worktree") {
		t.Errorf("refusal must name the escape hatch: %+v", res)
	}
	if r.calls.Load() != 0 {
		t.Error("a refused spawn must never run the child")
	}
	if kids := svc.List(); len(kids) != 0 {
		t.Errorf("a refusal reserves no record: %+v", kids)
	}

	// Non-mutating child under the same readonly posture: unaffected.
	if _, err := svc.Spawn(context.Background(), Spec{Prompt: "read"}); err != nil {
		t.Errorf("read-only child must run under readonly posture: %v", err)
	}

	svc.SetWorktreeIsolation(true)
	if _, err := svc.Spawn(context.Background(), Spec{Prompt: "edit files", Mutating: true}); err != nil {
		t.Errorf("worktree posture must allow mutating children: %v", err)
	}
	if r.calls.Load() != 2 {
		t.Errorf("runs = %d, want 2", r.calls.Load())
	}
}

// Law 10 (spend): children accumulate CostUSD against the turn's cap; the
// next spawn refuses with DELEGATION_SPEND and the completed children's
// reports stay durable on their records — the partial fan-out survives.
func TestSpendCapStopsRunawayFanout(t *testing.T) {
	r := &w12Runner{fn: func(context.Context, Spec) Result {
		return Result{FinalText: "partial work", Rounds: 2, CostUSD: 0.30}
	}}
	svc := NewService(r, 2)
	svc.SetSpendCap(0.5)

	for i := 0; i < 2; i++ {
		if _, err := svc.Spawn(context.Background(), Spec{Prompt: "work"}); err != nil {
			t.Fatalf("spawn %d: %v", i+1, err)
		}
	}
	if got := svc.Spent(); got < 0.5 {
		t.Fatalf("spent = %v, want >= 0.5 (two $0.30 children)", got)
	}

	res, err := svc.Spawn(context.Background(), Spec{Prompt: "more"})
	if !errors.Is(err, ErrSpendCap) {
		t.Fatalf("want ErrSpendCap, got %v", err)
	}
	if !res.Errored || !strings.Contains(res.Err, "spent $") || !strings.Contains(res.Err, "list_children") {
		t.Errorf("cap refusal must carry the figures and the recovery: %+v", res)
	}
	if r.calls.Load() != 2 {
		t.Errorf("runs = %d, want 2 — the cap must stop the third", r.calls.Load())
	}

	// The partial report: completed children keep their durable records.
	kids := svc.List()
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	for _, k := range kids {
		if k.State != "done" || k.Report != "partial work" || k.Rounds != 2 {
			t.Errorf("durable record lost: %+v", k)
		}
	}

	// 0 = unlimited: the cap setter can stand down.
	svc.SetSpendCap(0)
	if _, err := svc.Spawn(context.Background(), Spec{Prompt: "more"}); err != nil {
		t.Errorf("cap 0 must be unlimited: %v", err)
	}
}

// Wall-clock: a child that overruns its budget is cancelled and reported
// as the structured DELEGATION_WALL refusal, with the reason durable on
// the record.
func TestWallClockBoundsChild(t *testing.T) {
	r := &w12Runner{fn: func(ctx context.Context, _ Spec) Result {
		<-ctx.Done()
		return Result{Errored: true, Err: "child loop stopped: context canceled"}
	}}
	svc := NewService(r, 1)
	svc.SetWall(30 * time.Millisecond)

	start := time.Now()
	res, err := svc.Spawn(context.Background(), Spec{Prompt: "slow"})
	if err != nil {
		t.Fatalf("a wall-clock death is a result, not a Go error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("wall did not bound the child: %s", elapsed)
	}
	if !res.Errored || !strings.Contains(res.Err, "DELEGATION_WALL") {
		t.Errorf("want structured DELEGATION_WALL, got %+v", res)
	}
	if k := svc.List()[0]; k.State != "failed" || !strings.Contains(k.Err, "DELEGATION_WALL") {
		t.Errorf("wall refusal must be durable on the record: %+v", k)
	}
}

// Background children: the reservation is synchronous (structured
// refusals immediate, running state visible at once), the loop is
// detached, and the report lands durable where list_children collects it.
func TestSpawnBackgroundDurableReport(t *testing.T) {
	release := make(chan struct{})
	r := &w12Runner{fn: func(context.Context, Spec) Result {
		<-release
		return Result{FinalText: "background report", Rounds: 3}
	}}
	svc := NewService(r, 1) // pool 1: the reservation is provably held

	id, err := svc.SpawnBackground(Spec{Prompt: "fan out", Label: "bg child"})
	if err != nil || id == "" {
		t.Fatalf("SpawnBackground: id=%q err=%v", id, err)
	}
	kids := svc.List()
	if len(kids) != 1 || kids[0].State != "running" || kids[0].Report != "" {
		t.Fatalf("running state must be visible immediately: %+v", kids)
	}

	// The slot is reserved synchronously — a second spawn hits the pool.
	if _, err := svc.SpawnBackground(Spec{Prompt: "second"}); !errors.Is(err, ErrPoolFull) {
		t.Errorf("background reservation must hold the pool slot, got %v", err)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		k := svc.List()[0]
		if k.State == "done" {
			if k.Report != "background report" || k.Rounds != 3 {
				t.Errorf("durable report lost: %+v", k)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background child never reached done: %+v", k)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
