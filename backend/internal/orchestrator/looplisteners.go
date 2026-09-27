// This file is W14.1: the H2/§2.4 table made concrete. Every cross-cutting
// call the loop body used to make inline now flows through a seam here, and
// the loop file keeps claim → pre-step → request → append → dispatch →
// append → close. DSH's rule, ours now: everything beyond "call the model,
// run the tools, repeat" belongs to listeners.
//
//	table row                          seam (this file)
//	guardrail.Check(...)               toolPolicy      → ToolPolicy event
//	MarkSkillSeen / auto-attach        skillDelivered  → ToolDispatch event
//	                                   autoAttachSkills (moved verbatim)
//	NoteFileWrite nudge                fsWrite         → FSWrite event
//	EmitToolCall running/completed     emitDispatch/emitResult
//	                                   → ToolDispatch/ToolResult events
//	verify scheduling after N writes   enforcePeriodicVerify (moved verbatim)
//	failure-index reminder             failureReminder (failurefeed.go — its
//	                                   own file; reacts to the ToolResult the
//	                                   emit seam already published, reading
//	                                   the final post-hook output)
//
// Migration shape is H2's own: wraps-then-replaces. In the wrap stage this
// file owns the extracted bodies and the driver flag adds bus publishes
// around them — the legacy behavior is the prefix of the driver behavior,
// byte for byte, which is exactly what the parity gate proves (both
// polarities replay the same scripted provider and the same six golden
// tasks to identical transcripts). The replace stage — built-ins becoming
// subscribers themselves and composition rows switching them on — lands
// after the parity gate has been green, one listener at a time.
//
// TALONSTUDIO_LOOP gates everything: unset, "legacy", "off", or garbage
// means today's path with zero publishes (fail-closed to legacy — a typo in
// a migration flag must not silently enable the new architecture). "driver"
// (or the house truthy set 1|true|on|v1) arms the bus.
package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// loopDriverEnabled reads TALONSTUDIO_LOOP. Only an explicit driver (or the
// truthy set) enables the bus; everything else is legacy byte-for-byte.
func loopDriverEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_LOOP"))) {
	case "driver", "1", "true", "on", "v1":
		return true
	default:
		return false
	}
}

// PolicyEvent is the ToolPolicy point: one call, before anything runs.
type PolicyEvent struct {
	Call ai.ToolUseBlock
}

// PolicyDecision is a listener's answer: allow the call, or veto it with a
// rule name and reason the model sees. A veto renders exactly like a native
// guardrail refusal — one vocabulary for "this may not run".
type PolicyDecision struct {
	Allow  bool
	Rule   string
	Reason string
}

// ToolPolicyListener decides whether a dispatched call may run. It runs
// AFTER the built-in deterministic guardrails have allowed the call — a
// composition adds guardrails, it does not replace the security-critical
// ones. Panics fail closed (see askPolicy).
type ToolPolicyListener func(PolicyEvent) PolicyDecision

// DispatchEvent is the ToolDispatch point. Stage names which reaction fired:
// "start" (a call is about to run) or "skill" (a read_skill succeeded, so
// the skill counts as delivered). Listeners observe; they do not decide.
type DispatchEvent struct {
	ID     string
	Name   string
	Target string
	Verb   string
	Stage  string
}

// ToolDispatchListener observes dispatch activity.
type ToolDispatchListener func(DispatchEvent)

// ResultEvent is the ToolResult point: one call's outcome, carrying the
// output the emit seam saw (post spill/prune — what the transcript will
// hold). IsError makes the error-shaped subset (row 2's channel) readable
// without re-deriving it. Listeners observe; they do not decide.
type ResultEvent struct {
	ID      string
	Name    string
	Target  string
	Verb    string
	IsError bool
	Output  string
}

// ToolResultListener observes tool results.
type ToolResultListener func(ResultEvent)

// FSWriteEvent is the FSWrite point: a successful write tool touched Path.
// This is the channel the verify-cadence counter and any format-on-write
// style listener key on.
type FSWriteEvent struct {
	Tool string
	Path string
}

// FSWriteListener observes successful writes.
type FSWriteListener func(FSWriteEvent)

// policyRefusal is the refusal shape both kinds of policy answer render
// through: built-in guardrails (their Decision) and composition listeners
// (their PolicyDecision). One refusal block, one message shape.
type policyRefusal struct {
	Rule    string
	Message string
}

// loopSubscriptions is the driver bus: per-channel listener sets, zero
// value ready, guarded by its own mutex (dispatch runs under the bounded
// pipeline's pool, so publishes happen concurrently). The orchestrator owns
// one; the flag decides whether publishes reach it — with the flag off the
// slices are simply never read (today's path, byte-identical).
type loopSubscriptions struct {
	mu       sync.Mutex
	policy   []ToolPolicyListener
	dispatch []ToolDispatchListener
	result   []ToolResultListener
	fsWrite  []FSWriteListener
}

