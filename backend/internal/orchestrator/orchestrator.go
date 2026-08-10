package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/store"
)

type Orchestrator struct {
	ai         *ai.Client
	sessions   map[int64]*SessionState
	store      *store.Store
	maxRounds  int
}

func New(aiClient *ai.Client, s *store.Store) *Orchestrator {
	return &Orchestrator{
		ai:        aiClient,
		sessions:  make(map[int64]*SessionState),
		store:     s,
		maxRounds: 15,
	}
}

func (o *Orchestrator) CreateSession(id, projectID int64, workspacePath string) *SessionState {
	stagingPath := filepath.Join(workspacePath, ".staging")
	emitter := events.NewSessionEmitter(id, 128)
	sess := NewSession(id, projectID, workspacePath, stagingPath, emitter)
	o.sessions[id] = sess

	// Load ADRs and memory for this project
	o.loadProjectContext(sess, projectID)

	return sess
}

func (o *Orchestrator) loadProjectContext(sess *SessionState, projectID int64) {
	// Load Architecture Decision Records
	adrs, err := o.store.GetActiveADRs(projectID)
	if err == nil {
		sess.LoadADRs(adrs)
	}

	// Load project memory
	memory, err := o.store.GetAllMemory(projectID)
	if err == nil {
		sess.LoadMemory(memory)
	}
}

func (o *Orchestrator) GetSession(id int64) *SessionState {
	return o.sessions[id]
}

func (o *Orchestrator) RemoveSession(id int64) {
	if sess, ok := o.sessions[id]; ok {
		sess.Emitter.Close()
		sess.Staging.Cleanup()
		delete(o.sessions, id)
	}
}

func (o *Orchestrator) RunBuild(ctx context.Context, sess *SessionState, userPrompt string) error {
	sess.TransitionTo(PhaseContextBuild)
	o.buildContext(sess, userPrompt)

	sess.TransitionTo(PhaseGenerate)
	history := o.buildHistory(sess, userPrompt)

	if err := o.runGenerateLoop(ctx, sess, history); err != nil {
		return err
	}

	return nil
}

