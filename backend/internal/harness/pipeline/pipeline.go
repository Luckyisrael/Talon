// Package pipeline is the H3 tool-call pipeline: the ordered stages every
// tool call passes through, with bounded parallelism. Today the orchestrator's
// executeTools does policy (guardrails), dispatch, and observation inline,
// with an all-or-nothing parallel rule (every call read-only → unbounded
// goroutines; any writer → fully sequential). This package makes the stages a
// contract and the pool bounded: read-only calls overlap up to a ceiling,
// exclusive calls are ordering barriers that run alone, and observation is a
// listener seam so the orchestrator's tracking notes move there without the
// dispatch path knowing about them.
//
// The pool bound defaults to 4: the provider's shared rate limit (~8
// requests/min) means more concurrent tool traffic than that spends itself on
// retries, not throughput. Operators tune it with SEEKERSTUDIO_TOOL_POOL.
package pipeline

import (
	"context"
	"os"
	"strconv"
	"sync"
)

// Call is one tool invocation entering the pipeline.
type Call struct {
	// ID is the provider's tool_use id — results pair back to it.
	ID string
	// Name is the tool.
	Name string
	// Input is the raw arguments JSON.
	Input []byte
}

// Result is one tool invocation's outcome leaving the pipeline.
type Result struct {
	// ID pairs the result to its call.
	ID string
	// Content is the tool output the model reads.
	Content string
	// IsError marks a failed call.
	IsError bool
}

// Policy decides whether a call may run. Returning Allowed=false refuses the
// call before dispatch — it never executes, and the pipeline synthesizes the
// failed result carrying Rule and Message so the model can correct.
type Policy interface {
	// Check answers allow/refuse for one call.
	Check(call Call) Decision
}

// Decision is a policy verdict.
type Decision struct {
	Allowed bool
	Rule    string
	Message string
}

// Dispatcher runs one allowed call. It is the seam the tool registry fills.
type Dispatcher interface {
	// Dispatch executes the call and returns its output.
	Dispatch(ctx context.Context, call Call) (content string, isError bool)
}

// Classifier answers whether a call may overlap others (read-only) or is an
// ordering barrier (exclusive). The tool registry fills it.
type Classifier interface {
	// ReadOnly reports whether the tool can run concurrently with others in
	// the same batch. Everything else runs alone, in order.
	ReadOnly(name string) bool
}

// Observer sees every call's lifecycle: refused, started, finished. The
// orchestrator's emitter and result-note tracking move behind this seam.
type Observer interface {
	// OnRefused fires when the policy refuses a call.
	OnRefused(call Call, d Decision)
	// OnStart fires when a call begins executing.
	OnStart(call Call)
	// OnFinish fires when a call ends, error or not.
	OnFinish(call Call, result Result)
}

// PolicyFunc adapts a function to Policy. A nil PolicyFunc is not a no-op —
// construct New with nil to allow everything; a non-nil func must answer.
type PolicyFunc func(Call) Decision

func (f PolicyFunc) Check(c Call) Decision { return f(c) }

// ClassifierFunc adapts a function to Classifier.
type ClassifierFunc func(name string) bool

func (f ClassifierFunc) ReadOnly(name string) bool { return f(name) }

// DispatcherFunc adapts a function to Dispatcher.
type DispatcherFunc func(context.Context, Call) (string, bool)

func (f DispatcherFunc) Dispatch(ctx context.Context, c Call) (string, bool) { return f(ctx, c) }

// ObserverFunc adapts three functions to Observer; any may be nil.
type ObserverFunc struct {
	OnRefusedFn func(Call, Decision)
	OnStartFn   func(Call)
	OnFinishFn  func(Call, Result)
}

func (o ObserverFunc) OnRefused(c Call, d Decision) {
	if o.OnRefusedFn != nil {
		o.OnRefusedFn(c, d)
	}
}
func (o ObserverFunc) OnStart(c Call) {
	if o.OnStartFn != nil {
		o.OnStartFn(c)
	}
}
func (o ObserverFunc) OnFinish(c Call, r Result) {
	if o.OnFinishFn != nil {
		o.OnFinishFn(c, r)
	}
}

