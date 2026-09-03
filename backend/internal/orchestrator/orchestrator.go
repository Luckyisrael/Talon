package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/guardrails"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/tools"
)

// maxContextTokens is when the conversation gets compacted. Sized to leave room
// for the system prompt, tool definitions and a full response.
const maxContextTokens = 120_000

type Orchestrator struct {
	ai *ai.Client

	mu       sync.RWMutex
	sessions map[int64]*SessionState

	store     *store.Store
	knowledge *agent.Registry
	// preinstalled is the set the install guardrail checks against. Computed once
	// from the knowledge layer at construction so every tool call does not rebuild
	// it.
	preinstalled map[string]bool
	// discipline is the tier-specific working-discipline block appended to the
	// build system prompt, derived once from the model's capabilities. Empty for a
	// strong model (which needs none) and when there is no AI client.
	discipline string
	maxRounds  int
}

const (
	// defaultMaxRounds is the round budget for one build turn when AI_MAX_ROUNDS is
	// unset. It is the model-agnostic knob: a weaker model that works in smaller
	// steps needs more rounds to reach the same finished state, so operators tune it
	// per model. Raised from the original 40 — a real build hit that cap at ~90%
	// done — and exhaustion is now a resumable pause rather than a discarded build.
	defaultMaxRounds = 60
	minMaxRounds     = 1
	maxMaxRounds     = 500
)

