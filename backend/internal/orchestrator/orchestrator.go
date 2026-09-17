package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/build"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/guardrails"
	harnessagent "github.com/luckyisrael/talon/backend/internal/harness/agent"
	"github.com/luckyisrael/talon/backend/internal/harness/compaction"
	harnessevents "github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/loop"
	"github.com/luckyisrael/talon/backend/internal/harness/pipeline"
	"github.com/luckyisrael/talon/backend/internal/harness/promptsec"
	"github.com/luckyisrael/talon/backend/internal/harness/skills"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/metrics"
	"github.com/luckyisrael/talon/backend/internal/runtime"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// MaxContextTokens is when the conversation gets compacted. Sized to leave room
// for the system prompt, tool definitions and a full response. Exported so the
// token meter reads the same window the compaction decision uses — an occupancy
// bar and the compactor must agree on what "full" means.
const MaxContextTokens = 120_000

type Orchestrator struct {
	ai *ai.Client

	mu       sync.RWMutex
	sessions map[int64]*SessionState

	store     *store.Store
	knowledge *knowledge.Registry
	// preinstalled is the set the install guardrail checks against. Computed once
	// from the knowledge layer at construction so every tool call does not rebuild
	// it.
	preinstalled map[string]bool
	// discipline is the tier-specific working-discipline block appended to the
	// build system prompt, derived once from the model's capabilities. Empty for a
	// strong model (which needs none) and when there is no AI client.
	discipline string
	maxRounds  int
	// agents is the H2 live agent registry: one handle per running turn. The
	// SSE steer route looks the turn up here and delivers mid-turn messages
	// through the session's steer lane; the loop drains the lane at every
	// round boundary. Publish is rollback-covered: CreateSession registers
	// only after the session is fully initialized, RemoveSession unpublishes.
	agents *harnessagent.Registry
	// spill is the H8 oversized-output store. Nil until the composition
	// provides a directory; nil means outputs keep today's 32KB hard cap.
	spill *compaction.SpillStore
	// skills is the H6 multi-source skill catalog. Nil keeps the embedded-only
	// manifest from knowledge.Registry; non-nil, the manifest lists workspace
	// skills too (bodies still load only via read_skill).
	skills *skills.Catalog
	// instructionsFor is the H6 AGENTS.md chain factory: workspace path →
	// chain text. Nil disables the chain; non-nil, its text is appended to
	// the cached prompt prefix and re-read at each enrichment (the
	// reconciliation-on-touch discipline — no watcher). A factory instead of
	// a single loader because workspaces are per-project.
	instructionsChain func(workspacePath string) string
	// verifyReport is the chain report accumulator the run_gate tool reads. It is
	// reset at the start of every verify call and populated as each gate runs.
	// Atomic.Pointer so run_gate can read it without holding the mutex.
	verifyReport atomic.Pointer[tools.VerifyReport]
}

// maxRambleRounds caps consecutive tool-less max_tokens rounds before the
// turn ends as stalled. One truncation can be a long file in prose form, two
// is suspicious, three with zero tool calls is a ramble loop — each such
// round burns ~35K tokens and minutes for zero progress.
const maxRambleRounds = 3

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

func New(aiClient *ai.Client, s *store.Store, knowledge *knowledge.Registry) *Orchestrator {
	o := &Orchestrator{
		ai:           aiClient,
		sessions:     make(map[int64]*SessionState),
		store:        s,
		knowledge:    knowledge,
		preinstalled: knowledge.PreinstalledSet(),
		maxRounds:    maxRoundsFromEnv(),
		agents:       harnessagent.NewRegistry(),
	}
	o.verifyReport.Store(&tools.VerifyReport{OverallPassed: true})
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

	// Durable backlog mirror: every MarkPlanTask write-through lands in
	// plan_task_status keyed by this session's prompt id (attribution).
	sess.AttachTaskPersistence(func(pid int64, t Task, byPrompt int64) {
		if err := o.store.UpsertPlanTask(pid, store.PlanTask{
			TaskID: t.ID, Description: t.Description, Status: t.Status, Result: t.Result, CompletedByPrompt: byPrompt,
		}); err != nil {
			log.Printf("orchestrator: could not persist plan task %s (project %d): %v", t.ID, pid, err)
		}
	})

	o.mu.Lock()
	o.sessions[id] = sess
	o.mu.Unlock()

	// Publish the H2 agent handle after the session is fully initialized —
	// a steer that arrives before loadProjectContext finishes still lands in
	// the steer lane, which the loop drains at its first round boundary.
	// Publishing last is what makes setup failure never leak a phantom agent.
	o.agents.Publish(harnessagent.Handle(id, id, sess))

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
	// Load runtime failure patterns from memory — these augment the default
	// seed and are recorded by the model via record_failure_pattern.
	if mem, err := o.store.GetAllMemory(projectID); err == nil {
		for _, m := range mem {
			if m.EntryType == "failure_pattern" {
				parts := parseFailurePattern(m.Content)
				if parts.match != "" {
					re, err := regexp.Compile(parts.match)
					if err != nil {
						continue
					}
					sess.RuntimePatterns = append(sess.RuntimePatterns, knowledge.FailurePattern{
						Tool:           parts.tool,
						Match:          re,
						Cause:          parts.cause,
						Fix:            parts.fix,
						Skill:          m.Metadata,
						CooldownRounds: 3,
					})
				}
			}
		}
	}
	o.seedPlanTasks(sess, projectID)
}

type fpParts struct {
	tool, match, cause, fix string
}

func parseFailurePattern(content string) fpParts {
	var p fpParts
	for _, field := range strings.Split(content, " | ") {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "tool":
			p.tool = kv[1]
		case "match":
			p.match = kv[1]
		case "cause":
			p.cause = kv[1]
		case "fix":
			p.fix = kv[1]
		}
	}
	return p
}

// IsDeviceStep is isDeviceStep exported for the phase guard — the
// device-step set is a phase contract (crash-index membership).
func IsDeviceStep(tool string) bool { return isDeviceStep(tool) }

// isDeviceStep reports whether a failed tool call belongs in the run-scoped
// crash index: the gates and tools whose failures mean something on the
// device went wrong, as opposed to a code error.
func isDeviceStep(tool string) bool {
	switch tool {
	case "run_boot", "run_vision", "run_interact", "test_wallet_roundtrip",
		"capture_screenshot", "tail_logcat", "run_e2e":
		return true
	}
	return false
}

// crashTx names the transaction a failed device step belonged to, so the
// vision screenshot and the logcat delta for one wallet tx share one crash
// entry. Falls back to the intended screen, then to no name.
func crashTx(input json.RawMessage) string {
	var a struct {
		Tx             string `json:"tx"`
		IntendedScreen string `json:"intended_screen"`
	}
	if json.Unmarshal(input, &a) != nil {
		return ""
	}
	if a.Tx != "" {
		return a.Tx
	}
	return a.IntendedScreen
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
	// The durable backlog is the seed: statuses recorded by earlier turns
	// (and survived restarts) load as-is, so resume sees what is genuinely
	// done instead of re-deriving — or redoing — it.
	persisted := map[string]store.PlanTask{}
	if rows, err := o.store.LoadPlanTasks(projectID); err == nil {
		for _, r := range rows {
			persisted[r.TaskID] = r
		}
	}
	var liveIDs []string
	seed := func(id, desc string) {
		liveIDs = append(liveIDs, id)
		// Persist the seed row so the table holds the full backlog — not
		// only terminal states — and SupersedeMissingPlanTasks can see drift.
		// A row already present keeps its status (the conflict update only
		// refreshes the description).
		if o.store != nil {
			_ = o.store.UpsertPlanTask(projectID, store.PlanTask{TaskID: id, Description: desc, Status: "pending"})
		}
		sess.AddTask(id, desc)
		if p, ok := persisted[id]; ok && p.Status != "superseded" {
			// Restore the recorded state without re-emitting (EmitPlanSeed
			// delivers the full list after seeding completes).
			_ = sess.TaskTracker.UpdateTask(id, p.Status, p.Result)
		}
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
		seed("plan:screen:"+planSlug(screenBaseName(name)), desc)
	}
	for i, ft := range plan.Features {
		ft = strings.TrimSpace(ft)
		if ft == "" {
			continue
		}
		seed(fmt.Sprintf("plan:feature:%d", i), "Feature: "+ft)
	}
	// A plan edit that dropped work leaves stale rows: mark them superseded
	// (kept for the audit trail), never deleted.
	if n, err := o.store.SupersedeMissingPlanTasks(projectID, liveIDs); err == nil && n > 0 {
		log.Printf("orchestrator: %d plan task(s) superseded by a plan edit (project %d)", n, projectID)
	}
}

