package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/harness/subagent"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
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
	// forkHistory resolves the parent prefix a fork child seeds from — at
	// spawn time, so a later fork includes the turns since the last one.
	forkHistory func() []ai.Message
	// parentTools names the tools the parent holds — the child's set is
	// validated against it (a child cites no tools the parent lacks).
	parentTools map[string]bool
	// log and st fork the parent's session log for a fork child: the child's
	// log carries the parent prefix, the lineage event, and the child's own
	// turn. Nil (a test shape without a store) skips the fork — log loss
	// degrades replay fidelity, it never fails a run.
	log *sessionlog.Log
	st  *store.Store
	// runLoop executes the child's loop: childRunnerFor wires the
	// orchestrator call (with the retry AuditLog); tests override it to
	// capture the child's first request.
	runLoop func(ctx context.Context, sess *orchestrator.SessionState, bs *buildSession,
		defs []ai.ToolDefinition, history []ai.Message, system string) (*orchestrator.BuildOutcome, error)
}

// RunChild executes one child to completion. The loop is the same
// RunAgentLoop the parent uses — the scope is the difference: tools the
// child lacks are simply not in its registry, and delegate itself is never
// registered (depth 1 is enforced at the service seam; the tool not being
// present is the second wall).
func (r *childRunner) RunChild(ctx context.Context, spec subagent.Spec) subagent.Result {
	prompt := strings.TrimSpace(spec.Prompt)
	if prompt == "" && !spec.Fork {
		return subagent.Result{Errored: true, Err: "child task prompt is empty"}
	}
	if spec.Fork && prompt == "" {
		prompt = subagent.DefaultForkTask
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

	// W1.5 (H9): a fork child is seeded from the parent's session log — the
	// child's log carries a byte-identical copy of the parent's prefix, both
	// logs record the lineage, and the task joins the child log so a replay
	// of the child key reconstructs the whole delegation. Every log failure
	// logs and continues: log loss degrades replay, never the run.
	var childLog *sessionlog.Log
	if spec.Fork && r.log != nil && r.st != nil {
		childKey := fmt.Sprintf("%s-fork-%d", r.log.Key(), time.Now().UnixNano())
		fromSeq := int64(0)
		if last, ok := r.log.EventAt(int(r.log.Seq()) - 1); ok {
			fromSeq = last.Seq
		}
		cl, err := r.st.SessionLogForked(r.log, childKey, fromSeq)
		if err != nil {
			log.Printf("fork %s: could not seed the child log: %v", childKey, err)
		} else {
			childLog = cl
		}
		lin := events.ForkPayload{ParentKey: r.log.Key(), ChildKey: childKey, FromSeq: fromSeq, Label: spec.Label}
		if _, err := r.log.Append(events.SessionForked, lin); err != nil {
			log.Printf("fork %s: parent lineage append failed: %v", childKey, err)
		}
		if childLog != nil {
			if _, err := childLog.Append(events.SessionForked, lin); err != nil {
				log.Printf("fork %s: child lineage append failed: %v", childKey, err)
			}
			if _, err := childLog.Append(events.UserMessage, events.MessagePayload{
				Role:   "user",
				Blocks: []events.BlockPayload{{Type: "text", Text: prompt}},
			}); err != nil {
				log.Printf("fork %s: task append failed: %v", childKey, err)
			}
		}
	}

	history := make([]ai.Message, 0, 2)
	if spec.Fork && r.forkHistory != nil {
		if prefix := r.forkHistory(); len(prefix) > 0 {
			history = append(history, prefix...)
		}
	}
	history = append(history, ai.UserText(prompt))

	system := spec.SystemPrompt
	if system == "" {
		system = r.systemPrompt
	}

	if r.runLoop == nil {
		return subagent.Result{Errored: true, Err: "child loop runner is not configured in this session"}
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
	if childLog != nil {
		if _, err := childLog.Append(events.AssistantMessage, events.MessagePayload{
			Role:   "assistant",
			Blocks: []events.BlockPayload{{Type: "text", Text: final}},
		}); err != nil {
			log.Printf("fork child: final report append failed: %v", err)
		}
	}
	return subagent.Result{FinalText: final, Rounds: outcome.Rounds}
}

func outcomeLabel(o *orchestrator.BuildOutcome) string {
	switch {
	case o.Verified && o.VerifiedBestEffort:
		return "verified-best-effort"
	case o.Verified:
		return "verified"
	case o.Paused:
		return "paused"
	case o.Exhausted:
		return "exhausted"
	}
	return "done"
}