// maxRoundsFromEnv reads AI_MAX_ROUNDS, falling back to defaultMaxRounds when it is
// unset, unparseable, or out of the sane range. Clamped so a typo cannot set a
// zero budget (a turn that can never act) or an unbounded one.
func maxRoundsFromEnv() int {
	v := strings.TrimSpace(os.Getenv("AI_MAX_ROUNDS"))
	if v == "" {
		return defaultMaxRounds
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minMaxRounds || n > maxMaxRounds {
		log.Printf("orchestrator: ignoring invalid AI_MAX_ROUNDS=%q, using %d", v, defaultMaxRounds)
		return defaultMaxRounds
	}
	return n
}

func New(aiClient *ai.Client, s *store.Store, knowledge *agent.Registry) *Orchestrator {
	o := &Orchestrator{
		ai:           aiClient,
		sessions:     make(map[int64]*SessionState),
		store:        s,
		knowledge:    knowledge,
		preinstalled: knowledge.PreinstalledSet(),
		maxRounds:    maxRoundsFromEnv(),
	}
	// Adapt the prompt to the model once at startup: a weaker model gets the
	// working discipline spelled out, a strong one is trusted with the base
	// prompt. Guarded on a real client so tests that pass nil stay silent.
	if aiClient != nil {
		caps := aiClient.Capabilities()
		o.discipline = caps.WorkingDiscipline()
		log.Printf("orchestrator: model %q classified as %s tier", caps.Model, caps.Tier)
	}
	return o
}

func (o *Orchestrator) CreateSession(id, projectID int64, workspacePath string) *SessionState {
	emitter := events.NewSessionEmitter(id, 128)
	sess := NewSession(id, projectID, workspacePath, emitter)

	o.mu.Lock()
	o.sessions[id] = sess
	o.mu.Unlock()

	o.loadProjectContext(sess, projectID)
	return sess
}

func (o *Orchestrator) loadProjectContext(sess *SessionState, projectID int64) {
	if adrs, err := o.store.GetActiveADRs(projectID); err == nil {
		sess.LoadADRs(adrs)
	}
	if memory, err := o.store.GetAllMemory(projectID); err == nil {
		sess.LoadMemory(memory)
	}
	o.seedPlanTasks(sess, projectID)
}

// seedPlanTasks turns the project's saved plan into a task list the build loop can
// carry and check off. The plan (screens + features) was settled in Plan mode and
// persisted as PlanJSON; seeding it once here — rather than letting the model
// re-derive the plan from scratch every round — is what stops the plan-rebuild
// token burn that discarded a near-finished build. A missing, empty, or malformed
// plan is not an error: the build simply runs without a checklist, exactly as it
// did before this existed.
func (o *Orchestrator) seedPlanTasks(sess *SessionState, projectID int64) {
	project, err := o.store.GetProject(projectID)
	if err != nil || project == nil || strings.TrimSpace(project.PlanJSON) == "" {
		return
	}
	var plan ai.AppPlan
	if err := json.Unmarshal([]byte(project.PlanJSON), &plan); err != nil {
		return
	}
	for _, sc := range plan.Screens {
		name := strings.TrimSpace(sc.Name)
		if name == "" {
			continue
		}
		desc := "Screen: " + name
		if d := strings.TrimSpace(sc.Description); d != "" {
			desc += " — " + d
		}
		// Screen names often carry parenthetical qualifiers ("Portfolio (tab)")
		// that never appear in route file names. Slug the base name so the
		// file heuristic can match portfolio.tsx to the Portfolio screen; the
		// full name stays in the description the model sees.
		sess.AddTask("plan:screen:"+planSlug(screenBaseName(name)), desc)
	}
	for i, ft := range plan.Features {
		ft = strings.TrimSpace(ft)
		if ft == "" {
			continue
		}
		sess.AddTask(fmt.Sprintf("plan:feature:%d", i), "Feature: "+ft)
	}
}

// planSlug reduces a screen name to a stable, id-safe token: lowercase, with any
// run of non-alphanumeric characters collapsed to a single dash. It only has to be
// stable within one session (task ids are matched against it when marking done),
// not globally unique.
// screenBaseName strips a trailing parenthetical qualifier from a plan screen
// name: "Portfolio (tab)" → "Portfolio". Qualifiers describe navigation role,
// not identity, and route files never contain them.
func screenBaseName(name string) string {
	if i := strings.Index(name, " ("); i >= 0 {
		return strings.TrimSpace(name[:i])
	}
	return name
}

func planSlug(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// markScreenTasksFromFiles checks off screen tasks whose route file was written
// this run. It is called only after the verify gate passes, so a match means the
// screen's file exists AND the project compiles — an honest completion, not a
// guess. Matching is by name slug: a screen "Send" is marked when a touched path
// segment slugs to "send" (e.g. app/send.tsx). Screens that do not match by name
// (a "Home" screen living in app/index.tsx) stay remaining rather than be claimed
// — the checklist under-claims before it lies. Features are never marked here;
// their file mapping is too loose to be honest, so they rely on mark_task_done.
func (o *Orchestrator) markScreenTasksFromFiles(sess *SessionState, touched []string) {
	if len(touched) == 0 {
		return
	}
	writtenSlugs := make(map[string]bool)
	for _, p := range touched {
		for _, seg := range strings.Split(strings.ToLower(p), "/") {
			seg = strings.TrimSuffix(seg, filepath.Ext(seg))
			if s := planSlug(seg); s != "" {
				writtenSlugs[s] = true
			}
		}
	}
	for _, t := range sess.GetTasks() {
		if t.Status == "completed" {
			continue
		}
		slug := strings.TrimPrefix(t.ID, "plan:screen:")
		if slug == t.ID {
			continue // not a screen task
		}
		if writtenSlugs[slug] {
			sess.MarkPlanTask(t.ID, t.Description, "completed", "route file written and project compiles")
		}
	}
}

func (o *Orchestrator) GetSession(id int64) *SessionState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.sessions[id]
}

func (o *Orchestrator) RemoveSession(id int64) {
	o.mu.Lock()
	sess, ok := o.sessions[id]
	if ok {
		delete(o.sessions, id)
	}
	o.mu.Unlock()

	if ok {
		sess.Emitter.Close()
	}
}

// ToolExecutor runs a single tool call and returns its output. The bool reports
// whether the call failed, which the loop passes back as tool_result.is_error.
type ToolExecutor func(ctx context.Context, name string, input json.RawMessage) (output string, isError bool)

// BuildOutcome is everything the caller needs to persist after a turn.
type BuildOutcome struct {
	// Messages holds only the turns this run added, ready to append to the
	// stored conversation.
	Messages     []ai.Message
	Usage        ai.Usage
	Rounds       int
	FinalText    string
	FilesTouched []string
	Verified     bool
	// RemainingTasks names the seeded plan items still pending when the turn
	// ended. A turn can compile cleanly while the plan is half-built (the
	// foundation-first stop); callers surface these so "success" is never
	// mistaken for "everything is built". Empty means the plan is complete —
	// or there was no plan.
	RemainingTasks []string
	// Paused is set when the turn stopped to ask the user a question through
	// ask_user rather than finishing. A paused turn returns a nil error — it is a
	// deliberate stop, not a failure — so a caller must check Paused before
	// treating a nil error as "done". PendingToolUseID is the ask_user tool_use
	// the user's answer resolves as its tool_result on resume.
	Paused           bool
	PendingToolUseID string
	// Exhausted is set when the turn stopped because it reached the round budget
	// (maxRounds) while still working, rather than finishing or asking a question.
	// Like Paused it returns a nil error: a spent budget is a deliberate, graceful
	// stop, not a failure — every finished round is already persisted, so the turn
	// resumes from where it stopped. Unlike Paused it carries no pending tool_use;
	// it resumes through the ordinary Resume path (a "keep going" continuation),
	// not by answering a question. A caller must check Exhausted, like Paused,
	// before treating a nil error as "done".
	Exhausted bool
}

// RunOptions configures one agentic turn.
type RunOptions struct {
	SystemPrompt string
	// History is the conversation so far, ending with the user's new prompt.
	History []ai.Message
	Tools   []ai.ToolDefinition
	Execute ToolExecutor
	// ReadOnlyTool reports whether a tool can run concurrently with others in
	// the same turn. Nil means treat everything as sequential.
	ReadOnlyTool func(name string) bool
	// FilesTouched reports the paths changed so far, for the verification gate
	// and the final summary.
	FilesTouched func() []string
	// Exists reports whether a path already exists in the workspace. The
	// read-before-write guardrail uses it to tell creating a new file (always
	// fine) from overwriting one the model has not read. Nil treats every path
	// as new.
	Exists func(path string) bool
	// Persist, if set, is handed each round's turns as the loop advances rather
	// than only at the end, so an interrupted run keeps the rounds that already
	// finished. It is called at every round boundary (and once more on return)
	// with just the messages produced since the previous call — never a message
	// twice. A slow or failing Persist must not wedge the loop; keep it cheap and
	// swallow its own errors.
	Persist func(msgs []ai.Message)
}

// RunAgentLoop drives the agent until it stops calling tools and the code
// verifies, or until the round budget runs out.
//
// The loop terminates on the model's stop_reason, never on "the model said
// something". A turn that produces prose alongside tool calls is still working;
// treating text as completion is what makes an agent quit halfway.
func (o *Orchestrator) RunAgentLoop(ctx context.Context, sess *SessionState, opts RunOptions) (*BuildOutcome, error) {
	sess.TransitionTo(PhaseContextBuild)
	systemPrompt := o.enrichPromptWithContext(sess, opts.SystemPrompt)
	// Every other input to the enriched prompt (base prompt, rules, manifest,
	// working discipline) is fixed for the process; only the per-project ADRs and
	// memory can differ, and both are loaded once per session. Track their counts
	// so the loop rebuilds the prompt only if one actually changed — a rebuild
	// that produces the same bytes still hands the provider a fresh string and, on
	// caches keyed by the prompt prefix, keeps busting the breakpoint the caching
	// is meant to hold.
	lastADRs, lastMemory := len(sess.GetADRs()), len(sess.GetMemory())
	lastTaskVer := sess.TaskVersion()

	sess.TransitionTo(PhaseGenerate)
	sess.ClearReadFiles()
	sess.ResetWriteCounts()

	// conversation is what goes to the provider; added tracks just this run's
	// turns so the caller can append them to storage.
	conversation := make([]ai.Message, len(opts.History))
	copy(conversation, opts.History)

	outcome := &BuildOutcome{}
	const maxVerifyAttempts = 6

	// turnStart anchors wall-time accounting: wall time is model-latency-bound
	// (~10s/round observed), so the only honest efficiency metric is rounds —
	// but rounds without durations hide whether time went to the model or to
	// tools. Logged at every exit below.
	turnStart := time.Now()
	logTurn := func(ending string) {
		elapsed := time.Since(turnStart)
		perRound := time.Duration(0)
		if outcome.Rounds > 0 {
			perRound = elapsed / time.Duration(outcome.Rounds)
		}
		log.Printf("orchestrator: turn %s after %d rounds in %s (avg %s/round)",
			ending, outcome.Rounds, elapsed.Round(time.Second), perRound.Round(time.Second))
	}

	// Hand finished turns to the caller as the loop advances so an interrupted
	// run keeps them. `flushed` is the count already handed over; flush() sends
	// only the tail past it, so no message is persisted twice however the loop
	// exits. The deferred call catches the final round (and any early return,
	// including cancellation); the per-round call inside the loop is what makes
	// persistence incremental rather than all-at-the-end.
	flushed := 0
	flush := func() {
		if opts.Persist == nil || flushed >= len(outcome.Messages) {
			return
		}
		opts.Persist(outcome.Messages[flushed:])
		flushed = len(outcome.Messages)
	}
	defer flush()

	for round := 1; round <= o.maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			logTurn("cancelled")
			return outcome, fmt.Errorf("cancelled: %w", err)
		}
		outcome.Rounds = round
		flush() // persist what the previous round produced before starting this one
		sess.SetRound(round)

		// Rebuild the system prompt only when the ADRs, memory, or plan-task graph
		// that feed it changed since the last round. When nothing changed the prompt
		// is byte-for-byte identical, so the existing string is reused and the cached
		// prefix survives into the next round instead of being re-sent as new.
		//
		// The project map refreshes on the same principle but with its own guard:
		// files change almost every round while a build is active, so the map is
		// rebuilt only when the tree's fingerprint actually moved — and only then
		// does the prompt rebuild with it. A fresh map means the model never needs
		// project_map calls to re-learn the layout it already has in context.
		mapChanged := o.refreshProjectMap(sess)
		if adrs, memory, taskVer := len(sess.GetADRs()), len(sess.GetMemory()), sess.TaskVersion(); adrs != lastADRs || memory != lastMemory || taskVer != lastTaskVer || mapChanged {
			systemPrompt = o.enrichPromptWithContext(sess, opts.SystemPrompt)
			lastADRs, lastMemory, lastTaskVer = adrs, memory, taskVer
		}

		if ai.EstimateTokens(conversation) > maxContextTokens {
			before := len(conversation)
			conversation = ai.Compact(conversation, maxContextTokens*3/4)
			sess.Emitter.EmitToolCall("", "compact", "", fmt.Sprintf("Compacted context (%d → %d turns)",
				before, len(conversation)), "completed")
		}

		sess.Emitter.EmitPhase(events.Phase(sess.GetPhase()), fmt.Sprintf("round %d", round))

		// Collapse stale read_file/list_files bodies that a later call in this turn
		// re-fetched: they are otherwise replayed in full every round. This trims a
		// copy for the wire only — `conversation` (and the blocks it shares with
		// outcome.Messages, which is what gets persisted) keeps its full fidelity.
		sendConversation := ai.TrimSupersededToolOutput(conversation)

		result, err := o.ai.ChatWithTools(ctx, systemPrompt, sendConversation, opts.Tools)
		if err != nil {
			sess.TransitionTo(PhaseFailed)
			sess.Emitter.EmitError(err.Error())
			logTurn("model-request-failed")
			return outcome, fmt.Errorf("model request failed: %w", err)
		}

		outcome.Usage.InputTokens += result.Usage.InputTokens
		outcome.Usage.OutputTokens += result.Usage.OutputTokens
		outcome.Usage.CacheReadTokens += result.Usage.CacheReadTokens
		outcome.Usage.CacheWriteTokens += result.Usage.CacheWriteTokens
		sess.Emitter.EmitUsage(outcome.Usage.InputTokens, outcome.Usage.OutputTokens, outcome.Usage.CacheReadTokens)

		// Forward the model's own words. Without this the user watches a build
		// happen with no idea what the agent thinks it is doing.
		for _, b := range result.Blocks {
			switch b.Type {
			case "text":
				sess.Emitter.EmitText(b.Text)
			case "thinking":
				sess.Emitter.EmitThinking(b.Text)
			}
		}

		if result.Text != "" {
			outcome.FinalText = result.Text
		}

		assistantTurn := ai.Message{Role: "assistant", Blocks: result.Blocks}
		if len(result.Blocks) == 0 {
			assistantTurn.Blocks = []ai.Block{{Type: "text", Text: result.Text}}
		}
		conversation = append(conversation, assistantTurn)
		outcome.Messages = append(outcome.Messages, assistantTurn)

		// ── The model is asking the user something ──
		// ask_user pauses the turn instead of running: the loop cannot answer its
		// own question. The assistant turn carrying the tool_use is already recorded
		// above and gets persisted by the deferred flush, so the paused transcript
		// ends on that unanswered call — exactly what resume looks for. Detected
		// before executeTools so no sibling tool runs while the user is deciding.
		if id, question, choices, ok := askUserRequest(result.ToolUses); ok {
			sess.TransitionTo(PhaseWaiting)
			sess.Emitter.EmitQuestion(id, question, choices)
			outcome.Paused = true
			outcome.PendingToolUseID = id
			logTurn("paused-for-user")
			return outcome, nil
		}

		// ── The model is still working ──
		if len(result.ToolUses) > 0 {
			// mark_task_done is orchestrator-owned like ask_user, but it does not
			// pause: apply its effect on the plan graph here, then let the call run
			// its benign Execute so the tool_use still gets a matching tool_result.
			o.applyMarkTaskDone(sess, result.ToolUses)

			results := o.executeTools(ctx, sess, result.ToolUses, opts)
			userTurn := ai.ToolResults(results)
			conversation = append(conversation, userTurn)
			outcome.Messages = append(outcome.Messages, userTurn)

			// Any follow-up guidance goes in a single user turn after the tool
			// results: skills auto-attached by what the model just did, and, if the
			// response was cut off, a truncation nudge. Folding both into one message
			// keeps the tool_results→user shape both providers expect.
			var followUp strings.Builder
			followUp.WriteString(o.autoAttachSkills(sess, result.ToolUses))
			if result.StopReason == "max_tokens" {
				if followUp.Len() > 0 {
					followUp.WriteString("\n\n")
				}
				followUp.WriteString("Your previous response hit the output limit and was truncated. " +
					"Continue from where you stopped — work in smaller steps.")
			}
			if followUp.Len() > 0 {
				nudge := ai.UserText(followUp.String())
				conversation = append(conversation, nudge)
				outcome.Messages = append(outcome.Messages, nudge)
			}

			// Periodic verification is enforced here, in the loop — not in the
			// prompt. Prompt guidance demonstrably fails (a 60-round turn with two
			// verifications); a counter the model cannot skim past does not.
			o.enforcePeriodicVerify(sess, result.ToolUses, results, opts, &conversation, outcome)
			continue
		}

		// ── The model stopped calling tools ──
		if result.StopReason == "max_tokens" {
			nudge := ai.UserText("Your response was truncated at the output limit. Continue.")
			conversation = append(conversation, nudge)
			outcome.Messages = append(outcome.Messages, nudge)
			continue
		}

		touched := o.touchedFiles(opts)
		if len(touched) == 0 {
			// Nothing was changed. This is a legitimate outcome — the user may
			// have asked a question — so take the model at its word.
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			outcome.FilesTouched = touched
			logTurn("done-no-changes")
			return outcome, nil
		}

		// Code changed, so it has to compile before the turn can be called done.
		// Attempts live on the session — the single source of truth — so a
		// resumable turn carries its repair count instead of starting over.
		if sess.GetVerifyAttempts() >= maxVerifyAttempts {
			sess.TransitionTo(PhaseFailed)
			outcome.FilesTouched = touched
			sess.Emitter.EmitError(fmt.Sprintf("code did not pass verification after %d repair attempts", maxVerifyAttempts))
			logTurn("verify-failed")
			return outcome, fmt.Errorf("code did not pass verification after %d repair attempts", maxVerifyAttempts)
		}

		sess.TransitionTo(PhaseVerifyStatic)
		verdict := o.verify(ctx, sess, opts)
		if verdict.ok {
			// The project compiles and these files were written this run, so any
			// screen whose route file now exists is honestly done — mark it before
			// finishing. Features and unmatched screens stay remaining rather than
			// claim a completion we cannot see.
		o.markScreenTasksFromFiles(sess, touched)
		sess.TransitionTo(PhaseDone)
		sess.Emitter.EmitDone("success")
			outcome.FilesTouched = touched
			outcome.Verified = true
			outcome.RemainingTasks = pendingPlanTasks(sess)
			outcome.FinalText += remainingNote(sess, "Verified, but stopping")
			o.recordTurnMemory(sess, touched, outcome)
			logTurn("done-verified")
			return outcome, nil
		}

		sess.IncrementVerifyAttempt()
		sess.TransitionTo(PhaseGenerate)
		feedback := ai.UserText(verdict.feedback)
		conversation = append(conversation, feedback)
		outcome.Messages = append(outcome.Messages, feedback)
	}

	// ── The round budget is spent ──
	// Not a failure: the turn did real work and every finished round is already
	// persisted, so reaching maxRounds is a graceful, resumable pause — not a
	// discarded build. Flag it and return a nil error (like the ask_user pause) so
	// the caller records a resumable status and offers Resume, rather than
	// surfacing an error event that reads as broken. Transition to WAITING_FOR_USER
	// (the turn is waiting for the user to resume it), not FAILED. Resume replays
	// the persisted transcript and the agent continues from where it stopped.
	sess.TransitionTo(PhaseWaiting)
	outcome.FilesTouched = o.touchedFiles(opts)
	outcome.Exhausted = true
	// An exhausted turn is exactly when the user most needs the remaining list:
	// without it "paused" reads as done. Same helper as the success path.
	outcome.RemainingTasks = pendingPlanTasks(sess)
	outcome.FinalText += remainingNote(sess, "Paused")
	log.Printf("orchestrator: turn paused after reaching the %d-round budget; resumable", o.maxRounds)
	logTurn("exhausted")
	return outcome, nil
}

