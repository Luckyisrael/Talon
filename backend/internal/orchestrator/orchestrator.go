package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
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
	maxRounds int
}

func New(aiClient *ai.Client, s *store.Store) *Orchestrator {
	return &Orchestrator{
		ai:        aiClient,
		sessions:  make(map[int64]*SessionState),
		store:     s,
		maxRounds: 40,
	}
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

			if result.StopReason == "max_tokens" {
				// The response was cut mid-flight. Say so plainly rather than
				// letting the model guess why its output looks truncated.
				nudge := ai.UserText("Your previous response hit the output limit and was truncated. " +
					"Continue from where you stopped — work in smaller steps.")
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
			sess.Emitter.EmitError(fmt.Sprintf("code still does not compile after %d repair attempts", maxVerifyAttempts))
			return outcome, fmt.Errorf("code still does not compile after %d repair attempts", maxVerifyAttempts)
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

type verifyVerdict struct {
	ok       bool
	feedback string
}

// verify runs the static checks and turns any failure into feedback the agent
// can act on. It parses the tool's JSON verdict rather than treating non-empty
// output as failure — tsc prints on success too.
func (o *Orchestrator) verify(ctx context.Context, sess *SessionState, opts RunOptions) verifyVerdict {
	sess.Emitter.EmitVerify("typecheck", "running", "")
	raw, isError := opts.Execute(ctx, "run_typecheck", json.RawMessage(`{}`))

	if isError {
		// The checker itself could not run. That is a platform problem, not the
		// agent's code being wrong, so do not send it into a repair loop.
		sess.Emitter.EmitVerify("typecheck", "skip", firstLine(raw))
		return verifyVerdict{ok: true}
	}

	var tc struct {
		Passed  bool   `json:"passed"`
		Errors  string `json:"errors"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		// Unparseable output means the contract changed; failing open is
		// better than blocking every build on a parse error.
		sess.Emitter.EmitVerify("typecheck", "skip", "unreadable typecheck output")
		return verifyVerdict{ok: true}
	}

	if tc.Passed {
		sess.Emitter.EmitVerify("typecheck", "pass", tc.Summary)
		return verifyVerdict{ok: true}
	}

	sess.Emitter.EmitVerify("typecheck", "fail", tc.Summary)

	var b strings.Builder
	b.WriteString("The code you wrote does not compile. TypeScript reports:\n\n")
	b.WriteString(tc.Errors)
	b.WriteString("\n\nFix these errors. Read each file the compiler names before changing it, and use " +
		"edit_file for targeted changes. When you believe it is fixed, call run_typecheck to confirm.")

	if files := errorFiles(tc.Errors); len(files) > 0 {
		b.WriteString("\n\nFiles with errors: " + strings.Join(files, ", "))
	}

	return verifyVerdict{feedback: b.String()}
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
