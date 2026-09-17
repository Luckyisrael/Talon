package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
)

// childRunner is the H9 in-process subagent backend: a child runs a second
// orchestrator loop over a scoped tool registry on the same provider client.
// Nothing interlaces into the parent transcript — the child's outcome is
// its Result alone, delivered through the delegate tool.
type childRunner struct {
	// build assembles one buildSession for the workspace (scoped registry).
	build func() *buildSession
	// newSession creates the child's session state.
	newSession func() *orchestrator.SessionState
	// systemPrompt is the child's base prompt.
	systemPrompt string
	// forkHistory, when non-nil, seeds a fork child with the parent prefix.
	forkHistory []ai.Message
	// parentTools names the tools the parent holds — the child's set is
	// validated against it (a child cites no tools the parent lacks).
	parentTools map[string]bool
	// orchestrator runs the child's loop.
	orchestrator *orchestrator.Orchestrator
	// eventLog, when set, is where the child's recovered model-call retries
	// land — a delegated turn that hit a rate limit should show in the same
	// project log as the parent's retries. Nil keeps the child SSE-only.
	eventLog orchestrator.AuditLog
}

// RunChild executes one child to completion. The loop is the same
// RunAgentLoop the parent uses — the scope is the difference: tools the
// child lacks are simply not in its registry, and delegate itself is never
// registered (depth 1 is enforced at the service seam; the tool not being
// present is the second wall).
func (r *childRunner) RunChild(ctx context.Context, spec subagent.Spec) subagent.Result {
	if strings.TrimSpace(spec.Prompt) == "" && !spec.Fork {
		return subagent.Result{Errored: true, Err: "child task prompt is empty"}
	}

	bs := r.build()
	if bs == nil {
		return subagent.Result{Errored: true, Err: "child could not be assembled in this session"}
	}
	sess := r.newSession()
	if sess == nil {
		return subagent.Result{Errored: true, Err: "child session state could not be created"}
	}
	defer sess.Emitter.Close()

	// Validate the child's tool set against the parent's: the child may hold
	// a subset, never a superset.
	var defs []ai.ToolDefinition
	for _, d := range bs.defs {
		if r.parentTools != nil && !r.parentTools[d.Name] {
			continue
		}
		defs = append(defs, d)
	}

	history := make([]ai.Message, 0, 2)
	if spec.Fork && len(r.forkHistory) > 0 {
		history = append(history, r.forkHistory...)
	}
	history = append(history, ai.UserText(spec.Prompt))

	system := spec.SystemPrompt
	if system == "" {
		system = r.systemPrompt
	}

	outcome, err := r.runLoop(ctx, sess, bs, defs, history, system)
	if err != nil {
		return subagent.Result{Errored: true, Err: fmt.Sprintf("child loop failed: %v", err)}
	}
	if outcome == nil {
		return subagent.Result{Errored: true, Err: "child produced no outcome"}
	}
	if outcome.Stalled {
		return subagent.Result{Errored: true, Err: "child stalled: repeated full responses with no tool calls"}
	}
	final := strings.TrimSpace(outcome.FinalText)
	if final == "" {
		final = fmt.Sprintf("The delegated task ended without a final report (state: %s).", outcomeLabel(outcome))
	}
	return subagent.Result{FinalText: final, Rounds: outcome.Rounds}
}

// runLoop is the seam tests override; the production runner calls the
// orchestrator's loop directly.
func (r *childRunner) runLoop(ctx context.Context, sess *orchestrator.SessionState, bs *buildSession,
	defs []ai.ToolDefinition, history []ai.Message, system string) (*orchestrator.BuildOutcome, error) {
	return r.orchestrator.RunAgentLoop(ctx, sess, orchestrator.RunOptions{
		SystemPrompt: system,
		Mode:         "build",
		History:      history,
		Tools:        defs,
		Execute:      bs.executor(),
		ReadOnlyTool: bs.reg.IsReadOnly,
		FilesTouched: bs.fs.Touched,
		Exists:       bs.fs.Exists,
		// A subagent's retries belong to the same project log: the parent
		// turn's replay should see that a delegated call recovered too.
		EventLog: r.eventLog,
	})
}

func outcomeLabel(o *orchestrator.BuildOutcome) string {
	switch {
	case o.Verified:
		return "verified"
	case o.Paused:
		return "paused"
	case o.Exhausted:
		return "exhausted"
	}
	return "done"
}
