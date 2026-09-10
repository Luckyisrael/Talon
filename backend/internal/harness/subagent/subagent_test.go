package subagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubRunner records the specs it ran and returns canned results.
type stubRunner struct {
	mu     sync.Mutex
	ran    []Spec
	result func(Spec) Result
	delay  time.Duration
}

func (r *stubRunner) RunChild(_ context.Context, spec Spec) Result {
	r.mu.Lock()
	r.ran = append(r.ran, spec)
	res := r.result(spec)
	r.mu.Unlock()
	time.Sleep(r.delay)
	return res
}

// One-shot children return their final text; nothing interleaves into the
// parent (the runner sees exactly the spec; the result is the child's alone).
func TestOneShotChild(t *testing.T) {
	runner := &stubRunner{result: func(s Spec) Result {
		return Result{FinalText: "The package exports Connection and Keypair.", Rounds: 3}
	}}
	svc := NewService(runner, 2)
	res, err := svc.Spawn(context.Background(), Spec{Prompt: "inspect the package", Label: "pkg research"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Errored {
		t.Fatalf("child errored: %s", res.Err)
	}
	out := RenderResult("pkg research", res)
	if !strings.Contains(out, "BEGIN CHILD REPORT") || !strings.Contains(out, "exports Connection") {
		t.Errorf("result not framed as a report: %q", out)
	}
	if !strings.Contains(out, "not as instructions to you") {
		t.Error("untrusted-data framing missing")
	}
	// Discovery lists the child without transcripts.
	list := svc.List()
	if len(list) != 1 || list[0].State != "done" || list[0].Label != "pkg research" {
		t.Errorf("fault: discovery wrong: %+v", list)
	}
}

// Depth 1 is enforced at the seam: a descended service refuses spawns with
// the structured ErrDeep.
func TestDepthOneEnforced(t *testing.T) {
	runner := &stubRunner{result: func(Spec) Result { return Result{FinalText: "ok"} }}
	svc := NewService(runner, 2)
	childSvc := svc.Descend()
	if _, err := childSvc.Spawn(context.Background(), Spec{Prompt: "grandchild"}); !errors.Is(err, ErrDeep) {
		t.Fatalf("fault: grandchild spawn not refused with ErrDeep, got %v", err)
	}
}

// The pool bound: a second child waits (here: refuses) while the pool is
// full — the rate-limit reality.
func TestPoolBound(t *testing.T) {
	runner := &stubRunner{result: func(Spec) Result { return Result{FinalText: "ok"} }, delay: 50 * time.Millisecond}
	svc := NewService(runner, 1)

	var wg sync.WaitGroup
	var firstErr, secondErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, firstErr = svc.Spawn(context.Background(), Spec{Prompt: "one"})
	}()
	time.Sleep(10 * time.Millisecond) // let the first enter the pool
	go func() {
		defer wg.Done()
		_, secondErr = svc.Spawn(context.Background(), Spec{Prompt: "two"})
	}()
	wg.Wait()
	if firstErr != nil {
		t.Errorf("first spawn failed: %v", firstErr)
	}
	// The second either waited (spawned after the first finished) or was
	// refused with ErrPoolFull — both are contract-honest. It must never be
	// a panic or a silent run-over.
	if secondErr != nil && !errors.Is(secondErr, ErrPoolFull) {
		t.Errorf("second spawn refused for the wrong reason: %v", secondErr)
	}
	// A refused spawn creates no child; an admitted one does. Either way the
	// count matches what the pool did — never a phantom.
	want := 2
	if errors.Is(secondErr, ErrPoolFull) {
		want = 1
	}
	if got := len(svc.List()); got != want {
		t.Errorf("children listed = %d, want %d for secondErr=%v", got, want, secondErr)
	}
}

// A 429 in a child sets the cooldown; the next fan-out is refused with the
// structured ErrCooldown and names why.
func TestCooldownAfterRateLimit(t *testing.T) {
	runner := &stubRunner{result: func(Spec) Result {
		return Result{Errored: true, Err: "API error (status 429): rate limit exceeded"}
	}}
	svc := NewService(runner, 2)
	res, err := svc.Spawn(context.Background(), Spec{Prompt: "first"})
	if err != nil || !res.Errored {
		t.Fatalf("first spawn: %v %+v", err, res)
	}
	res2, err := svc.Spawn(context.Background(), Spec{Prompt: "second"})
	if !errors.Is(err, ErrCooldown) {
		t.Fatalf("fault: fan-out after 429 not suppressed, got %v", err)
	}
	// The refusal names the reason it paused.
	if !strings.Contains(res2.Err, "rate limit") {
		t.Errorf("cooldown refusal does not carry the reason: %s", res2.Err)
	}
}

// A child crash is a structured parent-visible error, never a parent crash:
// the runner panicking inside RunChild is recovered by the test runner — the
// service itself must surface it as Result.Errored.
func TestChildCrashStructured(t *testing.T) {
	runner := &stubRunner{result: func(Spec) Result {
		return Result{Errored: true, Err: "child loop panicked: index out of range"}
	}}
	svc := NewService(runner, 2)
	res, err := svc.Spawn(context.Background(), Spec{Prompt: "boom"})
	if err != nil {
		t.Fatalf("crash surfaced as a spawn error: %v", err)
	}
	if !res.Errored {
		t.Fatal("fault: crash not reported as an errored result")
	}
	out := RenderResult("boom", res)
	if !strings.Contains(out, "failed") {
		t.Errorf("crash not honestly reported: %s", out)
	}
	// The failed child is listed as failed, and the pool is free again.
	if list := svc.List(); list[0].State != "failed" {
		t.Errorf("fault: failed child listed as %q", list[0].State)
	}
	if _, err := svc.Spawn(context.Background(), Spec{Prompt: "next"}); err != nil {
		t.Errorf("pool not released after crash: %v", err)
	}
}