// Pipeline is the configured stages. Construct with New; zero extra stages
// still run policy → dispatch in order, sequential.
type Pipeline struct {
	policy     Policy
	dispatcher Dispatcher
	classify   Classifier
	observe    Observer
	pool       int
}

// New builds a pipeline over the required stages. Nil optional stages are
// no-ops; a nil policy allows everything (the caller composes real policy).
func New(policy Policy, dispatcher Dispatcher, classify Classifier, observe Observer) *Pipeline {
	return &Pipeline{
		policy:     policy,
		dispatcher: dispatcher,
		classify:   classify,
		observe:    observe,
		pool:       poolFromEnv(),
	}
}

// defaultPool is the concurrent read-only call ceiling.
const defaultPool = 4

// PoolOverride sets the concurrent-dispatch bound explicitly, clamped to
// [1, 32] like the env path. Composition and tests use it; deployments use
// SEEKERSTUDIO_TOOL_POOL.
func (p *Pipeline) PoolOverride(n int) {
	if n < 1 {
		n = 1
	}
	if n > 32 {
		n = 32
	}
	p.pool = n
}

// poolFromEnv reads SEEKERSTUDIO_TOOL_POOL, clamped to [1, 32]: a bound below
// 1 would serialize the pool name does not deserve, and one above 32 spends
// the provider's shared rate limit on retry storms rather than throughput.
func poolFromEnv() int {
	v := os.Getenv("SEEKERSTUDIO_TOOL_POOL")
	if v == "" {
		return defaultPool
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultPool
	}
	if n > 32 {
		return 32
	}
	return n
}

// Run sends one batch of calls through the stages and returns the results in
// the batch's order — the provider requires tool_results in the order the
// calls were sent, so the pool never reorders output even when it overlaps
// execution.
//
// Scheduling: a maximal run of read-only calls overlaps up to the pool bound;
// the first exclusive call drains the pool and runs alone; the calls after it
// schedule fresh. Refused calls never dispatch — their result is synthesized
// with the refusal, in place, in order.
func (p *Pipeline) Run(ctx context.Context, calls []Call) []Result {
	results := make([]Result, len(calls))

	if p.classify == nil {
		// No classifier means nothing may overlap — the conservative default.
		for i, c := range calls {
			results[i] = p.runOne(ctx, c)
		}
		return results
	}

	// i scans the batch; j closes out the current parallel run.
	i := 0
	var wg sync.WaitGroup
	for i < len(calls) {
		if p.classify.ReadOnly(calls[i].Name) {
			// Collect the maximal read-only run [i, j).
			j := i
			for j < len(calls) && p.classify.ReadOnly(calls[j].Name) {
				j++
			}
			// Overlap the run up to the pool bound: a semaphore of size
			// pool, acquired before dispatch and released after the result
			// lands. Extra calls queue on the semaphore — bounded rolling,
			// not one goroutine per call.
			sem := make(chan struct{}, p.pool)
			for k := i; k < j; k++ {
				wg.Add(1)
				go func(k int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					results[k] = p.runOne(ctx, calls[k])
				}(k)
			}
			i = j
			continue
		}
		// Exclusive call: drain the in-flight read-only run first so nothing
		// overlaps it, then run it alone.
		wg.Wait()
		results[i] = p.runOne(ctx, calls[i])
		i++
	}
	wg.Wait()
	return results
}

// runOne sends one call through policy → dispatch, with observation at each
// transition.
func (p *Pipeline) runOne(ctx context.Context, call Call) Result {
	if p.policy != nil {
		if d := p.policy.Check(call); !d.Allowed {
			if p.observe != nil {
				p.observe.OnRefused(call, d)
			}
			return Result{
				ID:      call.ID,
				Content: "Blocked by a project guardrail (" + d.Rule + "): " + d.Message,
				IsError: true,
			}
		}
	}
	if p.observe != nil {
		p.observe.OnStart(call)
	}
	content, isError := p.dispatcher.Dispatch(ctx, call)
	if p.observe != nil {
		p.observe.OnFinish(call, Result{ID: call.ID, Content: content, IsError: isError})
	}
	return Result{ID: call.ID, Content: content, IsError: isError}
}