// SubscribeLoopPolicy adds a composition guardrail. It runs after the
// built-ins allow the call; the first veto wins.
func (o *Orchestrator) SubscribeLoopPolicy(l ToolPolicyListener) {
	o.loopSubs.mu.Lock()
	defer o.loopSubs.mu.Unlock()
	o.loopSubs.policy = append(o.loopSubs.policy, l)
}

// SubscribeLoopDispatch adds a dispatch observer.
func (o *Orchestrator) SubscribeLoopDispatch(l ToolDispatchListener) {
	o.loopSubs.mu.Lock()
	defer o.loopSubs.mu.Unlock()
	o.loopSubs.dispatch = append(o.loopSubs.dispatch, l)
}

// SubscribeLoopResult adds a result observer.
func (o *Orchestrator) SubscribeLoopResult(l ToolResultListener) {
	o.loopSubs.mu.Lock()
	defer o.loopSubs.mu.Unlock()
	o.loopSubs.result = append(o.loopSubs.result, l)
}

// SubscribeLoopFSWrite adds a write observer.
func (o *Orchestrator) SubscribeLoopFSWrite(l FSWriteListener) {
	o.loopSubs.mu.Lock()
	defer o.loopSubs.mu.Unlock()
	o.loopSubs.fsWrite = append(o.loopSubs.fsWrite, l)
}

// askPolicy runs the composition policy listeners after the built-ins
// allowed the call. First veto wins and stops the walk. A panicking
// listener fails closed: an extension that broke must not become the hole
// it was written to guard (DSH: extension failure never corrupts the loop —
// here it refuses, with the failure named where the model can see it).
func (s *loopSubscriptions) askPolicy(call ai.ToolUseBlock) *policyRefusal {
	s.mu.Lock()
	ls := make([]ToolPolicyListener, len(s.policy))
	copy(ls, s.policy)
	s.mu.Unlock()
	for _, l := range ls {
		dec, panicked := runPolicyListener(l, PolicyEvent{Call: call})
		if panicked != nil {
			return &policyRefusal{
				Rule:    "extension listener",
				Message: fmt.Sprintf("a composition policy listener failed (%v) and this call was refused — failing closed; fix or remove the listener", panicked),
			}
		}
		if !dec.Allow {
			rule := strings.TrimSpace(dec.Rule)
			if rule == "" {
				rule = "composition"
			}
			msg := strings.TrimSpace(dec.Reason)
			if msg == "" {
				msg = "refused by a composition policy listener"
			}
			return &policyRefusal{Rule: rule, Message: msg}
		}
	}
	return nil
}

// runPolicyListener isolates one listener: its panic becomes the reason a
// fail-closed refusal carries instead of a dead goroutine.
func runPolicyListener(l ToolPolicyListener, ev PolicyEvent) (dec PolicyDecision, panicVal any) {
	defer func() {
		if r := recover(); r != nil {
			dec = PolicyDecision{}
			panicVal = r
		}
	}()
	return l(ev), nil
}

// publishDispatch fans an event out to observers. A panicking observer is
// logged and skipped: observers decide nothing, so isolating them is the
// whole of their safety story.
func (s *loopSubscriptions) publishDispatch(ev DispatchEvent) {
	for _, l := range s.snapshotDispatch() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("orchestrator: dispatch listener panicked (skipped): %v", r)
				}
			}()
			l(ev)
		}()
	}
}

// publishResult fans a result out to observers (see publishDispatch).
func (s *loopSubscriptions) publishResult(ev ResultEvent) {
	for _, l := range s.snapshotResult() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("orchestrator: result listener panicked (skipped): %v", r)
				}
			}()
			l(ev)
		}()
	}
}

// publishFSWrite fans a write out to observers (see publishDispatch).
func (s *loopSubscriptions) publishFSWrite(ev FSWriteEvent) {
	for _, l := range s.snapshotFSWrite() {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("orchestrator: fs-write listener panicked (skipped): %v", r)
				}
			}()
			l(ev)
		}()
	}
}

// The snapshot helpers copy under the lock so listeners run without it — a
// slow extension must not serialize dispatch, and a subscription arriving
// mid-batch must not race the walk.
func (s *loopSubscriptions) snapshotDispatch() []ToolDispatchListener {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolDispatchListener, len(s.dispatch))
	copy(out, s.dispatch)
	return out
}