// enforcePeriodicVerify counts successful writes since the last passing check
// and appends a verify-now nudge at the threshold. Only a *passing* check
// resets the count: a failed check means the code is still unverified, and a
// tool error means the check never ran. Past twice the threshold the nudge
// names the changed files so the check lands where the drift is.
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

// askUserRequest reports whether this turn's tool calls include an ask_user,
// and if so returns its id and the question and choices to surface. ask_user is
// handled specially — it pauses the loop — so it is detected before any tool
// runs. A malformed input still pauses (with an empty question) rather than
// being executed as an ordinary call: the model asked to stop and wait.
func askUserRequest(uses []ai.ToolUseBlock) (id, question string, choices []string, ok bool) {
	for _, u := range uses {
		if u.Name != "ask_user" {
			continue
		}
		var a struct {
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
		}
		_ = json.Unmarshal(u.Input, &a)
		return u.ID, a.Question, a.Choices, true
	}
	return "", "", nil, false
}

// applyMarkTaskDone processes any mark_task_done calls in this turn, flipping the
// matched plan tasks to completed. The model names screens or features as they read
// in the checklist — one per "task", or several per "tasks" — and we match by
// screen-name slug first, then by a substring of the task description (which covers
// features and loosely-named screens). Only the first unmatched-and-pending task per
// name is marked. Unmatched names are ignored — better a stale-but-honest checklist
// than one that claims an item the model could not actually identify.
func (o *Orchestrator) applyMarkTaskDone(sess *SessionState, uses []ai.ToolUseBlock) {
	for _, u := range uses {
		if u.Name != "mark_task_done" {
			continue
		}
		var a struct {
			Task  string   `json:"task"`
			Tasks []string `json:"tasks"`
			Note  string   `json:"note"`
		}
		if json.Unmarshal(u.Input, &a) != nil {
			continue
		}
		note := strings.TrimSpace(a.Note)
		if note == "" {
			note = "marked done by the model"
		}
		wants := a.Tasks
		if strings.TrimSpace(a.Task) != "" {
			wants = append([]string{a.Task}, wants...)
		}
		for _, want := range wants {
			o.markOnePlanTask(sess, want, note)
		}
	}
}

