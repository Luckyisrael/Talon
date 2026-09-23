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
	"github.com/luckyisrael/talon/backend/internal/harness/worktree"
	"github.com/luckyisrael/talon/backend/internal/hooks"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// childRunner is the H9 in-process subagent backend: a child runs a second
// orchestrator loop over a scoped tool registry on the same provider client.
// Nothing interlaces into the parent transcript — the child's outcome is
// its Result alone, delivered through the delegate tool.
type childRunner struct {
	// workspace is the parent's directory: the shared root for
	// read-only children (today's path, zero cost) and the merge target
	// for a mutating child's private worktree (W12.1).
	workspace string
	// build assembles one buildSession rooted at root. mutate selects the
	// registry class: false keeps the read-only filter, true gives the
	// full scoped set (writes confined to root — the worktree for a
	// mutating child).
	build func(root string, mutate bool) *buildSession
	// newSession creates the child's session state for its workspace
	// root — the private worktree when mutating, so phase marks and the
	// emitter describe where the child actually works.
	newSession func(workspace string) *orchestrator.SessionState
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
// child lacks are simply not in its registry, and the delegation tools are
// REMOVED from the child's registry (depth 1 is enforced at the service
// seam; un-executability is the second wall). A mutating child additionally
// works in a private git worktree whose diff merges back on dispose.
func (r *childRunner) RunChild(ctx context.Context, spec subagent.Spec) subagent.Result {
	prompt := strings.TrimSpace(spec.Prompt)
	if prompt == "" && !spec.Fork {
		return subagent.Result{Errored: true, Err: "child task prompt is empty"}
	}
	if spec.Fork && prompt == "" {
		prompt = subagent.DefaultForkTask
	}

	// W12.1: the overlay opens before anything is assembled. The service
	// already vetted the posture (DELEGATION_ISOLATION), so a failure
	// here is git reality — non-git workspace, no commits — refused
	// structured with CHILD_WORKTREE's stage text. The deferred discard
	// guarantees no early return (nil session, refused hook) leaks a
	// registered worktree.
	var overlay *worktree.Overlay
	disposed := false
	root := r.workspace
	if spec.Mutating {
		ov, err := worktree.Open(r.workspace)
		if err != nil {
			return subagent.Result{Errored: true, Err: err.Error()}
		}
		overlay = ov
		root = ov.Root
		defer func() {
			if overlay != nil && !disposed {
				if derr := overlay.Discard(); derr != nil {
					log.Printf("child %s: worktree cleanup after early exit: %v", spec.Label, derr)
				}
			}
		}()
	}

	bs := r.build(root, spec.Mutating)
	// W11: a child's dialled MCP servers reap with the child, exactly like
	// the parent's — closeMCP is idempotent and nil-safe.
	defer bs.closeMCP()
	if bs == nil {
		return subagent.Result{Errored: true, Err: "child could not be assembled in this session"}
	}
	sess := r.newSession(root)
	if sess == nil {
		return subagent.Result{Errored: true, Err: "child session state could not be created"}
	}
	defer sess.Emitter.Close()

	// W10 SubagentStart: the workspace's hooks gate the spawn itself,
	// right after the child session exists (SessionStart already fired in
	// build()). deny — and ask, since no seam is composed here — refuses
	// with the reason the parent model reads through the delegate tool;
	// an extension failure refuses too, failed closed (DSH's law).
	// Executions audit to the project log. Nil hooks (flag off / no
	// config): no-op, byte-identical.
	if bs.hooks != nil {
		sp := hooks.Payload{
			Event: hooks.SubagentStart, Prompt: prompt,
			ProjectID: bs.projectID, PromptID: bs.promptID,
		}
		d, herr := bs.hooks.CheckAudited(ctx, sp, func(rec hooks.ExecRecord) {
			appendHookExecToLog(r.log, bs.projectID, rec)
		})
		if herr != nil || d.Decision == hooks.Deny || d.Decision == hooks.Ask {
			reason := d.Reason
			if herr != nil {
				reason = herr.Error()
			}
			return subagent.Result{Errored: true,
				Err: "a project hook refused to spawn the child: " + reason}
		}
	}

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

	// W12.1 dispose, before anything reports the outcome: success merges
	// the child's diff back (a conflict is a structured apply error — the
	// lost-update class surfacing instead of winning silently); failure
	// discards the overlay so a broken child's partial state never
	// touches the parent. Cleanup runs inside both paths either way.
	var disposeErr error
	if overlay != nil {
		disposed = true
		if err != nil || outcome == nil {
			if derr := overlay.Discard(); derr != nil {
				log.Printf("child %s: worktree discard: %v", spec.Label, derr)
			}
		} else {
			disposeErr = overlay.MergeBack()
		}
	}
	var cost float64
	if outcome != nil {
		cost = outcome.CostUSD
	}

	// W10 SubagentStop: notify-class — the child turn is over whatever
	// happened, so this event's own failure only logs (DSH: an extension
	// never corrupts what already finished). Async entries (slack) spawn
	// from Check and return immediately. Executions audit to the project
	// log; nil hooks (flag off / no config): no-op, byte-identical.
	if bs.hooks != nil {
		sp := hooks.Payload{
			Event: hooks.SubagentStop, Prompt: prompt,
			ProjectID: bs.projectID, PromptID: bs.promptID,
		}
		switch {
		case err != nil:
			sp.Outcome, sp.Reason = "failure", err.Error()
		case disposeErr != nil:
			sp.Outcome, sp.Reason = "failure", disposeErr.Error()
		case outcome == nil || outcome.Stalled || outcome.HookStop:
			sp.Outcome = "failure"
		default:
			sp.Outcome = "success"
		}
		bs.hooks.CheckAudited(ctx, sp, func(rec hooks.ExecRecord) {
			appendHookExecToLog(r.log, bs.projectID, rec)
		})
	}
	if err != nil {
		return subagent.Result{Errored: true, Err: fmt.Sprintf("child loop failed: %v", err), CostUSD: cost}
	}
	if outcome == nil {
		return subagent.Result{Errored: true, Err: "child produced no outcome"}
	}
	if disposeErr != nil {
		return subagent.Result{
			Errored: true, Rounds: outcome.Rounds, CostUSD: cost,
			Err: fmt.Sprintf("the child finished (%d rounds), but its isolated changes did not merge back: %v",
				outcome.Rounds, disposeErr),
		}
	}
	if outcome.Stalled {
		return subagent.Result{Errored: true,
			Err:    "child stalled: repeated full responses with no tool calls",
			Rounds: outcome.Rounds, CostUSD: cost}
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
	return subagent.Result{FinalText: final, Rounds: outcome.Rounds, CostUSD: cost}
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