func (s *loopSubscriptions) snapshotResult() []ToolResultListener {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolResultListener, len(s.result))
	copy(out, s.result)
	return out
}

func (s *loopSubscriptions) snapshotFSWrite() []FSWriteListener {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FSWriteListener, len(s.fsWrite))
	copy(out, s.fsWrite)
	return out
}

// loopSeam is one construction point's view of the bus: the driver flag read
// once (so one batch/turn never splits across a mid-flight env change) and
// the orchestrator's subscriptions. Built with o.loopSeam().
type loopSeam struct {
	subs   *loopSubscriptions
	driver bool
}

// loopSeam resolves the flag for this batch/turn. Flag off: driver is false
// and no publish ever runs — the seams below are the extracted legacy code,
// nothing more.
func (o *Orchestrator) loopSeam() loopSeam {
	return loopSeam{subs: &o.loopSubs, driver: loopDriverEnabled()}
}

// toolPolicy is §2.4 row 1: guardrail.Check becomes the ToolPolicy event.
// The built-in deterministic guardrails run first, exactly as before
// (composition adds guardrails; it never weakens the security-critical
// ones), then — driver only — the composition listeners walk. nil means
// allowed.
func (cc loopSeam) toolPolicy(o *Orchestrator, sess *SessionState, call ai.ToolUseBlock, opts RunOptions) *policyRefusal {
	if dec := o.guardrail(sess, call.Name, call.Input, opts); !dec.Allowed {
		return &policyRefusal{Rule: dec.Rule, Message: dec.Message}
	}
	if !cc.driver {
		return nil
	}
	return cc.subs.askPolicy(call)
}

// emitDispatch is §2.4 row 5 (running half): the emitter's "running" row,
// published on ToolDispatch. The emit happens first so the legacy bytes are
// the prefix of the driver path; observers join after.
func (cc loopSeam) emitDispatch(sess *SessionState, id, name, target, verb string) {
	sess.Emitter.EmitToolCall(id, name, target, verb, "running")
	if !cc.driver {
		return
	}
	cc.subs.publishDispatch(DispatchEvent{ID: id, Name: name, Target: target, Verb: verb, Stage: "start"})
}

// emitResult is §2.4 row 5 (completed half): the status row on ToolResult,
// carrying the post-spill/prune output the transcript will hold. The emit
// happens first so the legacy bytes are the prefix of the driver path;
// observers join after.
func (cc loopSeam) emitResult(sess *SessionState, id, name, target, verb, output string, isError bool) {
	status := "completed"
	if isError {
		status = "error"
	}
	sess.Emitter.EmitToolCall(id, name, target, verb, status)
	if cc.driver {
		cc.subs.publishResult(ResultEvent{
			ID: id, Name: name, Target: target, Verb: verb,
			IsError: isError, Output: output,
		})
	}
}

// skillDelivered is §2.4 row 3 (MarkSkillSeen half): a successful read_skill
// counts as delivered, so auto-attach must not inject the same body again
// when a trigger fires this turn. Bookkeeping first (legacy prefix), then
// the dispatch-taxonomy event with Stage "skill".
func (cc loopSeam) skillDelivered(sess *SessionState, call ai.ToolUseBlock, isError bool) {
	if call.Name != "read_skill" || isError {
		return
	}
	var rs struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(call.Input, &rs) != nil || rs.Name == "" {
		return
	}
	sess.MarkSkillSeen(rs.Name)
	if cc.driver {
		cc.subs.publishDispatch(DispatchEvent{ID: call.ID, Name: call.Name, Stage: "skill"})
	}
}

// fsWrite is §2.4 row 4: the write bookkeeping (count the write as read so
// a follow-up edit is not refused by read-before-write; watch for thrash)
// and the NoteFileWrite nudge, now the FSWrite seam. All legacy state
// changes complete first; the event publishes last, once per successful
// write with a parsed path.
func (cc loopSeam) fsWrite(sess *SessionState, call ai.ToolUseBlock, isError bool) string {
	if isError {
		return ""
	}
	switch call.Name {
	case "write_file", "edit_file", "multi_edit":
	default:
		return ""
	}
	var w struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(call.Input, &w) != nil || w.Path == "" {
		return ""
	}
	// L3 routes-first: the first successful write under app/**.tsx closes
	// the route gate for the turn. Errored writes never reach here, and
	// skeleton-or-full both count — the S2 standard is "first route file"
	// (FM-L3b).
	if routePathRe.MatchString(w.Path) {
		sess.MarkRouteScaffold()
	}
	// A file the model just wrote or edited is one it now knows the contents
	// of. Count it as read so a follow-up edit this turn is not refused by
	// the read-before-write guardrail — the model created that content, it is
	// not working from an assumption.
	sess.MarkFileRead(w.Path)
	if mt, ok := fileMtime(sess.WorkspacePath, w.Path); ok {
		sess.NoteFileKnown(w.Path, mt)
	}
	// Two rewrites are iteration; the third is thrash. Stop and re-read the
	// errors instead of writing a fourth version blind.
	watchNote := ""
	if n := sess.NoteFileWrite(w.Path); n == rewriteWatchThreshold {
		watchNote = fmt.Sprintf("\n\nNote: this is your %drd write to %s this turn. "+
			"Stop writing it. Re-read the failing errors and the relevant files first, "+
			"then make one deliberate fix.", n, w.Path)
	}
	if cc.driver {
		cc.subs.publishFSWrite(FSWriteEvent{Tool: call.Name, Path: w.Path})
	}
	return watchNote
}