// markOnePlanTask flips the first pending plan task matching want.
func (o *Orchestrator) markOnePlanTask(sess *SessionState, want, note string) {
	want = strings.TrimSpace(want)
	if want == "" {
		return
	}
	wantSlug := planSlug(want)
	wantLower := strings.ToLower(want)
	for _, t := range sess.GetTasks() {
		if t.Status == "completed" {
			continue
		}
		slug := strings.TrimPrefix(t.ID, "plan:screen:")
		screenMatch := slug != t.ID && wantSlug != "" && slug == wantSlug
		descMatch := strings.Contains(strings.ToLower(t.Description), wantLower)
		if screenMatch || descMatch {
			sess.MarkPlanTask(t.ID, t.Description, "completed", note)
			break
		}
	}
}

func (o *Orchestrator) touchedFiles(opts RunOptions) []string {
	if opts.FilesTouched == nil {
		return nil
	}
	files := opts.FilesTouched()
	sort.Strings(files)
	return files
}

// executeTools runs one turn's tool calls, in parallel where that is safe.
// Results are returned in the order the model requested them: both providers
// require every tool_use to have a matching tool_result, and Anthropic matches
// on id while some OpenAI-compatible backends are order-sensitive.
func (o *Orchestrator) executeTools(ctx context.Context, sess *SessionState, calls []ai.ToolUseBlock, opts RunOptions) []ai.Block {
	results := make([]ai.Block, len(calls))

	// A batch that writes or deletes the same file more than once carries an order
	// the model may not have meant. Execution keeps the request order (any write
	// already forces parallel=false, so these calls run exactly as sent); the point
	// of detecting them is to surface what the model did instead of letting the
	// last call silently win — one warning row per conflicting path, and a note on
	// every involved tool_result, so whichever call the model reads it sees the
	// conflict it created. No dedup: order is the model's to own.
	conflicts := batchConflicts(calls)
	if len(conflicts) > 0 {
		seen := map[string]bool{}
		for _, path := range conflicts {
			if seen[path] {
				continue
			}
			seen[path] = true
			sess.Emitter.EmitToolCall("", "batch_conflict", path,
				"Conflicting writes to "+path, "warning")
		}
	}

	// Read-only calls can overlap; anything that writes files or shells out
	// runs alone to keep the workspace consistent.
	parallel := opts.ReadOnlyTool != nil && len(calls) > 1
	if parallel {
		for _, c := range calls {
			if !opts.ReadOnlyTool(c.Name) {
				parallel = false
				break
			}
		}
	}

	run := func(i int, call ai.ToolUseBlock) {
		// A tool that panics must not take the build down with it. Recover here,
		// at the body both the parallel and sequential paths invoke, and turn the
		// crash into a failed tool_result the model can react to. Without this a
		// single panicking tool in a goroutine kills the whole process; with it the
		// loop survives and the model tries a smaller step. The result still carries
		// call.ID, so the tool_use is never left without its matching tool_result.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("orchestrator: tool %q panicked: %v\n%s", call.Name, r, debug.Stack())
				results[i] = ai.Block{
					Type:      "tool_result",
					ToolUseID: call.ID,
					Content:   fmt.Sprintf("The %s tool crashed unexpectedly (%v). This is a fault in the tool, not in your request — try a smaller step or a different approach.", call.Name, r),
					IsError:   true,
				}
			}
		}()

		// Guardrails run before anything else: a refused call never executes, never
		// emits a "running" row, and comes back as a failed tool_result so the model
		// reads why and corrects. This is where the always-on rules become binding.
		if dec := o.guardrail(sess, call.Name, call.Input, opts); !dec.Allowed {
			sess.Emitter.EmitGuardrail(dec.Rule, dec.Message)
			results[i] = ai.Block{
				Type:      "tool_result",
				ToolUseID: call.ID,
				Content:   "Blocked by a project guardrail (" + dec.Rule + "): " + dec.Message,
				IsError:   true,
			}
			return
		}

		verb := describeTool(call.Name, call.Input, sess.WorkspacePath)
		target := toolTarget(call.Name, call.Input)
		sess.Emitter.EmitToolCall(call.ID, call.Name, target, verb, "running")

		if taskID := taskIDForTool(call.Name); taskID != "" {
			sess.EmitTaskUpdate(taskID, verb, "in_progress", "")
		}

		output, isError := opts.Execute(ctx, call.Name, call.Input)

		// readNote/watchNote/probeNote carry cross-turn knowledge into this result. They
		// are set by the tracking below and appended to the tool result at the
		// end of run(): small hints that prevent whole wasted rounds.
		readNote := ""
		watchNote := ""
		probeNote := ""

		status := "completed"
		if isError {
			status = "error"
		}
		sess.Emitter.EmitToolCall(call.ID, call.Name, target, verb, status)
		if taskID := taskIDForTool(call.Name); taskID != "" {
			detail := "success"
			if isError {
				detail = firstLine(output)
			}
			sess.EmitTaskUpdate(taskID, verb, "completed", detail)
		}

		if call.Name == "read_file" {
			var rd struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(call.Input, &rd) == nil && rd.Path != "" {
				sess.MarkFileRead(rd.Path)
				// A re-read of an unchanged file is pure waste: its contents are
				// already in the transcript above. Say so once, on the result,
				// so future rounds trust the transcript instead of re-fetching.
				if !isError {
					if mt, ok := fileMtime(sess.WorkspacePath, rd.Path); ok {
						if sess.KnownFresh(rd.Path, mt) {
							readNote = fmt.Sprintf("\n\nNote: this file is unchanged since round %d — "+
								"its contents are already in your transcript above. Do not read it again "+
								"unless it changes.", sess.GetRound())
						}
						sess.NoteFileKnown(rd.Path, mt)
					}
				}
			}
		}

		// A file the model just wrote or edited is one it now knows the contents of.
		// Count it as read so a follow-up edit this turn is not refused by the
		// read-before-write guardrail — the model created that content, it is not
		// working from an assumption.
		if !isError {
			switch call.Name {
			case "write_file", "edit_file", "multi_edit":
				var w struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(call.Input, &w) == nil && w.Path != "" {
					sess.MarkFileRead(w.Path)
					if mt, ok := fileMtime(sess.WorkspacePath, w.Path); ok {
						sess.NoteFileKnown(w.Path, mt)
					}
					// Two rewrites are iteration; the third is thrash. Stop and
					// re-read the errors instead of writing a fourth version blind.
					if n := sess.NoteFileWrite(w.Path); n == rewriteWatchThreshold {
						watchNote = fmt.Sprintf("\n\nNote: this is your %drd write to %s this turn. "+
							"Stop writing it. Re-read the failing errors and the relevant files first, "+
							"then make one deliberate fix.", n, w.Path)
					}
				}
			}
		}

		// A skill the model reads for itself counts as delivered: auto-attach must
		// not then inject the same body again when a trigger fires this turn.
		if call.Name == "read_skill" && !isError {
			var rs struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(call.Input, &rs) == nil && rs.Name != "" {
				sess.MarkSkillSeen(rs.Name)
			}
		}

		// A recorded decision — an architecture decision (write_adr) or a durable
		// memory (write_memory) — surfaces as its own card so the user sees the
		// choice the agent just committed to, not a bare "wrote memory" row that
		// carries none of the content. Emit only on success (a card stands for a
		// decision on record) and mirror the fields the memory panel shows, so the
		// same record reads the same way inline and in the panel.
		if !isError {
			switch call.Name {
			case "write_adr":
				var a struct {
					Decision string `json:"decision"`
					Outcome  string `json:"outcome"`
				}
				if json.Unmarshal(call.Input, &a) == nil && a.Decision != "" {
					sess.Emitter.EmitDecision(events.Decision{
						Variant:  "adr",
						Decision: a.Decision,
						Outcome:  a.Outcome,
					})
				}
			case "write_memory":
				var m struct {
					Type     string `json:"type"`
					Content  string `json:"content"`
					Metadata string `json:"metadata"`
				}
				if json.Unmarshal(call.Input, &m) == nil && m.Content != "" {
					category := m.Type
					if category == "" {
						category = "fact" // matches WriteMemory.Execute's default
					}
					sess.Emitter.EmitDecision(events.Decision{
						Variant:  "memory",
						Category: category,
						Content:  m.Content,
						Metadata: m.Metadata,
					})
				}
			}
		}

		// Third-party API discovery is legitimate exactly once per question: the
		// third inspection of the same package means the shape is confirmed and
		// should be recorded (write_memory) instead of re-probed.
		if !isError {
			switch call.Name {
			case "run_shell", "grep_codebase", "read_file":
				if target := probeTarget(call.Name, call.Input); target != "" {
					if n := sess.NoteProbe(target); n == probeConsolidateThreshold {
						probeNote = fmt.Sprintf("\n\nNote: this is your %drd look at %s this turn. "+
							"You have confirmed what you came for — record the API shape with write_memory "+
							"and proceed to write code instead of probing it again.", n, target)
					}
				}
			}
		}

		results[i] = ai.Block{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   output + readNote + watchNote + probeNote,
			IsError:   isError,
		}
	}

	if parallel {
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call ai.ToolUseBlock) {
				defer wg.Done()
				run(i, call)
			}(i, call)
		}
		wg.Wait()
	} else {
		for i, call := range calls {
			run(i, call)
		}
	}

	// Every tool_use must come back with a matching tool_result, or the next
	// request is rejected with a 400. Backfill any gap an unforeseen path left.
	backfillMissingResults(results, calls)

	// Add the conflict note last, so it rides on whatever the call produced and
	// the model sees it whether the call succeeded or failed. Backfill first: a
	// gap that became a synthetic error result still gets the note.
	if len(conflicts) > 0 {
		for i, path := range conflicts {
			results[i].Content += fmt.Sprintf(
				"\n\nNote: you sent multiple write/delete calls to %s in this same batch. They ran in the order you sent them, so the last one is what is on disk now — read the file if that is not what you intended.",
				path)
		}
	}

	return results
}