func (o *Orchestrator) RunBuildWithTools(ctx context.Context, sess *SessionState, userPrompt, systemPrompt string, initialHistory []ai.PromptMessage, tools []ai.ToolDefinition, toolExecutor func(ctx context.Context, name string, input json.RawMessage) (string, error)) error {
	sess.TransitionTo(PhaseContextBuild)
	sess.Emitter.EmitToolCall("context_build", "", "", "completed")

	sess.TransitionTo(PhaseGenerate)

	// Phase 7: Inject ADR and memory into context
	systemPrompt = o.enrichPromptWithContext(sess, systemPrompt)

	history := make([]ai.PromptMessage, len(initialHistory))
	copy(history, initialHistory)

	// Clear read tracking for new turn
	sess.ClearReadFiles()

	round := 0
	for round < o.maxRounds {
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled")
		}

		round++
		sess.Emitter.EmitPhase(events.Phase(sess.GetPhase()), fmt.Sprintf("round %d", round))

		result, err := o.ai.ChatWithTools(ctx, systemPrompt, history, tools)
		if err != nil {
			return fmt.Errorf("AI generation failed: %w", err)
		}

		if len(result.ToolUses) == 0 {
			// Check if any files were staged — if so, we're done
			staged, _ := sess.Staging.ListFiles()
			if len(staged) > 0 {
				sess.TransitionTo(PhaseDone)
				sess.Emitter.EmitDone("success")
				return nil
			}
			// No files generated and no tool calls — nudge and retry
			if result.Text != "" {
				history = append(history, ai.PromptMessage{
					Role:    "assistant",
					Content: result.Text,
				})
			}
			history = append(history, ai.PromptMessage{
				Role:    "user",
				Content: "You returned text but did not use any tools to write files. You MUST use the write_file tool to create the actual code files. Do not just describe what to write — actually write the files using write_file tool. Try again.",
			})
			sess.Emitter.EmitToolCall("guardrail", "", "", "no files generated, retrying with nudge")
			continue
		}

		assistantContent, _ := json.Marshal(result.ToolUses)
		history = append(history, ai.PromptMessage{
			Role:    "assistant",
			Content: string(assistantContent),
		})

		var toolResults []ai.ToolResultBlock
		var wroteFiles bool
		for _, toolUse := range result.ToolUses {
			// Phase 7: Emit specific verb based on tool type
			verb := o.getToolVerb(toolUse.Name, toolUse.Input)
			sess.Emitter.EmitToolCall(toolUse.Name, verb, "", "running")

			// Phase 7: Emit task update for key operations
			if taskID := o.getTaskIDForTool(toolUse.Name); taskID != "" {
				sess.EmitTaskUpdate(taskID, verb, "in_progress", "")
			}

			output, execErr := toolExecutor(ctx, toolUse.Name, toolUse.Input)
			isError := execErr != nil
			if output == "" && execErr != nil {
				output = execErr.Error()
			}

			status := "completed"
			if isError {
				status = "error"
			}
			sess.Emitter.EmitToolCall(toolUse.Name, verb, "", status)

			// Phase 7: Complete task for key operations
			if taskID := o.getTaskIDForTool(toolUse.Name); taskID != "" {
				result := "success"
				if isError {
					result = execErr.Error()
				}
				sess.EmitTaskUpdate(taskID, verb, "completed", result)
			}

			// Phase 7: Track file reads for read-before-write enforcement
			if toolUse.Name == "read_file" {
				var readArgs struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(toolUse.Input, &readArgs) == nil {
					sess.MarkFileRead(readArgs.Path)
				}
			}

			// Phase 7: Check read-before-write for write_file
			if toolUse.Name == "write_file" {
				wroteFiles = true
				var writeArgs struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(toolUse.Input, &writeArgs) == nil {
					if !sess.HasReadFile(writeArgs.Path) {
						// Emit warning but don't block (agent may be creating new file)
						sess.Emitter.EmitToolCall("guardrail", writeArgs.Path, "", "read-before-write warning")
					}
				}
			}

			toolResults = append(toolResults, ai.ToolResultBlock{
				Type:      "tool_result",
				ToolUseID: toolUse.ID,
				Content:   output,
				IsError:   isError,
			})
		}

		resultsJSON, _ := json.Marshal(toolResults)
		history = append(history, ai.PromptMessage{
			Role:    "user",
			Content: string(resultsJSON),
		})

		// Check if we're done — either files were written, or model returned text with tool results
		staged, _ := sess.Staging.ListFiles()
		if wroteFiles || len(staged) > 0 {
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			return nil
		}

		// Had tool calls but no write_file — nudge the model
		if result.Text != "" {
			history = append(history, ai.PromptMessage{
				Role:    "assistant",
				Content: result.Text,
			})
		}
		history = append(history, ai.PromptMessage{
			Role:    "user",
			Content: "You used tools but did not call write_file. You MUST use the write_file tool to create the actual code files. Do not just read files or list directories — write the code. Try again.",
		})
		sess.Emitter.EmitToolCall("guardrail", "", "", "no files written, retrying with nudge")
	}

	sess.TransitionTo(PhaseFailed)
	sess.Emitter.EmitError(fmt.Sprintf("exceeded max rounds (%d)", o.maxRounds))
	return fmt.Errorf("exceeded max rounds (%d)", o.maxRounds)
}

// enrichPromptWithContext adds ADR and memory context to the system prompt.
func (o *Orchestrator) enrichPromptWithContext(sess *SessionState, systemPrompt string) string {
	var enriched strings.Builder
	enriched.WriteString(systemPrompt)

	// Add ADR context
	if adrs := sess.GetADRs(); len(adrs) > 0 {
		enriched.WriteString("\n\n## Architecture Decisions (MUST follow)\n\n")
		for _, adr := range adrs {
			enriched.WriteString(fmt.Sprintf("### %s\n", adr.Decision))
			enriched.WriteString(fmt.Sprintf("Context: %s\n", adr.Context))
			if adr.Options != "" {
				enriched.WriteString(fmt.Sprintf("Options considered: %s\n", adr.Options))
			}
			enriched.WriteString(fmt.Sprintf("Decision: %s\n\n", adr.Outcome))
		}
		enriched.WriteString("IMPORTANT: These decisions are established. Do not introduce conflicting patterns. If a request would conflict with an existing decision, either reconcile it within the existing architecture or flag the conflict to the user.\n")
	}

	// Add project memory context
	if memory := sess.GetMemory(); len(memory) > 0 {
		enriched.WriteString("\n\n## Project Memory\n\n")
		for _, m := range memory {
			enriched.WriteString(fmt.Sprintf("[%s] %s\n", m.EntryType, m.Content))
		}
	}

	return enriched.String()
}