// planSlug reduces a screen name to a stable, id-safe token: lowercase, with any
// run of non-alphanumeric characters collapsed to a single dash. It only has to be
// stable within one session (task ids are matched against it when marking done),
// not globally unique.
// deviceBuildRe matches user words that ask for a device build: apk, device,
// preview, emulator, install, release, deploy. A bare "build" deliberately
// does not match — in prompts it almost always means "build the screen".
var deviceBuildRe = regexp.MustCompile(`(?i)\b(apk|device|preview|emulator|install|release|deploy|on[ -]?device|physical)\b`)

// WantsDeviceBuild reports whether the user's prompt asks for a device build.
// Only the user's words count — model plans never unlock gradle. Exported so
// the Answer path can grant the same unlock for approval replies, which arrive
// as tool_result turns and are invisible to text scanning.
func WantsDeviceBuild(prompt string) bool {
	return deviceBuildRe.MatchString(prompt)
}

// lastUserText returns the most recent user turn's text, or "" when the
// history opens otherwise (defensive; callers pass real conversations).
func lastUserText(conversation []ai.Message) string {
	for i := len(conversation) - 1; i >= 0; i-- {
		if conversation[i].Role == "user" {
			return conversation[i].Text()
		}
	}
	return ""
}

// retryClass folds a retry loop's reason string into a stable failure class for
// the audit record. The exact status is useful detail; the class is what an
// operator groups over ("how often did the gateway rate-limit us this week").
func retryClass(why string) string {
	switch {
	case strings.Contains(why, "rate limit"):
		return "rate_limited"
	case strings.Contains(why, "server error"):
		return "server_error"
	case strings.Contains(why, "empty"):
		return "empty_response"
	case strings.Contains(why, "stream"):
		return "stream_error"
	case strings.Contains(why, "timeout") || strings.Contains(why, "deadline"):
		return "timeout"
	case strings.Contains(why, "transient"):
		return "transient"
	}
	return "other"
}

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

// auditScreenTasks is markScreenTasksFromFiles in reverse: for every screen
// task marked completed (by the model or the heuristic), confirm a route file
// with a matching slug actually exists in the workspace. A completed screen
// with no route file was checked off without being built — the false
// completion the E2E caught — so it flips back to pending with the reason
// recorded, and lands in remaining_tasks instead of vanishing. Runs only on
// the verified-success path, so existence means exists-and-compiles.
// Slugs that cannot map to a file (home, index) are skipped: the audit
// under-claims before it lies, same as the marking heuristic.
func (o *Orchestrator) auditScreenTasks(sess *SessionState, opts RunOptions) {
	if opts.Exists == nil {
		return
	}
	for _, t := range sess.GetTasks() {
		if t.Status != "completed" {
			continue
		}
		slug := strings.TrimPrefix(t.ID, "plan:screen:")
		if slug == t.ID || slug == "" || slug == "home" || slug == "index" {
			continue // not a screen task, or unmappable — leave it alone
		}
		candidates := []string{
			"app/" + slug + ".tsx",
			"app/(tabs)/" + slug + ".tsx",
			"app/" + slug + "/index.tsx",
			"app/(tabs)/" + slug + "/index.tsx",
		}
		found := false
		for _, c := range candidates {
			if opts.Exists(c) {
				found = true
				break
			}
		}
		if !found {
			sess.MarkPlanTask(t.ID, t.Description, "pending",
				"route file missing — checked off without being built")
		}
	}
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
	// Unpublish the agent handle first: once removal starts, a steer must
	// fail with "no running build" instead of vanishing into a dying session.
	o.agents.Unpublish(id)
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

// AttachSpill provides the H8 oversized-output store. Called once by the
// composition; nil (the default) keeps the hard-cap behavior.
func (o *Orchestrator) AttachSpill(s *compaction.SpillStore) {
	o.spill = s
}

// AttachSkills provides the H6 multi-source catalog; nil keeps the embedded
// manifest.
func (o *Orchestrator) AttachSkills(c *skills.Catalog) {
	o.skills = c
}

// SkillCatalog returns the attached catalog (nil when none) — the api layer
// resolves read_skill through it.
func (o *Orchestrator) SkillCatalog() *skills.Catalog { return o.skills }

// AttachInstructions provides the H6 AGENTS.md chain factory; nil disables
// the chain. The factory reads the workspace's chain fresh per call.
func (o *Orchestrator) AttachInstructions(f func(workspacePath string) string) {
	o.instructionsChain = f
}

// Agent returns the live H2 agent handle for a running prompt (nil when none
// is running). The steer route is the caller; it delivers mid-turn messages
// through the handle, which the loop drains at round boundaries.
func (o *Orchestrator) Agent(id int64) *harnessagent.Agent {
	return o.agents.Get(id)
}

// requestErrorListener is the default request-error policy (H2 seam): one
// retry on the routing policy's fallback model when one is configured and the
// turn has not used its fallback yet, latching on success; terminal otherwise.
// This is exactly the inline policy it replaced — extracted so a composition
// can swap it without editing the loop.
func (o *Orchestrator) requestErrorListener(round int, model string, err error, fellBack bool) loop.RequestErrorDecision {
	policy := ai.LoadRoutingPolicy()
	if fellBack || policy.FallbackModel == "" || policy.FallbackModel == model {
		return loop.Terminal("model request failed")
	}
	return loop.RetryOnModel(policy.FallbackModel, true,
		fmt.Sprintf("falling back to %s once", policy.FallbackModel))
}

// pipelineEnabled reports whether the H3 bounded tool pipeline drives batch
// dispatch. Ships dark: legacy (off) behaves identically to today, the flag
// opts in per deployment. Flip the default only after the same-script
// transcript-parity eval has run green on both modes.
func pipelineEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_PIPELINE"))) {
	case "v1", "1", "true", "on":
		return true
	}
	return false
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
	// SkippedGates names verify-ladder stages that reported "unavailable"
	// when the turn finished verified. The ladder is deliberately fail-open
	// (missing infra must not block a build), but "verified: true" with
	// silently skipped gates reads as "fully tested" — the E2E shipped a
	// stub-only app this way. Callers surface these so verified means what
	// it says: everything that could run, ran.
	SkippedGates []string
	// RemainingTasks names the seeded plan items still pending when the turn
	// ended. A turn can compile cleanly while the plan is half-built (the
	// foundation-first stop); callers surface these so "success" is never
	// mistaken for "everything is built". Empty means the plan is complete —
	// or there was no plan.
	RemainingTasks []string
	// RoutedModel is the model string that served the most recent round, and
	// RouteReason is why it was chosen. Recorded so cost attribution prices
	// the tokens actually spent, not the configured default.
	RoutedModel string
	RouteReason string
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
	// Stalled is set when the ramble breaker ended the turn: the model
	// produced maxRambleRounds consecutive full-length responses with zero
	// tool calls. Stalled is terminal (not resumable) and never verified —
	// the caller must surface why, not offer a resume.
	Stalled bool
}