// probeTarget extracts the third-party package under inspection from a tool
// call, or "" when the call inspects workspace code. node_modules/<pkg> (with
// an optional @scope) is the unit: repeated looks at one package answer one
// question, so the counter keys on the package, not the exact command.
func probeTarget(toolName string, input json.RawMessage) string {
	var text string
	switch toolName {
	case "run_shell":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &a) != nil {
			return ""
		}
		text = a.Command
	case "grep_codebase":
		var a struct {
			Pattern string `json:"pattern"`
			Glob    string `json:"glob"`
		}
		if json.Unmarshal(input, &a) != nil {
			return ""
		}
		text = a.Pattern + " " + a.Glob
	case "read_file":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &a) != nil {
			return ""
		}
		text = a.Path
	default:
		return ""
	}
	idx := strings.Index(text, "node_modules/")
	if idx < 0 {
		return ""
	}
	rest := text[idx+len("node_modules/"):]
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	if strings.HasPrefix(parts[0], "@") && len(parts) > 1 {
		return parts[0] + "/" + strings.TrimRight(parts[1], "\"'`")
	}
	return strings.TrimRight(parts[0], "\"'`")
}

// fileMtime stats a workspace-relative path and reports its mtime. Used to
// tell whether a file changed since the model last saw it; a missing file is
// not an error here, just "unknown".
func fileMtime(workspaceRoot, rel string) (int64, bool) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return 0, false
	}
	fi, err := os.Stat(filepath.Join(workspaceRoot, clean))
	if err != nil || fi.IsDir() {
		return 0, false
	}
	return fi.ModTime().Unix(), true
}