// enforcePeriodicVerify counts successful writes since the last passing check
// and appends a verify-now nudge at the threshold. Only a *passing* check
// resets the count: a failed check means the code is still unverified, and a
// tool error means the check never ran. Past twice the threshold the nudge
// names the changed files so the check lands where the drift is.
//
// §2.4 row 6 — the FSWrite counter listener's built-in. Its body moved here
// from the loop file verbatim; it still runs as a direct call at the batch
// boundary because its inputs are the whole batch (writes AND passing
// checks, including the malformed-input edge a per-write event would miss).
// It reads the same write activity the fsWrite seam publishes on FSWrite —
// moving it onto that channel as a subscriber is the replace-stage step.
func (o *Orchestrator) enforcePeriodicVerify(sess *SessionState, calls []ai.ToolUseBlock, results []ai.Block, opts RunOptions, conversation *[]ai.Message, outcome *BuildOutcome) {
	byID := make(map[string]ai.Block, len(results))
	for _, r := range results {
		if r.ToolUseID != "" {
			byID[r.ToolUseID] = r
		}
	}
	for _, u := range calls {
		r, ok := byID[u.ID]
		if !ok || r.IsError {
			continue
		}
		switch u.Name {
		case "write_file", "edit_file", "multi_edit":
			sess.NoteWrite()
		case "run_checks", "run_typecheck", "run_lint":
			var vr verifyResult
			if json.Unmarshal([]byte(r.Content), &vr) == nil && vr.Passed {
				sess.NoteVerified()
			}
		}
	}
	n := sess.WritesUnverified()
	if n < verifyEnforceThreshold {
		return
	}
	msg := fmt.Sprintf("You have made %d file writes since the last passing verification. "+
		"Call run_checks now, fix what it reports, then continue building.", n)
	if n >= 2*verifyEnforceThreshold {
		if touched := o.touchedFiles(opts); len(touched) > 0 {
			msg += "\nFiles changed this turn: " + strings.Join(touched, ", ")
		}
	}
	nudge := ai.UserText(msg)
	*conversation = append(*conversation, nudge)
	outcome.Messages = append(outcome.Messages, nudge)
}

// autoAttachSkills injects the bodies of any auto-attach skills whose triggers
// fired on the tool calls just made — each at most once per session — and returns
// the text to append as a follow-up user turn (or "" if nothing new applies).
// Handing the model the current, version-pinned guidance the moment it acts in a
// skill's area is what holds quality up on models that would not otherwise know
// the exact API shipped here.
//
// §2.4 row 3 (auto-attach half) — moved here from the loop file verbatim.
// It reacts to the dispatch activity the emit seam already published, so it
// needs no second event of its own; it runs direct in both flag polarities.
//
// Returns context blocks rather than a text blob: the skill body is guidance
// the model needs verbatim, but a user never typed it, and rendering it as a
// chat bubble put a 400-line skill document in the middle of the conversation.
// One block per skill, titled with the skill name, so the transcript shows a
// single line and the body stays collapsed until it is asked for.
func (o *Orchestrator) autoAttachSkills(sess *SessionState, calls []ai.ToolUseBlock) []ai.Block {
	if o.knowledge == nil {
		return nil
	}
	var out []ai.Block
	for _, call := range calls {
		for _, s := range o.knowledge.AutoAttachFor(call.Name, call.Input) {
			if !sess.MarkSkillSeen(s.Name) {
				continue // already delivered this session
			}
			body := s.Body
			if body == "" {
				continue
			}
			out = append(out, ai.Context("skill", "Applied the "+s.Name+" skill", body))
			// Show it in the trace too: an auto-attached skill is a real thing
			// that happened, and the timeline should say so rather than inject
			// silently.
			sess.Emitter.EmitToolCall("", "read_skill", "", "Applied the "+s.Name+" skill", "completed")
		}
	}
	return out
}
