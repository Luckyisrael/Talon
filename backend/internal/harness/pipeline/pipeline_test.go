package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testStages wires a pipeline with recording stages.
type testStages struct {
	mu         sync.Mutex
	started    map[string]int
	finished   map[string]int
	refused    map[string]int
	dispatched map[string]int
	// concurrent tracks live dispatches; maxConcurrent records the peak.
	concurrent    int
	maxConcurrent int
	// dispatchDelay, when set, sleeps in Dispatch to expose overlap.
	dispatchDelay time.Duration
}

func newTestStages() *testStages {
	return &testStages{
		started:    map[string]int{},
		finished:   map[string]int{},
		refused:    map[string]int{},
		dispatched: map[string]int{},
	}
}

func (ts *testStages) Policy() Policy {
	return policyFunc(func(c Call) Decision {
		if c.Name == "forbidden" {
			return Decision{Allowed: false, Rule: "test", Message: "not allowed here"}
		}
		return Decision{Allowed: true}
	})
}

func (ts *testStages) Classifier() Classifier {
	return classifierFunc(func(name string) bool {
		return name == "read_file" || name == "grep"
	})
}

func (ts *testStages) Observer() Observer {
	return observerFunc{
		onRefused: func(c Call, d Decision) {
			ts.mu.Lock()
			defer ts.mu.Unlock()
			ts.refused[c.Name]++
		},
		onStart: func(c Call) {
			ts.mu.Lock()
			defer ts.mu.Unlock()
			ts.started[c.Name]++
		},
		onFinish: func(c Call, r Result) {
			ts.mu.Lock()
			defer ts.mu.Unlock()
			ts.finished[c.Name]++
		},
	}
}

func (ts *testStages) Dispatcher() Dispatcher {
	return dispatcherFunc(func(ctx context.Context, c Call) (string, bool) {
		ts.mu.Lock()
		ts.dispatched[c.Name]++
		ts.concurrent++
		if ts.concurrent > ts.maxConcurrent {
			ts.maxConcurrent = ts.concurrent
		}
		ts.mu.Unlock()
		if ts.dispatchDelay > 0 {
			time.Sleep(ts.dispatchDelay)
		}
		ts.mu.Lock()
		ts.concurrent--
		ts.mu.Unlock()
		return fmt.Sprintf("ran %s", c.Name), false
	})
}

// Adapters turning functions into the stage interfaces.
type policyFunc func(Call) Decision

func (f policyFunc) Check(c Call) Decision { return f(c) }

type classifierFunc func(string) bool

func (f classifierFunc) ReadOnly(name string) bool { return f(name) }

type dispatcherFunc func(context.Context, Call) (string, bool)

func (f dispatcherFunc) Dispatch(ctx context.Context, c Call) (string, bool) {
	return f(ctx, c)
}

type observerFunc struct {
	onRefused func(Call, Decision)
	onStart   func(Call)
	onFinish  func(Call, Result)
}

func (o observerFunc) OnRefused(c Call, d Decision) { o.onRefused(c, d) }
func (o observerFunc) OnStart(c Call)              { o.onStart(c) }
func (o observerFunc) OnFinish(c Call, r Result)   { o.onFinish(c, r) }

func roCall(id, name string) Call { return Call{ID: id, Name: name, Input: []byte("{}")} }

// The pool bound is real: 20 read-only calls with a dispatch delay overlap at
// most pool at once. Unbounded goroutines (the old behavior) would peak at 20.
func TestPoolBoundHolds(t *testing.T) {
	ts := newTestStages()
	ts.dispatchDelay = 5 * time.Millisecond
	p := New(ts.Policy(), ts.Dispatcher(), ts.Classifier(), ts.Observer())
	p.pool = 4

	calls := make([]Call, 20)
	for i := range calls {
		calls[i] = roCall(fmt.Sprintf("c%d", i), "read_file")
	}
	results := p.Run(context.Background(), calls)

	if len(results) != 20 {
		t.Fatalf("got %d results, want 20", len(results))
	}
	if ts.maxConcurrent > 4 {
		t.Errorf("pool bound violated: %d concurrent dispatches (bound 4)", ts.maxConcurrent)
	}
	if ts.maxConcurrent < 2 {
		t.Errorf("pool never overlapped (max %d) — the bound made it sequential", ts.maxConcurrent)
	}
	for i, r := range results {
		if r.ID != calls[i].ID {
			t.Errorf("result %d out of order: %s", i, r.ID)
		}
	}
}