// batchConflicts finds write/delete calls in one batch that touch the same file
// path, keyed by call index → path. Reads never conflict — read-before-write is
// the order the loop wants — so read_file is ignored entirely. Every call
// involved in a repeated path is keyed, not just the repeats, so the note lands
// on all of them. Execution order is left untouched; this is a lens on the
// batch, not a reordering.
func batchConflicts(calls []ai.ToolUseBlock) map[int]string {
	first := map[string]int{} // path -> index of the first call that wrote it
	conflicts := map[int]string{}
	for i, call := range calls {
		switch call.Name {
		case "write_file", "edit_file", "multi_edit", "delete_file":
		default:
			continue
		}
		var w struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Input, &w) != nil || w.Path == "" {
			continue
		}
		if prev, ok := first[w.Path]; ok {
			conflicts[prev] = w.Path
			conflicts[i] = w.Path
		} else {
			first[w.Path] = i
		}
	}
	return conflicts
}

// backfillMissingResults fills any gap in a pre-sized results slice — a zero-value
// Block whose empty Type both serializers drop silently, which orphans its
// tool_use and gets the next request rejected with a 400. A gap should not occur
// now that run() recovers from panics and every branch assigns a result, so this
// is belt-and-suspenders against a future path that returns before assignment:
// each gap becomes an error tool_result carrying its call's ID, and a result a run
// already set is left untouched.
func backfillMissingResults(results []ai.Block, calls []ai.ToolUseBlock) {
	for i := range results {
		if results[i].Type == "" {
			results[i] = ai.Block{
				Type:      "tool_result",
				ToolUseID: calls[i].ID,
				Content:   "This tool produced no result. Treat it as failed and try again or take a different approach.",
				IsError:   true,
			}
		}
	}
}

// guardState adapts a session to the guardrails.State interface: read tracking
// comes from the session, existence from the workspace filesystem via RunOptions.
type guardState struct {
	sess   *SessionState
	exists func(string) bool
}

func (g guardState) HasReadFile(path string) bool { return g.sess.HasReadFile(path) }
func (g guardState) FileExists(path string) bool {
	return g.exists != nil && g.exists(path)
}

// guardrail evaluates one tool call against the deterministic guardrails before
// it runs. It is the single place a call can be refused for policy: the rules
// tell the model what not to do, and this makes the security-critical ones hold
// even when the model does not comply.
func (o *Orchestrator) guardrail(sess *SessionState, name string, input json.RawMessage, opts RunOptions) guardrails.Decision {
	st := guardState{sess: sess, exists: opts.Exists}
	return guardrails.Check(name, input, st, o.preinstalled)
}

// autoAttachSkills injects the bodies of any auto-attach skills whose triggers
// fired on the tool calls just made — each at most once per session — and returns
// the text to append as a follow-up user turn (or "" if nothing new applies).
// Handing the model the current, version-pinned guidance the moment it acts in a
// skill's area is what holds quality up on models that would not otherwise know
// the exact API shipped here.
func (o *Orchestrator) autoAttachSkills(sess *SessionState, calls []ai.ToolUseBlock) string {
	if o.knowledge == nil {
		return ""
	}
	var bodies []string
	for _, call := range calls {
		for _, s := range o.knowledge.AutoAttachFor(call.Name, call.Input) {
			if !sess.MarkSkillSeen(s.Name) {
				continue // already delivered this session
			}
			bodies = append(bodies, s.Body)
			// Show it in the trace: an auto-attached skill is a real thing that
			// happened, and the timeline should say so rather than inject silently.
			sess.Emitter.EmitToolCall("", "read_skill", "", "Applied the "+s.Name+" skill", "completed")
		}
	}
	if len(bodies) == 0 {
		return ""
	}
	return "A project skill applies to what you just did. Follow it for the rest of this work:\n\n" +
		strings.Join(bodies, "\n\n---\n\n")
}

type verifyVerdict struct {
	ok       bool
	feedback string
}

// verifyResult mirrors the JSON every verification tool returns. The orchestrator
// speaks to tools only through the executor and JSON, so it re-declares the shape
// here rather than importing the tools package — the contract is the interface.
type verifyResult struct {
	Passed  bool   `json:"passed"`
	Errors  string `json:"errors"`
	Summary string `json:"summary"`
}

// verifyGate is one rung of the verification ladder: a named static check and the
// repair guidance to hand back when it fails. Gates run in order and the ladder
// stops at the first real failure — there is no point linting code that does not
// compile, and one focused class of problem is easier to fix than a pile of mixed
// ones.
type verifyGate struct {
	stage    string
	tool     string
	feedback func(verifyResult) string
}

// verifyGates is the ladder, ordered most-fundamental first: compilation before
// lint. It deliberately stops short of the APK build — that needs a device and
// minutes, and runs as its own explicit step, not on every repair iteration.
func (o *Orchestrator) verifyGates() []verifyGate {
	return []verifyGate{
		{stage: "typecheck", tool: "run_typecheck", feedback: typecheckFeedback},
		{stage: "lint", tool: "run_lint", feedback: lintFeedback},
	}
}

// verify walks the gates in order and returns the first failure as feedback the
// agent can act on, or ok once every gate is satisfied (or unavailable).
func (o *Orchestrator) verify(ctx context.Context, sess *SessionState, opts RunOptions) verifyVerdict {
	for _, g := range o.verifyGates() {
		if v := o.runGate(ctx, sess, opts, g); !v.ok {
			return v
		}
	}
	return verifyVerdict{ok: true}
}

// runGate runs one gate and classifies its outcome three ways: pass, unavailable,
// and fail — pass and unavailable both let the ladder continue (ok), while a real
// failure stops it with feedback. A gate whose tool could not run, or whose output
// does not parse, is unavailable rather than failed — a missing or broken checker
// is a platform problem, and blocking a build on it would punish the agent for
// code that may be perfectly correct. The distinction is surfaced, not swallowed:
// a quiet "skip" hid a broken checker as if nothing was wrong, so the event now
// carries a distinct "unavailable" status with the reason, and a log line records
// the infra break. It parses the JSON verdict rather than treating non-empty
// output as failure, since these tools print banners on success too.
func (o *Orchestrator) runGate(ctx context.Context, sess *SessionState, opts RunOptions, g verifyGate) verifyVerdict {
	sess.Emitter.EmitVerify(g.stage, "running", "")
	raw, isError := opts.Execute(ctx, g.tool, json.RawMessage(`{}`))

	if isError {
		reason := firstLine(raw)
		if reason == "" {
			reason = g.stage + " tool failed to run"
		}
		log.Printf("orchestrator: %s check unavailable (%s); skipping so the build is not blocked by checker breakage", g.stage, reason)
		sess.Emitter.EmitVerify(g.stage, "unavailable", reason)
		return verifyVerdict{ok: true}
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(raw), &vr); err != nil {
		log.Printf("orchestrator: %s check output unreadable (%v); skipping so the build is not blocked by checker breakage", g.stage, err)
		sess.Emitter.EmitVerify(g.stage, "unavailable", "unreadable "+g.stage+" output")
		return verifyVerdict{ok: true}
	}

	if vr.Passed {
		sess.Emitter.EmitVerify(g.stage, "pass", vr.Summary)
		return verifyVerdict{ok: true}
	}

	sess.Emitter.EmitVerify(g.stage, "fail", vr.Summary)
	return verifyVerdict{feedback: g.feedback(vr)}
}

