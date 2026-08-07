package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
)

type Orchestrator struct {
	ai         *ai.Client
	sessions   map[int64]*SessionState
	maxRounds  int
}

func New(aiClient *ai.Client) *Orchestrator {
	return &Orchestrator{
		ai:        aiClient,
		sessions:  make(map[int64]*SessionState),
		maxRounds: 15,
	}
}

func (o *Orchestrator) CreateSession(id, projectID int64, workspacePath string) *SessionState {
	stagingPath := filepath.Join(workspacePath, ".staging")
	emitter := events.NewSessionEmitter(id, 128)
	sess := NewSession(id, projectID, workspacePath, stagingPath, emitter)
	o.sessions[id] = sess
	return sess
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

	history := make([]ai.PromptMessage, len(initialHistory))
	copy(history, initialHistory)

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
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			return nil
		}

		assistantContent, _ := json.Marshal(result.ToolUses)
		history = append(history, ai.PromptMessage{
			Role:    "assistant",
			Content: string(assistantContent),
		})

		var toolResults []ai.ToolResultBlock
		for _, toolUse := range result.ToolUses {
			sess.Emitter.EmitToolCall(toolUse.Name, "", "", "running")

			output, execErr := toolExecutor(ctx, toolUse.Name, toolUse.Input)
			isError := execErr != nil
			if output == "" && execErr != nil {
				output = execErr.Error()
			}

			status := "completed"
			if isError {
				status = "error"
			}
			sess.Emitter.EmitToolCall(toolUse.Name, "", "", status)

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

		if result.Text != "" {
			sess.TransitionTo(PhaseDone)
			sess.Emitter.EmitDone("success")
			return nil
		}
	}

	sess.TransitionTo(PhaseFailed)
	sess.Emitter.EmitError(fmt.Sprintf("exceeded max rounds (%d)", o.maxRounds))
	return fmt.Errorf("exceeded max rounds (%d)", o.maxRounds)
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

func readFileContent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