// RunOptions configures one agentic turn.
type RunOptions struct {
	SystemPrompt string
	// Mode is "plan" or "build": routing classifies the turn from it.
	Mode string
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
	// PatchBufferEnabled opts one session into the try_edit lane: edits are
	// staged with pre-images and type-breaking changes auto-revert. Default
	// off — off behaves identically to today. The TALONSTUDIO_PATCH_BUFFER
	// env var (1/true/on) opts in without a code change.
	PatchBufferEnabled bool
	// OnCompacted, when set, is called at a compaction boundary with the
	// turn count before and after (H8): the caller (the api layer, which owns
	// the session log) shadows the log at the same boundary so derivation
	// and the wire stay honest with each other. Errors are the caller's to
	// log; the loop never blocks on them.
	OnCompacted func(before, after int)
	// EventLog, when set, is the durability boundary the checkpoint policy
	// enforces: the assistant's tool requests are flushed and forced to stable
	// storage before any tool body runs, and before each model request, so a
	// crash between recording intent and producing the effect cannot leave the
	// log claiming something that never happened. Nil keeps the loop quiet:
	// tests that do not care about crash fidelity pass nothing.
	EventLog AuditLog
}

// EventAppender is the write half of the audit sink.
type EventAppender interface {
	Append(kind harnessevents.Kind, payload any) (int64, error)
}