// typecheckFeedback turns a failed tsc verdict into repair guidance, naming the
// files the compiler flagged so the agent knows where to read first.
func typecheckFeedback(vr verifyResult) string {
	var b strings.Builder
	b.WriteString("The code you wrote does not compile. TypeScript reports:\n\n")
	b.WriteString(vr.Errors)
	b.WriteString("\n\nFix these errors. Read each file the compiler names before changing it, and use " +
		"edit_file for targeted changes. When you believe it is fixed, call run_typecheck to confirm.")

	if files := errorFiles(vr.Errors); len(files) > 0 {
		b.WriteString("\n\nFiles with errors: " + strings.Join(files, ", "))
	}
	b.WriteString("\n\nFix everything in as few edits as you can, then verify once — " +
		"do not alternate one edit with one typecheck.")
	return b.String()
}

// lintFeedback turns a failed ESLint verdict into repair guidance. The framing
// matters: the code already compiles, so these are the defects the type system
// does not catch — unused bindings, missing effect dependencies, unreachable
// code — worth fixing rather than silencing.
func lintFeedback(vr verifyResult) string {
	var b strings.Builder
	b.WriteString("The code compiles, but ESLint reports problems:\n\n")
	b.WriteString(vr.Errors)
	b.WriteString("\n\nFix these — they flag real defects the compiler cannot see, not just style. " +
		"Read each file the linter names before changing it. Do not silence a rule with a disable " +
		"comment unless it is genuinely a false positive. When you believe it is fixed, call run_lint " +
		"to confirm. Fix everything in as few edits as you can, then verify once.")
	return b.String()
}

// errorFiles pulls the distinct file paths out of tsc output, preserving the
// relative path — the directory is what makes it findable.
func errorFiles(tscOutput string) []string {
	seen := make(map[string]bool)
	var files []string
	for _, line := range strings.Split(tscOutput, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, "(")
		if idx <= 0 {
			continue
		}
		path := line[:idx]
		// A tsc location looks like path(line,col): error TS1234. Anything
		// without that shape is prose.
		if !strings.Contains(line[idx:], "): error TS") {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
		if len(files) >= 10 {
			break
		}
	}
	return files
}

// enrichPromptWithContext appends architecture decisions and project memory to
// the system prompt. Both are bounded: the system prompt is the cached prefix of
// every request, and letting it grow without limit costs on every round.
func (o *Orchestrator) enrichPromptWithContext(sess *SessionState, systemPrompt string) string {
	const (
		maxADRs      = 12
		maxMemory    = 20
		maxMemoryLen = 240
	)

	var b strings.Builder
	b.WriteString(systemPrompt)

	// Always-on rules and the skill manifest come first: they are identical across
	// every session, so keeping them adjacent to the base prompt keeps the cached
	// prefix long. The per-project ADRs and memory below vary and follow after.
	if o.knowledge != nil {
		if rules := o.knowledge.RulesBlock(); rules != "" {
			b.WriteString("\n\n")
			b.WriteString(rules)
		}
		if manifest := o.knowledge.Manifest(); manifest != "" {
			b.WriteString("\n\n")
			b.WriteString(manifest)
		}
	}

	// The working-discipline block is model-specific but session-invariant, so it
	// belongs here in the cached prefix alongside the rules — before the per-project
	// ADRs and memory that vary below.
	if o.discipline != "" {
		b.WriteString("\n\n")
		b.WriteString(o.discipline)
	}

	// The project map snapshot (files, their exports, their imports) refreshes
	// itself when the tree moves and stays byte-stable when it does not, so it
	// sits in the stable prefix: a build that grounds the model this way does
	// not re-derive the layout round after round, which is where the largest
	// token waste came from.
	if pm := sess.ProjectMap(); pm != "" {
		b.WriteString("\n\n")
		b.WriteString(pm)
	}

	// The plan checklist is the settled screens and features from Plan mode. Its
	// presence every round is the whole point: the model tracks progress against a
	// fixed plan instead of re-deriving one, which is what burned ~966k tokens on a
	// build that was then discarded. It varies only when a task's status flips —
	// the rebuild guard keys on TaskVersion so the cached prefix survives the rounds
	// in between.
	if tasks := sess.GetTasks(); len(tasks) > 0 {
		b.WriteString("\n\n")
		b.WriteString(renderPlanChecklist(tasks))
	}

	if adrs := sess.GetADRs(); len(adrs) > 0 {
		if len(adrs) > maxADRs {
			adrs = adrs[len(adrs)-maxADRs:]
		}
		b.WriteString("\n\n## Architecture decisions already made\n\n")
		for _, adr := range adrs {
			fmt.Fprintf(&b, "- **%s** — %s\n", adr.Decision, adr.Outcome)
			if adr.Context != "" {
				fmt.Fprintf(&b, "  Why: %s\n", truncateLine(adr.Context, maxMemoryLen))
			}
		}
		b.WriteString("\nThese are settled. Build within them. If a request genuinely conflicts with one, " +
			"say so and propose how to reconcile it rather than quietly introducing a second pattern.\n")
	}

	if memory := sess.GetMemory(); len(memory) > 0 {
		if len(memory) > maxMemory {
			memory = memory[len(memory)-maxMemory:]
		}
		b.WriteString("\n## What exists in this project\n\n")
		for _, m := range memory {
			fmt.Fprintf(&b, "- [%s] %s\n", m.EntryType, truncateLine(m.Content, maxMemoryLen))
		}
	}

	return b.String()
}

// renderPlanChecklist formats the seeded plan tasks as a done/remaining checklist
// for the system prompt. Completed items are checked so the model sees what is left
// without re-listing the whole plan itself; a failed item is flagged so it is not
// mistaken for finished.
func renderPlanChecklist(tasks []Task) string {
	var b strings.Builder
	b.WriteString("## Build plan — track progress, do not re-derive\n\n")
	b.WriteString("These screens and features were settled with the user. Work through them; " +
		"check what is done against this list rather than re-planning. Items marked done are done.\n\n")
	for _, t := range tasks {
		mark := "[ ]"
		suffix := ""
		switch t.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			suffix = " (in progress)"
		case "failed":
			mark = "[!]"
			suffix = " (blocked — needs another pass)"
		}
		fmt.Fprintf(&b, "- %s %s%s\n", mark, t.Description, suffix)
	}
	return b.String()
}

func truncateLine(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return truncateLine(s, 200)
}