// getToolVerb returns a human-readable verb for the tool call.
func (o *Orchestrator) getToolVerb(toolName string, input json.RawMessage) string {
	switch toolName {
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &args) == nil {
			return fmt.Sprintf("Reading %s", args.Path)
		}
		return "Reading file"
	case "write_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &args) == nil {
			return fmt.Sprintf("Creating %s", args.Path)
		}
		return "Writing file"
	case "delete_file":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(input, &args) == nil {
			return fmt.Sprintf("Deleting %s", args.Path)
		}
		return "Deleting file"
	case "install_package":
		var args struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(input, &args) == nil {
			return fmt.Sprintf("Installing %s", args.Name)
		}
		return "Installing package"
	case "run_typecheck":
		return "Checking types"
	case "run_lint":
		return "Linting"
	case "run_build":
		return "Building"
	case "run_command":
		return "Shell"
	case "web_search":
		return "Searching web"
	case "github_search":
		return "Searching GitHub"
	case "capture_screenshot":
		return "Capturing screenshot"
	case "tail_logcat":
		return "Tailing logcat"
	default:
		return toolName
	}
}

// getTaskIDForTool returns a task ID for tools that should emit task tracking events.
func (o *Orchestrator) getTaskIDForTool(toolName string) string {
	switch toolName {
	case "build_apk":
		return "build-apk"
	case "run_tests":
		return "run-tests"
	case "run_typecheck", "run_lint":
		return "verify"
	case "install_package":
		return "install-deps"
	case "capture_screenshot":
		return "runtime-check"
	default:
		return ""
	}
}

func (o *Orchestrator) buildContext(sess *SessionState, userPrompt string) {
	sess.Emitter.EmitToolCall("context_build", "", "", "started")

	projectContext := o.readProjectContext(sess.WorkspacePath)
	if projectContext != "" {
		sess.Emitter.EmitToolCall("context_build", "", "", "completed")
	} else {
		sess.Emitter.EmitToolCall("context_build", "", "", "no-existing-files")
	}
}

func (o *Orchestrator) readProjectContext(workspacePath string) string {
	var files []string
	keyFiles := []string{
		"App.tsx", "index.js", "package.json", "tsconfig.json",
		"babel.config.js", "polyfill.js", "app.json",
	}

	for _, f := range keyFiles {
		path := filepath.Join(workspacePath, f)
		content, err := readFileContent(path)
		if err == nil {
			files = append(files, fmt.Sprintf("=== %s ===\n%s", f, content))
		}
	}

	if len(files) == 0 {
		return ""
	}

	result := "Existing project files:\n\n"
	for _, f := range files {
		result += f + "\n\n"
	}
	return result
}

func (o *Orchestrator) buildHistory(sess *SessionState, userPrompt string) []ai.PromptMessage {
	history := []ai.PromptMessage{}

	projectContext := o.readProjectContext(sess.WorkspacePath)
	if projectContext != "" {
		history = append(history, ai.PromptMessage{
			Role:    "user",
			Content: projectContext,
		})
		history = append(history, ai.PromptMessage{
			Role:    "assistant",
			Content: "I've reviewed the existing project files. Ready to proceed.",
		})
	}

	history = append(history, ai.PromptMessage{
		Role:    "user",
		Content: userPrompt,
	})

	return history
}