// AuditLog is the durable audit sink the checkpoint policy needs: Append
// records an event, Sync forces everything recorded onto stable storage so it
// survives a hard crash, not just a process exit. sessionlog.Log satisfies it;
// the interface keeps the orchestrator from importing the log package (and the
// store behind it).
//
// Sync is the fail-closed boundary: when it fails, the model call or tool that
// was about to run must not run — its intent is not yet durable.
type AuditLog interface {
	EventAppender
	Sync() error
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
	sess.ResetToolRepeats()

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

	// checkpoint is the fail-closed durability boundary (the DSH
	// session-checkpoint policy). It flushes the turns recorded so far — which
	// writes the assistant's tool/dispatch events, the durable record of what
	// the model asked for — and then forces them to stable storage. A tool body
	// or a model request must not run until its intent survives a hard crash:
	// without this, a power loss between recording a tool call and running it
	// leaves the log unable to explain an effect that happened.
	//
	// A failed barrier is a hard stop, not a warning: the honest failure is
	// "the round ended because its record could not be made durable," never
	// "the effect ran, unrecorded."
	checkpoint := func(why string) bool {
		flush()
		if opts.EventLog == nil {
			return true
		}
		if err := opts.EventLog.Sync(); err != nil {
			log.Printf("prompt %d: durability barrier failed (%s); refusing to proceed: %v",
				sess.ID, why, err)
			return false
		}
		return true
	}

	// Whether this turn may run device builds comes from the user's own
	// words, once, up front — never from the model's plans. A bare "build"
	// does not count (it usually means "build the screen"); only device words
	// unlock the 10-minute gradle run.
	sess.SetBuildRequested(WantsDeviceBuild(lastUserText(conversation)))

	// Routing resolves once per round, not once per turn: a turn that enters
	// repair escalates tier mid-flight. The policy loads once — it is process
	// configuration, and re-reading env every round would let a mid-build env
	// change split one turn across two policies.
	routingPolicy := ai.LoadRoutingPolicy()
	promptLen := 0
	for i := len(conversation) - 1; i >= 0; i-- {
		if conversation[i].Role == "user" {
			promptLen = len(conversation[i].Text())
			break
		}
	}
	routedModel := ""
	fellBack := false
	// latchedModel pins the turn to the fallback model after a successful
	// failover. Without it the next round would re-resolve the same broken
	// primary and die again — one retry that hands the turn back to a dead
	// model is theater, not reliability.
	latchedModel := ""
	// rambleCount tracks consecutive tool-less max_tokens rounds. Any tool
	// call resets it — only a model emitting full outputs with zero calls,
	// repeatedly, trips the breaker.
	rambleCount := 0

	for round := 1; round <= o.maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			logTurn("cancelled")
			return outcome, fmt.Errorf("cancelled: %w", err)
		}
		outcome.Rounds = round
		flush() // persist what the previous round produced before starting this one
		sess.SetRound(round)
		sess.TickCooldowns()

		// H2 steer lane: drain mid-turn messages that arrived since the last
		// round. A steer is the user redirecting the running build — it lands
		// in this round's request as its own user turn, honestly labeled, so
		// the model sees fresh instructions without the turn restarting. An
		// inject rides the same way but is marked context, not direction.
		// Deliveries made while the model was generating are picked up here,
		// at the boundary — never later than the next request.
		if steers := sess.DrainSteers(); len(steers) > 0 {
			var lane strings.Builder
			for i, in := range steers {
				if i > 0 {
					lane.WriteString("\n\n")
				}
				if in.Class == harnessagent.Steer {
					lane.WriteString("[user steered the running build] " + in.Text)
				} else {
					lane.WriteString("[user added context mid-build] " + in.Text)
				}
			}
			steerTurn := ai.UserText(lane.String())
			conversation = append(conversation, steerTurn)
			outcome.Messages = append(outcome.Messages, steerTurn)
			sess.Emitter.EmitText("↪ " + lane.String())
		}

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

		if ai.EstimateTokens(conversation) > MaxContextTokens {
			before := len(conversation)
			// Ask the model to write the checkpoint over the span being dropped
			// instead of replacing it with a static line: the summary carries
			// the intent, the errors, and the open decisions the oldest turns
			// held, which a "context was compacted" notice cannot. Degrades to
			// that notice when the summarizer fails or there is no client — the
			// budget is still honored, only the memory is lost.
			if o.ai != nil {
				conversation = o.ai.CompactWithSummary(ctx, systemPrompt, conversation, MaxContextTokens*3/4)
			} else {
				conversation = ai.Compact(conversation, MaxContextTokens*3/4)
			}
			sess.Emitter.EmitToolCall("", "compact", "", fmt.Sprintf("Compacted context (%d → %d turns)",
				before, len(conversation)), "completed")
			if opts.OnCompacted != nil {
				opts.OnCompacted(before, len(conversation))
			}
		}

		sess.Emitter.EmitPhase(events.Phase(sess.GetPhase()), fmt.Sprintf("round %d", round))

		// Collapse stale read_file/list_files bodies that a later call in this turn
		// re-fetched: they are otherwise replayed in full every round. This trims a
		// copy for the wire only — `conversation` (and the blocks it shares with
		// outcome.Messages, which is what gets persisted) keeps its full fidelity.
		sendConversation := ai.TrimSupersededToolOutput(conversation)

		// Route this round: classify from mode, prompt size, and repair state,
		// then point a client copy at the tier's model. A model change mid-turn
		// is logged with its reason — that log is the tuning data.
		_, tier, reason := ai.ClassifyTurn(opts.Mode, promptLen, sess.GetVerifyAttempts(), routingPolicy)
		model := routingPolicy.ModelFor(tier)
		client := o.ai
		if latchedModel != "" {
			model = latchedModel
			reason = "latched to fallback after primary failed"
		}
		if model != "" && model != o.ai.Model() {
			client = o.ai.WithModel(model)
		}
		// A rate limit or a transient server error backoffs for seconds with no
		// output — without this the request looks hung and gets killed. The hook
		// says which attempt failed, why, and how long the wait is, so the trace
		// shows a bounded recovery instead of silence. It also lands in the
		// session log: after a reload a recovered failure is otherwise gone.
		retryHook := func(attempt, maxRetries int, why string, after time.Duration) {
			sess.Emitter.EmitText(fmt.Sprintf("⏳ Model call %d/%d failed (%s) — retrying in %s.",
				attempt, maxRetries, why, after.Round(time.Second)))
			if opts.EventLog != nil {
				if _, err := opts.EventLog.Append(harnessevents.RetryLogged, harnessevents.RetryPayload{
					Attempt:    attempt,
					MaxRetries: maxRetries,
					Reason:     retryClass(why),
					Detail:     why,
					BackoffMs:  after.Milliseconds(),
					Model:      client.Model(),
					PromptID:   sess.ID,
				}); err != nil {
					// A failed audit append must never fail the retry it records.
					log.Printf("prompt %d: could not record the llm/retry event: %v", sess.ID, err)
				}
			}
		}
		client = client.WithRetryHook(retryHook)
		if client.Model() != routedModel {
			log.Printf("orchestrator: round %d routed to %s (%s)", round, client.Model(), reason)
			routedModel = client.Model()
			outcome.RoutedModel = routedModel
			outcome.RouteReason = reason
		}

		// Boundary 1 — before the model request: everything the previous round
		// produced (its tool results, its assistant turns) must be durable
		// before the next request is derived from it. A crash here otherwise
		// lets a resumed session hand the model a history that silently omits
		// the work it already sees the results of.
		if !checkpoint("model dispatch") {
			outcome.FinalText = "This turn stopped because its record could not be made durable before the model request. No work was lost; re-run the turn."
			return outcome, fmt.Errorf("durability barrier failed before model dispatch")
		}

		result, err := client.ChatWithTools(ctx, systemPrompt, sendConversation, opts.Tools)
		if err != nil {
			// H2: the request-error decision is a listener (harness/loop), not
			// inline policy. The default listener reproduces today's behavior
			// exactly — one retry on the fallback model, latch on success — so
			// this is a pure seam extraction; a composition can swap the policy
			// without touching the loop body.
			dec := o.requestErrorListener(round, client.Model(), err, fellBack)
			switch dec.Action {
			case loop.ErrorRetryOnModel:
				log.Printf("orchestrator: round %d model %s failed (%v); %s",
					round, client.Model(), err, dec.Reason)
				fellBack = true
				retryClient := client
				if dec.RetryModel != "" && dec.RetryModel != client.Model() {
					retryClient = o.ai.WithModel(dec.RetryModel).WithRetryHook(retryHook)
				}
				result, err = retryClient.ChatWithTools(ctx, systemPrompt, sendConversation, opts.Tools)
				if err == nil {
					if retryClient.Model() != routedModel {
						routedModel = retryClient.Model()
						outcome.RoutedModel = routedModel
						outcome.RouteReason = dec.Reason
					}
					if dec.Latch {
						latchedModel = retryClient.Model()
						log.Printf("orchestrator: round %d latched to fallback model %s for the rest of the turn",
							round, latchedModel)
					}
				}
			case loop.ErrorRetryWithBackoff:
				log.Printf("orchestrator: round %d model %s failed (%v); %s",
					round, client.Model(), err, dec.Reason)
				select {
				case <-time.After(dec.Backoff):
				case <-ctx.Done():
				}
				result, err = client.ChatWithTools(ctx, systemPrompt, sendConversation, opts.Tools)
			case loop.ErrorTerminal:
				// Fall through to the error exit below.
			}
		}
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

		// The meter's source: one usage/record event per model call, so the
		// cumulative accounting survives a reload instead of living only in the
		// SSE stream. Cost attribution prices the model that actually served
		// the round.
		if opts.EventLog != nil {
			if _, err := opts.EventLog.Append(harnessevents.UsageRecord, harnessevents.UsagePayload{
				InputTokens:  result.Usage.InputTokens,
				OutputTokens: result.Usage.OutputTokens,
				CacheRead:    result.Usage.CacheReadTokens,
				Model:        client.Model(),
			}); err != nil {
				log.Printf("prompt %d: could not record the usage event: %v", sess.ID, err)
			}
		}

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

		// Durability ordering: the assistant turn carrying this round's tool_use
		// blocks must be persisted before any of those tools run. Persist is
		// what appends the tool/dispatch event — the durable record that the
		// model asked for the effect — and executeTools' barrier Syncs it to
		// stable storage. Without this flush the side effect would precede its
		// own record, and a crash between the two would leave a changed file
		// the log cannot explain.
		flush()

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
			// Any tool call is progress: the ramble breaker only counts
			// consecutive tool-less rounds, so a working round resets it.
			rambleCount = 0
			// mark_task_done is orchestrator-owned like ask_user, but it does not
			// pause: apply its effect on the plan graph here, then let the call run
			// its benign Execute so the tool_use still gets a matching tool_result.
			o.applyMarkTaskDone(sess, result.ToolUses)

			// try_edit/keep_edit are orchestrator-owned when the patch buffer
			// lane is on: intercept them here, run the rest normally, and
			// merge back in call order so downstream indices still align.
			owned := o.applyPatchBuffer(ctx, sess, result.ToolUses, opts)
			var rest []ai.ToolUseBlock
			var restIdx []int
			for i, u := range result.ToolUses {
				if _, ok := owned[i]; !ok {
					rest = append(rest, u)
					restIdx = append(restIdx, i)
				}
			}
			restResults := o.executeTools(ctx, sess, rest, opts)
			results := make([]ai.Block, len(result.ToolUses))
			for i, r := range owned {
				results[i] = r
			}
			for j, i := range restIdx {
				results[i] = restResults[j]
			}
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
			rambleCount++
			if rambleCount >= maxRambleRounds {
				// Ramble loop: full outputs, zero tool calls, repeatedly.
				// Continuing just burns tokens — end the turn honestly as
				// stalled (terminal, not resumable) with what happened named.
				sess.TransitionTo(PhaseDone)
				sess.Emitter.EmitDone("stalled")
				outcome.FilesTouched = o.touchedFiles(opts)
				outcome.Stalled = true
				outcome.RemainingTasks = pendingPlanTasks(sess)
				outcome.FinalText += remainingNote(sess, "Stalled") +
					"\n\nThe model produced " + strconv.Itoa(rambleCount) +
					" consecutive full-length responses without calling any tools, " +
					"so the turn was stopped instead of burning the round budget. " +
					"No files were written by those rounds — re-prompt with a smaller, " +
					"concrete next step."
				log.Printf("orchestrator: turn stalled after %d tool-less max_tokens rounds; not resumable", rambleCount)
				logTurn("stalled")
				return outcome, nil
			}
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
			metrics.Default.ObserveVerifyFailure()
			logTurn("verify-failed")
			return outcome, fmt.Errorf("code did not pass verification after %d repair attempts", maxVerifyAttempts)
		}

		sess.TransitionTo(PhaseVerifyStatic)
		verdict := o.verify(ctx, sess, opts)
		if verdict.ok {
			// The project compiles and these files were written this run, so any
			// screen whose route file now exists is honestly done — mark it before
			// finishing. Then audit the other direction: any completed screen with
			// no route file on disk was checked off without being built, so flip
			// it back rather than letting a false completion through.
			o.markScreenTasksFromFiles(sess, touched)
			o.auditScreenTasks(sess, opts)
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			outcome.FilesTouched = touched
			outcome.Verified = true
			outcome.SkippedGates = sess.SkippedGates()
			outcome.RemainingTasks = pendingPlanTasks(sess)
			outcome.FinalText += remainingNote(sess, "Verified, but stopping")
			if len(outcome.SkippedGates) > 0 {
				outcome.FinalText += "\n\nVerification note: " + strings.Join(outcome.SkippedGates, ", ") +
					" check(s) could not run (missing infra) — verified covers everything that ran, not these."
			}
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

// patchBufferOn resolves the G6 lane: per-session opt-in, or the
// TALONSTUDIO_PATCH_BUFFER env var for fleet-wide experiments. Both default
// off — off behaves identically to today.
func patchBufferOn(opts RunOptions) bool {
	if opts.PatchBufferEnabled {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_PATCH_BUFFER"))) {
	case "1", "true", "on":
		return true
	}
	return false
}

// applyPatchBuffer intercepts try_edit/keep_edit calls, returning owned
// result blocks keyed by call index. Calls it does not own are absent from
// the map and flow to executeTools normally. When the lane is off the map
// is empty and the calls reach their benign fallback Execute.
func (o *Orchestrator) applyPatchBuffer(ctx context.Context, sess *SessionState, calls []ai.ToolUseBlock, opts RunOptions) map[int]ai.Block {
	owned := make(map[int]ai.Block)
	if !patchBufferOn(opts) {
		return owned
	}
	if sess.PatchBuffer == nil {
		sess.PatchBuffer = build.NewPatchBuffer()
	}
	for i, call := range calls {
		switch call.Name {
		case "try_edit":
			owned[i] = o.applyTryEdit(ctx, sess, call, opts)
		case "keep_edit":
			owned[i] = o.applyKeepEdit(sess, call)
		}
	}
	return owned
}

type tryEditArgs struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Content string `json:"content"`
	Edits   []struct {
		Old string `json:"old"`
		New string `json:"new"`
	} `json:"edits"`
}