// describeTool renders a tool call as something a person can read in the
// timeline, e.g. "Updating app/index.tsx" rather than "write_file".
func describeTool(name string, input json.RawMessage, workspacePath string) string {
	pathOf := func() string {
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &a) == nil {
			return a.Path
		}
		return ""
	}

	switch name {
	case "read_file":
		if p := pathOf(); p != "" {
			return "Reading " + p
		}
		return "Reading a file"
	case "write_file":
		p := pathOf()
		if p == "" {
			return "Writing a file"
		}
		if _, err := os.Stat(filepath.Join(workspacePath, p)); err == nil {
			return "Rewriting " + p
		}
		return "Creating " + p
	case "edit_file", "multi_edit":
		if p := pathOf(); p != "" {
			return "Editing " + p
		}
		return "Editing a file"
	case "delete_file":
		if p := pathOf(); p != "" {
			return "Deleting " + p
		}
		return "Deleting a file"
	case "list_files":
		return "Listing project files"
	case "grep_codebase":
		var a struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal(input, &a) == nil && a.Pattern != "" {
			return "Searching for " + truncateLine(a.Pattern, 40)
		}
		return "Searching the codebase"
	case "install_package":
		var a struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(input, &a) == nil && a.Name != "" {
			return "Installing " + a.Name
		}
		return "Installing a package"
	case "run_typecheck":
		return "Checking types"
	case "run_lint":
		return "Linting"
	case "run_checks":
		return "Verifying types and lint"
	case "build_apk":
		return "Building the app"
	case "run_command":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &a) == nil && a.Command != "" {
			return "Running " + truncateLine(a.Command, 60)
		}
		return "Running a command"
	case "run_shell":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &a) == nil && a.Command != "" {
			return "Probing " + truncateLine(a.Command, 60)
		}
		return "Probing the system"
	case "capture_screenshot":
		return "Looking at the screen"
	case "tail_logcat":
		return "Reading device logs"
	case "test_wallet_roundtrip":
		return "Testing the wallet connection"
	case "write_adr":
		return "Recording a decision"
	case "write_memory":
		return "Noting project context"
	case "read_skill":
		var a struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(input, &a) == nil && a.Name != "" {
			return "Consulting the " + a.Name + " skill"
		}
		return "Consulting a skill"
	case "web_search":
		var a struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(input, &a) == nil && a.Query != "" {
			return "Searching the web for " + truncateLine(a.Query, 40)
		}
		return "Searching the web"
	default:
		return name
	}
}

// toolTarget extracts the single most relevant argument of a tool call — the
// file path, command, search pattern or package name — as a bare string for the
// UI to show beside the verb. describeTool folds this same value into a prose
// sentence for the label; toolTarget returns it raw so the timeline can render
// "Edited · app/index.tsx" rather than "edit file · Editing app/index.tsx".
// Tools whose action has no such argument (a typecheck, a screenshot) and the
// pseudo-tools whose whole description lives in the verb phrase return "".
func toolTarget(name string, input json.RawMessage) string {
	field := func(key string) string {
		var m map[string]json.RawMessage
		if json.Unmarshal(input, &m) != nil {
			return ""
		}
		raw, ok := m[key]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	switch name {
	case "read_file", "write_file", "edit_file", "multi_edit", "delete_file", "list_files":
		return field("path")
	case "grep_codebase":
		return truncateLine(field("pattern"), 60)
	case "run_command":
		return truncateLine(field("command"), 80)
	case "install_package":
		return field("name")
	case "web_search", "github_search":
		return truncateLine(field("query"), 60)
	default:
		return ""
	}
}

// taskIDForTool maps long-running tools onto the task list the UI shows.
func taskIDForTool(name string) string {
	switch name {
	case "build_apk":
		return "build-apk"
	case "run_typecheck", "run_lint":
		return "verify"
	case "install_package":
		return "install-deps"
	case "capture_screenshot", "tail_logcat":
		return "runtime-check"
	case "test_wallet_roundtrip":
		return "wallet-test"
	default:
		return ""
	}
}

// refreshProjectMap rebuilds the injected structure map when the workspace tree
// moved since the map was captured, and reports whether it changed. The
// fingerprint walk is milliseconds against source files (dependencies are
// skipped like the map itself skips them); the rebuild runs only on real
// change, so quiet rounds keep the cached prompt prefix intact.
func (o *Orchestrator) refreshProjectMap(sess *SessionState) bool {
	root := sess.WorkspacePath
	if root == "" {
		return false
	}
	fp := tools.ProjectFingerprint(root)
	if fp == sess.ProjectMapFP() {
		return false
	}
	if m := tools.ProjectMapText(root); m != "" {
		sess.SetProjectMapFP(m, fp)
		return true
	}
	return false
}

// pendingPlanTasks names the seeded plan items not yet completed. Empty both
// when the plan is fully built and when there was never a plan. Always
// non-nil so the API serializes [] rather than null.
func pendingPlanTasks(sess *SessionState) []string {
	screens, features := pendingPlanTasksSplit(sess)
	return append(screens, features...)
}

// pendingPlanTasksSplit separates unfinished screens from unfinished features.
// Screens are the hard signal — each maps to a route file that either exists
// or does not. Features are soft: nothing auto-marks them, so an unmarked
// feature after a verified build means "unconfirmed", not "missing".
func pendingPlanTasksSplit(sess *SessionState) (screens, features []string) {
	screens = []string{}
	features = []string{}
	for _, t := range sess.GetTasks() {
		if t.Status == "completed" {
			continue
		}
		if strings.HasPrefix(t.ID, "plan:screen:") {
			screens = append(screens, t.Description)
		} else {
			features = append(features, t.Description)
		}
	}
	return screens, features
}

// remainingNote renders the honest-partial footnote for a turn that verified
// but did not finish the plan. Screens remaining are stated as incomplete;
// features remaining are stated as unconfirmed, since the agent may have built
// them without checking them off.
func remainingNote(sess *SessionState, verb string) string {
	screens, features := pendingPlanTasksSplit(sess)
	if len(screens) == 0 && len(features) == 0 {
		return ""
	}
	var b strings.Builder
	if len(screens) > 0 {
		fmt.Fprintf(&b, "\n\n%s with %d of %d plan screens still unbuilt: %s.",
			verb, len(screens), countPlanScreens(sess), strings.Join(screens, "; "))
	}
	if len(features) > 0 {
		fmt.Fprintf(&b, "\n\nUnconfirmed features (%d) — built but never checked off, or skipped: %s.",
			len(features), strings.Join(features, "; "))
	}
	return b.String()
}

// countPlanScreens counts seeded screen tasks regardless of status.
func countPlanScreens(sess *SessionState) int {
	n := 0
	for _, t := range sess.GetTasks() {
		if strings.HasPrefix(t.ID, "plan:screen:") {
			n++
		}
	}
	return n
}

// WriteADR records an architecture decision for the project.
func (o *Orchestrator) WriteADR(projectID int64, decision, context, options, outcome string) error {
	_, err := o.store.CreateADR(projectID, decision, context, options, outcome)
	return err
}

// recordTurnMemory files one memory row for a verified turn — what changed and
// the agent's own summary — so the next turn starts with real project context
// instead of reconstructing it from the file tree. It writes directly rather
// than spending a model round on write_memory, and it never fails the turn:
// a memory write is bookkeeping, not the build.
func (o *Orchestrator) recordTurnMemory(sess *SessionState, touched []string, outcome *BuildOutcome) {
	if len(touched) == 0 {
		return
	}
	summary := strings.TrimSpace(outcome.FinalText)
	if len(summary) > 500 {
		summary = summary[:500] + "…"
	}
	content := "Changed: " + strings.Join(touched, ", ")
	if summary != "" {
		content += "\n" + summary
	}
	if err := o.store.AddMemory(sess.ProjectID, "build-note", content, ""); err != nil {
		log.Printf("project %d: could not record turn memory: %v", sess.ProjectID, err)
	}
}

// WriteMemory adds an entry to the project's memory.
func (o *Orchestrator) WriteMemory(projectID int64, entryType, content, metadata string) error {
	return o.store.AddMemory(projectID, entryType, content, metadata)
}