func (o *Orchestrator) runGenerateLoop(ctx context.Context, sess *SessionState, history []ai.PromptMessage) error {
	round := 0

	for round < o.maxRounds {
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled")
		}

		round++

		sess.Emitter.EmitPhase(events.Phase(sess.GetPhase()), fmt.Sprintf("round %d", round))

		buildResp, err := o.ai.Build(ctx, history, sess.WorkspacePath)
		if err != nil {
			return fmt.Errorf("AI generation failed: %w", err)
		}

		if len(buildResp.Files) == 0 {
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("no-files-generated")
			return nil
		}

		sess.TransitionTo(PhaseStageWrite)
		var writtenPaths []string
		for _, file := range buildResp.Files {
			sess.Emitter.EmitToolCall("write_file", file.Path, "", "staging")
			if err := sess.Staging.WriteFile(file.Path, file.Content); err != nil {
				sess.Emitter.EmitToolCall("write_file", file.Path, "", "error: "+err.Error())
				return fmt.Errorf("staging failed for %s: %w", file.Path, err)
			}
			writtenPaths = append(writtenPaths, file.Path)
			sess.Emitter.EmitToolCall("write_file", file.Path, "", "staged")
		}

		sess.RecordPatch(writtenPaths, "")

		if err := o.runVerifyStatic(ctx, sess); err == nil {
			sess.TransitionTo(PhasePromote)
			if err := sess.Staging.PromoteAll(); err != nil {
				sess.Emitter.EmitError("promote failed: " + err.Error())
				return fmt.Errorf("promote failed: %w", err)
			}
			sess.Emitter.EmitPhase(events.PhasePromote, "done")
			sess.ResetHealAttempts()
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			return nil
		} else {
			verifyErr := err.Error()
			sess.RecordPatch(writtenPaths, verifyErr)

			if sess.CanHeal() {
				attempt := sess.IncrementHealAttempt()
				sess.Emitter.EmitPhase(events.PhaseHeal, fmt.Sprintf("attempt %d/%d", attempt, sess.MaxHealAttempts))

				history = append(history, ai.PromptMessage{
					Role:    "assistant",
					Content: fmt.Sprintf("Files written: %v", writtenPaths),
				})
				history = append(history, ai.PromptMessage{
					Role:    "user",
					Content: fmt.Sprintf("The verification failed with these errors:\n\n%s\n\nPlease fix these errors and regenerate the files.", verifyErr),
				})

				sess.TransitionTo(PhaseGenerate)
				continue
			} else {
				sess.TransitionTo(PhaseFailed)
				sess.Emitter.EmitError(fmt.Sprintf("failed after %d heal attempts: %s", sess.MaxHealAttempts, verifyErr))
				return fmt.Errorf("failed after %d heal attempts: %s", sess.MaxHealAttempts, verifyErr)
			}
		}
	}

	sess.TransitionTo(PhaseFailed)
	sess.Emitter.EmitError(fmt.Sprintf("exceeded max rounds (%d)", o.maxRounds))
	return fmt.Errorf("exceeded max rounds (%d)", o.maxRounds)
}

func (o *Orchestrator) runVerifyStatic(ctx context.Context, sess *SessionState) error {
	sess.TransitionTo(PhaseVerifyStatic)
	sess.Emitter.EmitVerify("typecheck", "running", "")

	tscErr := o.runTypecheck(ctx, sess.WorkspacePath)
	if tscErr != "" {
		sess.Emitter.EmitVerify("typecheck", "fail", tscErr)
		return fmt.Errorf("typecheck failed: %s", tscErr)
	}
	sess.Emitter.EmitVerify("typecheck", "pass", "")

	sess.Emitter.EmitVerify("lint", "running", "")
	lintErr := o.runLint(ctx, sess.WorkspacePath)
	if lintErr != "" {
		sess.Emitter.EmitVerify("lint", "fail", lintErr)
		return fmt.Errorf("lint failed: %s", lintErr)
	}
	sess.Emitter.EmitVerify("lint", "pass", "")

	return nil
}

func (o *Orchestrator) runTypecheck(ctx context.Context, workspacePath string) string {
	if workspacePath == "" {
		return ""
	}

	cmd := exec.CommandContext(ctx, "npx", "tsc", "--noEmit")
	cmd.Dir = workspacePath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output)
	}
	return ""
}

func (o *Orchestrator) runLint(ctx context.Context, workspacePath string) string {
	if workspacePath == "" {
		return ""
	}

	cmd := exec.CommandContext(ctx, "npx", "eslint", "--ext", ".ts,.tsx,.js,.jsx", ".")
	cmd.Dir = workspacePath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output)
	}
	return ""
}

// WriteADR records an Architecture Decision Record for the project.
func (o *Orchestrator) WriteADR(projectID int64, decision, context, options, outcome string) error {
	_, err := o.store.CreateADR(projectID, decision, context, options, outcome)
	return err
}

// WriteMemory writes an entry to project memory.
func (o *Orchestrator) WriteMemory(projectID int64, entryType, content, metadata string) error {
	return o.store.AddMemory(projectID, entryType, content, metadata)
}

func readFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