func tryEditResult(call ai.ToolUseBlock, content string, isError bool) ai.Block {
	return ai.Block{Type: "tool_result", ToolUseID: call.ID, Content: content, IsError: isError}
}

// resolvePatchPath keeps try_edit inside the workspace: clean the path and
// refuse anything escaping the root. The tool path is model-controlled, so
// this is a guardrail, not a helper.
func resolvePatchPath(workspace, path string) (string, error) {
	clean := filepath.Clean("/" + strings.TrimSpace(path))
	abs := filepath.Join(workspace, clean)
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes the workspace")
	}
	return abs, nil
}

func (o *Orchestrator) applyTryEdit(ctx context.Context, sess *SessionState, call ai.ToolUseBlock, opts RunOptions) ai.Block {
	var a tryEditArgs
	if json.Unmarshal(call.Input, &a) != nil || strings.TrimSpace(a.Path) == "" {
		return tryEditResult(call, "try_edit needs a path and a mode (edit/write/multi).", true)
	}
	// Defensive: the loop always inits via applyPatchBuffer, but a direct
	// call must not panic on a nil buffer.
	if sess.PatchBuffer == nil {
		sess.PatchBuffer = build.NewPatchBuffer()
	}
	abs, err := resolvePatchPath(sess.WorkspacePath, a.Path)
	if err != nil {
		return tryEditResult(call, "try_edit refused: "+err.Error(), true)
	}

	// Map the mode onto the underlying tool call the model would have made.
	var tool string
	var toolArgs map[string]any
	switch a.Mode {
	case "edit":
		if a.Old == "" {
			return tryEditResult(call, "mode edit needs old and new text.", true)
		}
		tool, toolArgs = "edit_file", map[string]any{"path": a.Path, "old": a.Old, "new": a.New}
	case "write":
		tool, toolArgs = "write_file", map[string]any{"path": a.Path, "content": a.Content}
	case "multi":
		if len(a.Edits) == 0 {
			return tryEditResult(call, "mode multi needs at least one old/new pair.", true)
		}
		tool, toolArgs = "multi_edit", map[string]any{"path": a.Path, "edits": a.Edits}
	default:
		return tryEditResult(call, "unknown mode — use edit, write, or multi.", true)
	}

	// Pre-image first: read the current content (missing is fine for write),
	// then stage it before anything mutates the file.
	var preImage []byte
	if data, rerr := os.ReadFile(abs); rerr == nil {
		preImage = data
	} else if a.Mode != "write" {
		return tryEditResult(call, "cannot try_edit a file that does not exist (use mode write to create it).", true)
	}
	evicted := sess.PatchBuffer.Stage(a.Path, preImage, sess.GetRound())
	notice := ""
	if evicted != "" {
		notice = fmt.Sprintf("\nNote: the patch buffer is full — the oldest pre-image (%s) was dropped and can no longer be reverted.", evicted)
	}

	toolInput, _ := json.Marshal(toolArgs)
	output, isError := opts.Execute(ctx, tool, toolInput)
	if isError {
		// The edit itself failed — nothing changed, so the stage is stale.
		sess.PatchBuffer.Accept(a.Path)
		return tryEditResult(call, output+notice, true)
	}

	// Gate 1 minimum: the change must typecheck. Unavailable (no tsconfig)
	// reads as passed — same policy as the ladder.
	verifyOut, _ := opts.Execute(ctx, "run_typecheck", json.RawMessage(`{}`))
	var vr tools.VerifyResult
	passed := true
	reason := ""
	if json.Unmarshal([]byte(verifyOut), &vr) == nil {
		passed = vr.Passed
		reason = vr.Summary
		if vr.Errors != "" {
			reason = vr.Errors
		}
	}
	if passed {
		return tryEditResult(call, output+fmt.Sprintf("\nTypecheck passed — edit stands, pre-image staged (keep_edit %s to accept).", a.Path)+notice, false)
	}

	// Type-breaking: revert to the pre-image with a direct write (not the
	// write_file tool — re-entering the tool would re-trigger guardrails on
	// a file whose mtime just moved), then refresh the session's freshness
	// tracking so the next fix is not refused as unread.
	if werr := os.WriteFile(abs, preImage, 0o644); werr != nil {
		return tryEditResult(call, fmt.Sprintf("Typecheck failed (%s) AND the revert failed (%v) — the file may be half-edited. Read it before touching it.", reason, werr), true)
	}
	sess.MarkFileRead(a.Path)
	if mt, ok := fileMtime(sess.WorkspacePath, a.Path); ok {
		sess.NoteFileKnown(a.Path, mt)
	}
	return tryEditResult(call, fmt.Sprintf("Reverted: typecheck failed (%s). The file is back to what it was — fix the error and try again, do not build on the broken version.", reason)+notice, true)
}