// An exclusive call is an ordering barrier: read-only calls in flight before
// it must finish before it starts, and nothing overlaps it. The test records
// the barrier's start/end and every read's start/end, then asserts no overlap.
func TestExclusiveCallIsBarrier(t *testing.T) {
	ts := newTestStages()
	ts.dispatchDelay = 5 * time.Millisecond

	var mu sync.Mutex
	type span struct{ start, end int64 }
	spans := map[string]span{}
	var clock atomic.Int64

	tick := func() int64 { return clock.Add(1) }

	p := New(ts.Policy(), dispatcherFunc(func(ctx context.Context, c Call) (string, bool) {
		mu.Lock()
		spans[c.ID] = span{start: tick(), end: 0}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		s := spans[c.ID]
		s.end = tick()
		spans[c.ID] = s
		mu.Unlock()
		return "ok", false
	}), ts.Classifier(), nil)

	calls := []Call{
		roCall("r1", "read_file"),
		roCall("r2", "read_file"),
		roCall("w1", "write_file"), // exclusive: the barrier
		roCall("r3", "read_file"),  // after the barrier, may overlap r4
		roCall("r4", "read_file"),
	}
	p.Run(context.Background(), calls)

	mu.Lock()
	defer mu.Unlock()
	barrier := spans["w1"]
	for _, id := range []string{"r1", "r2"} {
		if s := spans[id]; s.end >= barrier.start {
			t.Errorf("read %s overlapped the exclusive barrier (end %d >= start %d)", id, s.end, barrier.start)
		}
	}
	for _, id := range []string{"r3", "r4"} {
		if s := spans[id]; s.start <= barrier.end {
			t.Errorf("read %s started before the barrier cleared (start %d <= end %d)", id, s.start, s.end)
		}
	}
}

// A refused call never dispatches and returns its refusal in place, in order.
func TestPolicyRefusalInPlace(t *testing.T) {
	ts := newTestStages()
	p := New(ts.Policy(), ts.Dispatcher(), ts.Classifier(), ts.Observer())

	results := p.Run(context.Background(), []Call{
		roCall("a", "read_file"),
		roCall("b", "forbidden"),
		roCall("c", "grep"),
	})

	if !results[1].IsError || results[1].ID != "b" {
		t.Errorf("refusal result malformed: %+v", results[1])
	}
	if results[1].Content == "" {
		t.Error("refusal carries no message")
	}
	if results[0].IsError || results[0].Content == "" {
		t.Error("allowed call before the refusal produced no healthy result")
	}
	if ts.dispatched["forbidden"] != 0 {
		t.Error("refused call dispatched anyway")
	}
	if ts.refused["forbidden"] != 1 {
		t.Error("observer did not see the refusal")
	}
	if ts.started["forbidden"] != 0 {
		t.Error("observer saw a start for a refused call")
	}
}

// No classifier = fully sequential, still correct: the conservative default
// never overlaps anything it cannot classify.
func TestNilClassifierSequential(t *testing.T) {
	ts := newTestStages()
	p := New(ts.Policy(), ts.Dispatcher(), nil, ts.Observer())
	results := p.Run(context.Background(), []Call{
		roCall("a", "read_file"),
		roCall("b", "grep"),
	})
	if len(results) != 2 || results[0].ID != "a" || results[1].ID != "b" {
		t.Fatalf("sequential results wrong: %+v", results)
	}
}

// The env override clamps honestly: 0 and garbage fall back to the default,
// and a giant value clamps at 32.
func TestPoolFromEnvClamps(t *testing.T) {
	t.Setenv("SEEKERSTUDIO_TOOL_POOL", "0")
	if got := poolFromEnv(); got != defaultPool {
		t.Errorf("pool 0 → %d, want default %d", got, defaultPool)
	}
	t.Setenv("SEEKERSTUDIO_TOOL_POOL", "garbage")
	if got := poolFromEnv(); got != defaultPool {
		t.Errorf("pool garbage → %d, want default %d", got, defaultPool)
	}
	t.Setenv("SEEKERSTUDIO_TOOL_POOL", "100")
	if got := poolFromEnv(); got != 32 {
		t.Errorf("pool 100 → %d, want clamp 32", got)
	}
	t.Setenv("SEEKERSTUDIO_TOOL_POOL", "8")
	if got := poolFromEnv(); got != 8 {
		t.Errorf("pool 8 → %d, want 8", got)
	}
}
