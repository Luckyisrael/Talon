package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/guardrails"
	"github.com/seekersstudio/backend/internal/store"
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

func New(aiClient *ai.Client, s *store.Store, knowledge *agent.Registry) *Orchestrator {
	o := &Orchestrator{
		ai:           aiClient,
		sessions:     make(map[int64]*SessionState),
		store:        s,
		knowledge:    knowledge,
		preinstalled: knowledge.PreinstalledSet(),
		maxRounds:    40,
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

	sess.TransitionTo(PhaseGenerate)
	sess.ClearReadFiles()

	// conversation is what goes to the provider; added tracks just this run's
	// turns so the caller can append them to storage.
	conversation := make([]ai.Message, len(opts.History))
	copy(conversation, opts.History)

	outcome := &BuildOutcome{}
	verifyAttempts := 0
	const maxVerifyAttempts = 6

	for round := 1; round <= o.maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return outcome, fmt.Errorf("cancelled: %w", err)
		}
		outcome.Rounds = round

		if ai.EstimateTokens(conversation) > maxContextTokens {
			before := len(conversation)
			conversation = ai.Compact(conversation, maxContextTokens*3/4)
			sess.Emitter.EmitToolCall("compact", fmt.Sprintf("Compacted context (%d → %d turns)",
				before, len(conversation)), "", "completed")
		}

		sess.Emitter.EmitPhase(events.Phase(sess.GetPhase()), fmt.Sprintf("round %d", round))

		result, err := o.ai.ChatWithTools(ctx, systemPrompt, conversation, opts.Tools)
		if err != nil {
			sess.TransitionTo(PhaseFailed)
			sess.Emitter.EmitError(err.Error())
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

		// ── The model is still working ──
		if len(result.ToolUses) > 0 {
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
			return outcome, nil
		}

		// Code changed, so it has to compile before the turn can be called done.
		if verifyAttempts >= maxVerifyAttempts {
			sess.TransitionTo(PhaseFailed)
			outcome.FilesTouched = touched
			sess.Emitter.EmitError(fmt.Sprintf("code did not pass verification after %d repair attempts", maxVerifyAttempts))
			return outcome, fmt.Errorf("code did not pass verification after %d repair attempts", maxVerifyAttempts)
		}

		sess.TransitionTo(PhaseVerifyStatic)
		verdict := o.verify(ctx, sess, opts)
		if verdict.ok {
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			outcome.FilesTouched = touched
			outcome.Verified = true
			return outcome, nil
		}

		verifyAttempts++
		sess.IncrementHealAttempt()
		sess.TransitionTo(PhaseGenerate)
		feedback := ai.UserText(verdict.feedback)
		conversation = append(conversation, feedback)
		outcome.Messages = append(outcome.Messages, feedback)
	}

	sess.TransitionTo(PhaseFailed)
	outcome.FilesTouched = o.touchedFiles(opts)
	sess.Emitter.EmitError(fmt.Sprintf("stopped after %d rounds without finishing", o.maxRounds))
	return outcome, fmt.Errorf("stopped after %d rounds without finishing", o.maxRounds)
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
		sess.Emitter.EmitToolCall(call.Name, verb, "", "running")

		if taskID := taskIDForTool(call.Name); taskID != "" {
			sess.EmitTaskUpdate(taskID, verb, "in_progress", "")
		}

		output, isError := opts.Execute(ctx, call.Name, call.Input)

		status := "completed"
		if isError {
			status = "error"
		}
		sess.Emitter.EmitToolCall(call.Name, verb, "", status)
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

		results[i] = ai.Block{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   output,
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

	return results
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
			sess.Emitter.EmitToolCall("read_skill", "Applied the "+s.Name+" skill", "", "completed")
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

// runGate runs one gate and classifies its outcome three ways: pass and skip both
// let the ladder continue (ok), while a real failure stops it with feedback. A
// gate whose tool could not run, or whose output does not parse, is skipped rather
// than failed — a missing or broken checker is a platform problem, and blocking a
// build on it would punish the agent for code that may be perfectly correct. It
// parses the JSON verdict rather than treating non-empty output as failure, since
// these tools print banners on success too.
func (o *Orchestrator) runGate(ctx context.Context, sess *SessionState, opts RunOptions, g verifyGate) verifyVerdict {
	sess.Emitter.EmitVerify(g.stage, "running", "")
	raw, isError := opts.Execute(ctx, g.tool, json.RawMessage(`{}`))

	if isError {
		sess.Emitter.EmitVerify(g.stage, "skip", firstLine(raw))
		return verifyVerdict{ok: true}
	}

	var vr verifyResult
	if err := json.Unmarshal([]byte(raw), &vr); err != nil {
		sess.Emitter.EmitVerify(g.stage, "skip", "unreadable "+g.stage+" output")
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
		"to confirm.")
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

// WriteADR records an architecture decision for the project.
func (o *Orchestrator) WriteADR(projectID int64, decision, context, options, outcome string) error {
	_, err := o.store.CreateADR(projectID, decision, context, options, outcome)
	return err
}

// WriteMemory adds an entry to the project's memory.
func (o *Orchestrator) WriteMemory(projectID int64, entryType, content, metadata string) error {
	return o.store.AddMemory(projectID, entryType, content, metadata)
}