func (o *Orchestrator) applyKeepEdit(sess *SessionState, call ai.ToolUseBlock) ai.Block {
	var a struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(call.Input, &a) != nil || strings.TrimSpace(a.Path) == "" {
		return tryEditResult(call, "keep_edit needs the path the try_edit used.", true)
	}
	if sess.PatchBuffer == nil {
		return tryEditResult(call, "Nothing staged — the patch buffer is off in this session.", false)
	}
	sess.PatchBuffer.Accept(strings.TrimSpace(a.Path))
	return tryEditResult(call, "Accepted — the edit stands as an ordinary change.", false)
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

	// H3 advisory repeat reminder, counted at the batch level: a tool that
	// appears in this batch increments its counter once (the batch is the
	// unit, matching how the model issues them — parallel siblings are one
	// decision). Counting per dispatch instead made the reminder depend on
	// goroutine interleaving, which the transcript-parity gate rejects.
	// Escalating at 3/5/8 same-tool batches; the note rides the first
	// matching result so whichever the model reads carries it.
	repeatNotes := map[int]string{}
	seenInBatch := map[string]bool{}
	for i, c := range calls {
		if seenInBatch[c.Name] {
			continue
		}
		seenInBatch[c.Name] = true
		if count := sess.NoteToolRepeat(c.Name); repeatReminderThresholds[count] {
			// The first call of this tool in the batch carries the note —
			// decided here, before dispatch, so no goroutine writes shared
			// state deciding it.
			repeatNotes[i] = repeatReminder(c.Name, count)
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

	runCall := func(i int, call ai.ToolUseBlock) (block ai.Block) {
		// A tool that panics must not take the build down with it. Recover here,
		// at the body both the parallel and sequential paths invoke, and turn the
		// crash into a failed tool_result the model can react to. Without this a
		// single panicking tool in a goroutine kills the whole process; with it the
		// loop survives and the model tries a smaller step. The result still carries
		// call.ID, so the tool_use is never left without its matching tool_result.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("orchestrator: tool %q panicked: %v\n%s", call.Name, r, debug.Stack())
				block = ai.Block{
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
			return ai.Block{
				Type:      "tool_result",
				ToolUseID: call.ID,
				Content:   "Blocked by a project guardrail (" + dec.Rule + "): " + dec.Message,
				IsError:   true,
			}
		}

		verb := describeTool(call.Name, call.Input, sess.WorkspacePath)
		target := toolTarget(call.Name, call.Input)
		sess.Emitter.EmitToolCall(call.ID, call.Name, target, verb, "running")

		if taskID := taskIDForTool(call.Name); taskID != "" {
			sess.EmitTaskUpdate(taskID, verb, "in_progress", "")
		}

		// Boundary 2 is enforced once before the dispatch fork, not inside
		// runCall: a parallel read-only batch crosses it together.

		output, isError := opts.Execute(ctx, call.Name, call.Input)

		// H8 context economy, in order of severity: spill moves a giant
		// output to disk (the model re-reads by range through read_file);
		// prune shrinks a merely-large one to head+tail with the omitted
		// count named. Both preserve the stored transcript's full fidelity —
		// only the wire copy shrinks.
		if o.spill != nil {
			if rep, spilled, err := o.spill.Spill(call.Name, output); err == nil && spilled {
				output = rep
			}
		}
		if compaction.NeedsPrune(output) {
			output = compaction.Prune(output)
		}

		// readNote/watchNote/probeNote carry cross-turn knowledge into this result. They
		// are set by the tracking below and appended to the tool result at the
		// end of run(): small hints that prevent whole wasted rounds.
		repeatNote := repeatNotes[i]
		readNote := ""
		watchNote := ""
		probeNote := ""
		failReminder := ""

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

		// Failure-pattern lookup: if the error matches a known pattern and
		// its cooldown has expired, inject a one-line reminder so the model
		// fixes the known issue instead of reasoning it from scratch.
		// Failed device steps additionally file in the run-scoped crash
		// index, so the crash triage sees every failed step in one place.
		if isError {
			patterns := knowledge.DefaultFailureIndex()
			// Compile and append runtime patterns from project memory.
			for _, rp := range sess.RuntimePatterns {
				if rp.Match != nil {
					patterns = append(patterns, rp)
				}
			}
			if pat, ok := knowledge.Lookup(patterns, call.Name, output); ok {
				norm := knowledge.NormalizeError(output)
				if sess.CooldownLeft(call.Name, norm) == 0 {
					failReminder = "\n\n" + pat.Reminder()
					sess.NoteCooldown(call.Name, norm, pat.CooldownRounds)
				}
			}
			if isDeviceStep(call.Name) {
				sess.NoteCrash(runtime.CrashEntry{
					Tx:      crashTx(call.Input),
					Tool:    call.Name,
					Excerpt: runtime.CrashExcerpt(output, 20),
				})
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
		// first inspection redirects to inspect_package (one call returns the
		// whole API surface — hand-reading .d.ts files burns rounds), and the
		// third means the shape is confirmed and should be recorded
		// (write_memory) instead of re-probed.
		if !isError {
			switch call.Name {
			case "run_shell", "grep_codebase", "read_file":
				if target := probeTarget(call.Name, call.Input); target != "" {
					n := sess.NoteProbe(target)
					probeNote = probeNoteFor(n, target)
				}
			}
		}

		return ai.Block{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   output + readNote + watchNote + probeNote + failReminder + repeatNote,
			IsError:   isError,
		}
	}

	// Boundary 2 — before the side effect. The assistant message carrying
	// these tool_use blocks was flushed to Persist by the caller, which
	// appends the tool/dispatch event: the durable record that the model
	// asked for these effects. No body runs until that record is on stable
	// storage — otherwise a power loss between here and the effect leaves a
	// changed file the log cannot explain. One barrier per round, ahead of
	// the fork, so a parallel batch costs one checkpoint, not one per call.
	if opts.EventLog != nil {
		if err := opts.EventLog.Sync(); err != nil {
			log.Printf("prompt %d: durability barrier failed; refusing to run this round's tools: %v",
				sess.ID, err)
			for i, call := range calls {
				results[i] = ai.Block{
					Type:      "tool_result",
					ToolUseID: call.ID,
					Content: "The tool could not run because its record could not be made durable. " +
						"Re-run the turn; nothing was changed.",
					IsError: true,
				}
			}
			return results
		}
	}

	// H3: TALONSTUDIO_PIPELINE=v1 selects the bounded harness pipeline for
	// batch dispatch. runCall — guardrails, emission, result notes — is the
	// single body in both modes; the pipeline replaces only the scheduling:
	// read-only runs overlap in a bounded rolling pool (legacy peaks at one
	// goroutine per call), and an exclusive call drains the pool and runs
	// alone (legacy serializes the whole batch). The flag ships dark.
	if pipelineEnabled() && len(calls) > 0 {
		callIndex := map[string]int{}
		for i, c := range calls {
			callIndex[c.ID] = i
		}
		pipe := pipeline.New(
			nil, // policy stays inside runCall — moving it to the stage is H4
			pipeline.DispatcherFunc(func(ctx context.Context, call pipeline.Call) (string, bool) {
				blk := runCall(callIndex[call.ID], ai.ToolUseBlock{ID: call.ID, Name: call.Name, Input: call.Input})
				return blk.Content, blk.IsError
			}),
			pipeline.ClassifierFunc(func(name string) bool {
				return opts.ReadOnlyTool != nil && opts.ReadOnlyTool(name)
			}),
			nil,
		)
		batched := pipe.Run(ctx, toPipelineCalls(calls))
		for i, r := range batched {
			results[i] = ai.Block{Type: "tool_result", ToolUseID: r.ID, Content: r.Content, IsError: r.IsError}
		}
	} else if parallel {
		var wg sync.WaitGroup
		for i, call := range calls {
			wg.Add(1)
			go func(i int, call ai.ToolUseBlock) {
				defer wg.Done()
				results[i] = runCall(i, call)
			}(i, call)
		}
		wg.Wait()
	} else {
		for i, call := range calls {
			results[i] = runCall(i, call)
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

// toPipelineCalls adapts the orchestrator's tool_use batch to pipeline
// Calls — a pure shape conversion; the pipeline owns no orchestrator types.
func toPipelineCalls(calls []ai.ToolUseBlock) []pipeline.Call {
	out := make([]pipeline.Call, len(calls))
	for i, c := range calls {
		out[i] = pipeline.Call{ID: c.ID, Name: c.Name, Input: c.Input}
	}
	return out
}

// probeTarget extracts the third-party package under inspection from a tool
// call, or "" when the call inspects workspace code. node_modules/<pkg> (with
// an optional @scope) is the unit: repeated looks at one package answer one
// question, so the counter keys on the package, not the exact command.
// nodeBuiltins are the modules every Node install ships: probing one is
// reading the platform, not discovering a third-party package, so they do
// not count as probes.
var nodeBuiltins = map[string]bool{
	"fs": true, "path": true, "os": true, "util": true, "events": true,
	"stream": true, "crypto": true, "http": true, "https": true, "url": true,
	"child_process": true, "buffer": true, "process": true, "assert": true,
	"zlib": true, "querystring": true, "string_decoder": true, "timers": true,
}

// requirePattern finds require('pkg') / require("pkg") mentions in a shell
// command. Relative ('./x', '../x') and builtin (fs, path…) requires are not
// third-party probes and return "".
// requirePattern matches any require('…') argument; the package unit
// (with @scope) is extracted in code below, so sub-paths cut correctly.
var requirePattern = regexp.MustCompile(`require\(['"]([^'"]+)['"]\)`)

func probeTarget(toolName string, input json.RawMessage) string {
	var text string
	switch toolName {
	case "run_shell", "run_command":
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

	// A require() in a shell command is the model probing a third-party
	// package's shape by execution. The first such probe is a legitimate
	// question; keyed on the package name so repeats count.
	if m := requirePattern.FindStringSubmatch(text); m != nil {
		pkg := m[1]
		if strings.HasPrefix(pkg, "./") || strings.HasPrefix(pkg, "../") ||
			strings.HasPrefix(pkg, "/") || nodeBuiltins[pkg] {
			return ""
		}
		// The package unit is @scope/name or name — anything after a second
		// slash (a sub-path) is the same package, not a new probe.
		parts := strings.Split(pkg, "/")
		if len(parts) > 2 && strings.HasPrefix(parts[0], "@") {
			return parts[0] + "/" + parts[1]
		}
		if len(parts) > 1 && !strings.HasPrefix(parts[0], "@") && parts[0] != "" {
			// Bare package with a sub-path: node_modules semantics say the
			// first segment is the package.
			return parts[0]
		}
		return pkg
	}

	// node_modules/<pkg> paths: the unit is the package (with @scope), not
	// the deep file, because repeated looks at one package answer one
	// question.
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

// probeNoteFor renders the probe policy: first look redirects to
// inspect_package, third look consolidates to write_memory, anything else is
// silent. Pure so the policy is unit-testable without a running loop.
func probeNoteFor(n int, target string) string {
	switch n {
	case 1:
		return fmt.Sprintf("\n\nNote: you are reading %s by hand. Call inspect_package "+
			"with {\"package\": \"%s\"} instead — one call returns its full exported API "+
			"surface, which is what %d rounds of .d.ts spelunking approximates.",
			target, target, probeConsolidateThreshold)
	case probeConsolidateThreshold:
		return fmt.Sprintf("\n\nNote: this is your %drd look at %s this turn. "+
			"You have confirmed what you came for — record the API shape with write_memory "+
			"and proceed to write code instead of probing it again.", n, target)
	default:
		return ""
	}
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
// It also implements guardrails.Versioned (H5): version stamps come from the
// session's KnownFiles (last-seen) and the workspace mtime (current), so a
// write over a file someone else changed mid-turn is refused, not clobbering.
type guardState struct {
	sess         *SessionState
	exists       func(string) bool
	currentMtime func(path string) (int64, bool)
}

func (g guardState) HasReadFile(path string) bool { return g.sess.HasReadFile(path) }
func (g guardState) FileExists(path string) bool {
	return g.exists != nil && g.exists(path)
}

func (g guardState) LastSeenVersion(path string) int64 {
	return g.sess.KnownFileMtime(path)
}

func (g guardState) CurrentVersion(path string) int64 {
	if g.currentMtime == nil {
		return 0
	}
	mt, ok := g.currentMtime(path)
	if !ok {
		return 0
	}
	return mt
}

// guardrail evaluates one tool call against the deterministic guardrails before
// it runs. It is the single place a call can be refused for policy: the rules
// tell the model what not to do, and this makes the security-critical ones hold
// even when the model does not comply.
func (o *Orchestrator) guardrail(sess *SessionState, name string, input json.RawMessage, opts RunOptions) guardrails.Decision {
	// build_apk is a 10-minute gradle run: refuse it unless the user's own
	// prompt asked for a device build, or the user granted device_build approval
	// mid-turn by answering. Answers arrive as tool_result turns, invisible to
	// text scanning — the turn_approvals table is their binding record.
	if name == "build_apk" && !sess.WantsBuild() && !o.approved(sess.ID) {
		return guardrails.Decision{
			Allowed: false,
			Rule:    "scope",
			Message: "The user did not ask for a device build this turn — verify with run_checks instead. " +
				"If they actually want an APK on a device, ask them with ask_user first.",
		}
	}
	st := guardState{sess: sess, exists: opts.Exists, currentMtime: func(path string) (int64, bool) {
		return fileMtime(sess.WorkspacePath, path)
	}}
	return guardrails.Check(name, input, st, o.preinstalled)
}

// approved reports whether the prompt lifecycle holds a device_build
// approval (granted via Answer). Nil-store safe: test harnesses and
// sessions without persistence simply have no approvals.
func (o *Orchestrator) approved(promptID int64) bool {
	if o.store == nil {
		return false
	}
	return o.store.HasApproval(promptID, store.ScopeDeviceBuild)
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
	// unavailable marks a gate that did not run (tool error, unreadable or
	// contract-less output). ok stays true — fail-open — but the sweep
	// records it so a verified outcome can name what never ran.
	unavailable bool
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
		// The gates below are G4. Each one returns passed=true with an
		// "unavailable" summary when its infra is missing (no android/ dir,
		// no JDK, no device) so the ladder is safe to run on every repair
		// without blowing up on hosts that lack the full toolchain.
		{stage: "bundle", tool: "run_bundle", feedback: bundleFeedback},
		{stage: "gradle", tool: "run_gradle", feedback: gradleFeedback},
		{stage: "boot", tool: "run_boot", feedback: bootFeedback},
		{stage: "vision", tool: "run_vision", feedback: visionFeedback},
		{stage: "interact", tool: "run_interact", feedback: interactFeedback},
	}
}

// verify walks the gates in order and returns the first failure as feedback the
// agent can act on, or ok once every gate is satisfied (or unavailable). Each
// unavailable gate is noted on the session so a verified outcome can list
// exactly which checks never actually ran.
func (o *Orchestrator) verify(ctx context.Context, sess *SessionState, opts RunOptions) verifyVerdict {
	o.resetVerifyReport()
	sess.BeginGateSweep()
	for _, g := range o.verifyGates() {
		v := o.runGate(ctx, sess, opts, g)
		if !v.ok {
			return v
		}
		if v.unavailable {
			sess.NoteGateUnavailable(g.stage)
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
		o.appendVerifyGate(g.stage, "unavailable", reason)
		return verifyVerdict{ok: true, unavailable: true}
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(raw), &vr); err != nil {
		log.Printf("orchestrator: %s check output unreadable (%v); skipping so the build is not blocked by checker breakage", g.stage, err)
		sess.Emitter.EmitVerify(g.stage, "unavailable", "unreadable "+g.stage+" output")
		o.appendVerifyGate(g.stage, "unavailable", "unreadable "+g.stage+" output")
		return verifyVerdict{ok: true, unavailable: true}
	}

	// A verdict without an explicit "passed" field is a checker that does not
	// speak the contract — {"ok":true} parses fine with passed defaulting to
	// false, which used to fail the whole build on an empty error message.
	// Treat it like unreadable output: unavailable, visible, non-blocking.
	// A checker that wants to fail a build must say passed:false explicitly.
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &shape); err == nil {
		if _, ok := shape["passed"]; !ok {
			log.Printf("orchestrator: %s check omitted the passed verdict; skipping so the build is not blocked by checker breakage", g.stage)
			sess.Emitter.EmitVerify(g.stage, "unavailable", g.stage+" omitted the passed verdict")
			o.appendVerifyGate(g.stage, "unavailable", g.stage+" omitted the passed verdict")
			return verifyVerdict{ok: true, unavailable: true}
		}
	}

	if vr.Passed {
		sess.Emitter.EmitVerify(g.stage, "pass", vr.Summary)
		o.appendVerifyGate(g.stage, "pass", vr.Summary)
		return verifyVerdict{ok: true}
	}

	sess.Emitter.EmitVerify(g.stage, "fail", vr.Summary)
	o.appendVerifyGate(g.stage, "fail", vr.Summary)
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

// G4 feedback functions. Each turns a failed gate verdict into repair guidance.
// "Unavailable" verdicts are passed=true so they never reach these — the ladder
// short-circuits on passed, so an unavailable gate never invokes feedback.

func bundleFeedback(vr verifyResult) string {
	return "Metro bundling failed. The project compiles but cannot be bundled for Android, " +
		"which means it will not run on a device. Metro errors are usually a missing module, " +
		"a broken asset path, or a Reanimated/Babel plugin mismatch.\n\n" +
		"Metro reported:\n\n" + vr.Errors + "\n\n" +
		"Read the file Metro names, then check the import path. If Reanimated is involved, " +
		"confirm babel.config.js includes 'react-native-reanimated/plugin'. " +
		"Then call run_bundle to confirm."
}

func gradleFeedback(vr verifyResult) string {
	return "Gradle assembleDebug failed. The project bundles for Metro but will not produce an APK. " +
		"This is usually a native module version mismatch, a missing permission, or a JDK / SDK mismatch.\n\n" +
		"Gradle reported:\n\n" + vr.Errors + "\n\n" +
		"Read android/app/build.gradle and the failing module's build.gradle. " +
		"Then call run_gradle to confirm."
}

func bootFeedback(vr verifyResult) string {
	return "The APK was built but did not come up on the device. Distinguish the three cases " +
		"before retrying: (1) adb install failed — reinstall; (2) the process never appeared in " +
		"pidof — it crashed on launch, read the logcat excerpt for the FATAL block; (3) the " +
		"process is alive but produced no first-launch log — the launch activity is wrong or " +
		"the app white-screens before running code, not a build problem.\n\n" +
		"adb reported:\n\n" + vr.Errors + "\n\n" +
		"Check this run's crash index for earlier failed device steps on the same package — " +
		"a repeated identical failure already has its known fix. Otherwise run tail_logcat " +
		"scoped to the package before re-running run_boot with a cleared buffer."
}

func visionFeedback(vr verifyResult) string {
	return "The app is on the device but the screen does not match what the plan intended, " +
		"or the screen shows an error state. Vision is the cheapest way to catch a white screen, " +
		"a crash dialog, or a wrong route.\n\n" +
		"Vision reported:\n\n" + vr.Errors + "\n\n" +
		"Read the named file or route, then re-run the build loop. The vision check is " +
		"intentionally lenient — if it disagrees, the screen is genuinely wrong."
}

func interactFeedback(vr verifyResult) string {
	return "At least one route in the plan is not reachable. The app launched, but a tap " +
		"did not land on the intended screen, or the screen on the other side is broken.\n\n" +
		"Interact reported:\n\n" + vr.Errors + "\n\n" +
		"Read the route file the gate named, confirm the screen exists and is registered, " +
		"then re-run the build loop."
}

// lastVerifyReport is the chain report accumulator the run_gate tool reads. It
// is reset at the start of every verify call and populated as each gate runs.
// The accessor is a method on Orchestrator so tests can substitute a fake.
func (o *Orchestrator) lastVerifyReport() *tools.VerifyReport {
	return o.verifyReport.Load()
}

func (o *Orchestrator) resetVerifyReport() {
	o.verifyReport.Store(&tools.VerifyReport{})
}

func (o *Orchestrator) appendVerifyGate(stage, status, note string) {
	cur := o.verifyReport.Load()
	if cur == nil {
		cur = &tools.VerifyReport{}
	}
	next := &tools.VerifyReport{
		OverallPassed: cur.OverallPassed && status != "fail",
		Gates:         append(append([]tools.VerifyReportGate{}, cur.Gates...), tools.VerifyReportGate{Stage: stage, Status: status, Note: note}),
	}
	o.verifyReport.Store(next)
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

	// H7: the prompt is an ordered registry of fingerprinted sections. The
	// bytes assembled here are identical to the monolith it replaced — the
	// section split is a pure refactor that generalizes the projectMapFP
	// cache discipline to every section: a section whose render is stable
	// keeps the cached prefix; a change late in the order (memory, ADRs)
	// does not invalidate what sits before it.
	reg := promptsec.New()

	// persona — the caller's base prompt (mode persona + device facts).
	reg.Register(promptsec.Section{
		Name:   "persona",
		Weight: promptsec.WeightPersona,
		Render: func() string { return systemPrompt },
	})

	// rules/core — always-on rules + preinstalled packages; identical across
	// sessions, so they sit adjacent to the persona for the longest cached
	// prefix.
	if o.knowledge != nil {
		reg.Register(promptsec.Section{
			Name:   "rules/core",
			Weight: promptsec.WeightRules,
			Render: func() string { return o.knowledge.RulesBlock() },
		})
		// H6: the multi-source catalog, when attached, replaces the
		// embedded-only manifest — workspace skills listed alongside
		// embedded ones (bodies still load through read_skill only).
		reg.Register(promptsec.Section{
			Name:   "skills/catalog",
			Weight: promptsec.WeightSkills,
			Render: func() string {
				if o.skills != nil {
					return o.skills.Manifest()
				}
				return o.knowledge.Manifest()
			},
		})
	}

	// instructions chain — H6: the project's AGENTS.md conventions, budgeted,
	// re-read on each enrichment (reconciliation-on-touch, no watcher).
	if o.instructionsChain != nil && sess.WorkspacePath != "" {
		reg.Register(promptsec.Section{
			Name:   "instructions/chain",
			Weight: promptsec.WeightInstructions,
			Render: func() string { return o.instructionsChain(sess.WorkspacePath) },
		})
	}

	// working-discipline — model-specific but session-invariant; belongs in
	// the cached prefix.
	if o.discipline != "" {
		reg.Register(promptsec.Section{
			Name:   "discipline",
			Weight: promptsec.WeightDiscipline,
			Render: func() string { return o.discipline },
		})
	}

	// project map — the file-tree snapshot; byte-stable while the tree is.
	if pm := sess.ProjectMap(); pm != "" {
		reg.Register(promptsec.Section{
			Name:   "project/map",
			Weight: promptsec.WeightProjectMap,
			Render: func() string { return sess.ProjectMap() },
		})
	}

	// plan checklist — the settled screens/features; varies only when a
	// task's status flips.
	if tasks := sess.GetTasks(); len(tasks) > 0 {
		reg.Register(promptsec.Section{
			Name:   "plan/checklist",
			Weight: promptsec.WeightPlanCheck,
			Render: func() string { return renderPlanChecklist(sess.GetTasks()) },
		})
	}

	// ADRs — the settled architecture decisions.
	if adrs := sess.GetADRs(); len(adrs) > 0 {
		reg.Register(promptsec.Section{
			Name:   "adrs",
			Weight: promptsec.WeightADRs,
			Render: func() string { return renderADRs(sess.GetADRs(), maxADRs, maxMemoryLen) },
		})
	}

	// memory — what exists in this project; the most volatile section, so it
	// renders last.
	if memory := sess.GetMemory(); len(memory) > 0 {
		reg.Register(promptsec.Section{
			Name:   "memory",
			Weight: promptsec.WeightMemory,
			Render: func() string { return renderMemory(sess.GetMemory(), maxMemory, maxMemoryLen) },
		})
	}

	return reg.Assemble()
}

// renderADRs formats the bounded architecture-decision list.
func renderADRs(adrs []store.ArchitectureDecision, max, lineLen int) string {
	if len(adrs) > max {
		adrs = adrs[len(adrs)-max:]
	}
	var b strings.Builder
	b.WriteString("## Architecture decisions already made\n\n")
	for _, adr := range adrs {
		fmt.Fprintf(&b, "- **%s** — %s\n", adr.Decision, adr.Outcome)
		if adr.Context != "" {
			fmt.Fprintf(&b, "  Why: %s\n", truncateLine(adr.Context, lineLen))
		}
	}
	b.WriteString("\nThese are settled. Build within them. If a request genuinely conflicts with one, " +
		"say so and propose how to reconcile it rather than quietly introducing a second pattern.\n")
	return b.String()
}

// renderMemory formats the bounded project-memory list.
func renderMemory(memory []store.ProjectMemory, max, lineLen int) string {
	if len(memory) > max {
		memory = memory[len(memory)-max:]
	}
	var b strings.Builder
	b.WriteString("## What exists in this project\n\n")
	for _, m := range memory {
		fmt.Fprintf(&b, "- [%s] %s\n", m.EntryType, truncateLine(m.Content, lineLen))
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
// a memory write is bookkeeping, not the build. A nil store (unit tests that
// drive the loop without persistence) skips silently for the same reason.
func (o *Orchestrator) recordTurnMemory(sess *SessionState, touched []string, outcome *BuildOutcome) {
	if o.store == nil || len(touched) == 0 {
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
