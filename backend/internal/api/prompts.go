package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/tools"
)

type PromptHandler struct {
	store        *store.Store
	ai           *ai.Client
	workspaceDir string
	orch         *orchestrator.Orchestrator
	devClient    *devclient.Manager
	knowledge    *agent.Registry
}

func NewPromptHandler(s *store.Store, a *ai.Client, workspaceDir string, orch *orchestrator.Orchestrator, dc *devclient.Manager, knowledge *agent.Registry) *PromptHandler {
	return &PromptHandler{
		store:        s,
		ai:           a,
		workspaceDir: workspaceDir,
		orch:         orch,
		devClient:    dc,
		knowledge:    knowledge,
	}
}

type promptRequest struct {
	ProjectID int64  `json:"project_id"`
	Prompt    string `json:"prompt"`
	Mode      string `json:"mode"`
}

const maxPromptLength = 50_000

// historyTurns is how many stored turns are replayed into a new request. The
// model also compacts on its own once the context gets large; this bound just
// keeps the query and the JSON decode cheap.
const historyTurns = 60

// promptLimitFor returns the monthly prompt allowance for a tier. -1 is
// unlimited. Previously every user was checked against a hardcoded 20 and the
// tier column was ignored entirely.
func promptLimitFor(tier string) int {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "pro":
		return 2000
	case "team", "enterprise", "unlimited":
		return -1
	default:
		return 20
	}
}

// checkQuota validates ownership-independent limits and returns false having
// already written the response.
func (h *PromptHandler) checkQuota(w http.ResponseWriter, userID int64) bool {
	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return false
	}
	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not check your usage")
		return false
	}
	limit := promptLimitFor(user.Tier)
	if limit >= 0 && usage.PromptsUsed >= limit {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf(
			"You have used all %d prompts for this month. Upgrade your plan to keep building.", limit))
		return false
	}
	return true
}

// validate checks the request shape and the caller's right to touch the project.
func (h *PromptHandler) validate(w http.ResponseWriter, r *http.Request, req *promptRequest) bool {
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return false
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return false
	}
	if len(req.Prompt) > maxPromptLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("that prompt is %d characters; the limit is %d", len(req.Prompt), maxPromptLength))
		return false
	}
	if req.Mode != "plan" && req.Mode != "build" {
		writeError(w, http.StatusBadRequest, "mode must be 'plan' or 'build'")
		return false
	}
	if req.ProjectID <= 0 {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return false
	}
	if _, ok := h.ownedProject(w, r, req.ProjectID); !ok {
		return false
	}
	return h.checkQuota(w, GetUserID(r.Context()))
}

// ownedProject loads a project and confirms the caller owns it. A project
// someone else owns is reported as missing rather than forbidden, so the
// endpoint does not confirm that an id exists.
func (h *PromptHandler) ownedProject(w http.ResponseWriter, r *http.Request, projectID int64) (*store.Project, bool) {
	project, err := h.store.GetProject(projectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// ── Non-streaming ─────────────────────────────────────

// Create runs one turn and returns the whole result at once. The streaming
// endpoint is what the IDE uses; this exists for scripted callers.
func (h *PromptHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req promptRequest
	if !h.validate(w, r, &req) {
		return
	}

	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the prompt")
		return
	}

	// The request context, not Background: if the caller hangs up there is no
	// reason to keep spending tokens on their behalf.
	ctx := r.Context()

	if req.Mode == "plan" {
		planResp, err := h.ai.Plan(ctx, h.planHistory(req.ProjectID, req.Prompt))
		if err != nil {
			log.Printf("plan failed (user %d, project %d): %v", userID, req.ProjectID, err)
			writeError(w, http.StatusBadGateway, friendlyAIError(err))
			return
		}
		h.savePlan(req.ProjectID, planResp)

		respBytes, _ := json.Marshal(planResp)
		h.store.UpdatePromptResponse(prompt.ID, string(respBytes))
		h.store.IncrementPrompts(userID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":       prompt.ID,
			"mode":     "plan",
			"response": json.RawMessage(respBytes),
		})
		return
	}

	outcome, buildErr := h.runBuild(ctx, req.ProjectID, prompt.ID, userID, req.Prompt, nil)

	response := map[string]interface{}{
		"id":            prompt.ID,
		"mode":          "build",
		"message":       "",
		"files_touched": []string{},
		"verified":      false,
		"rounds":        0,
	}
	if outcome != nil {
		response["message"] = outcome.FinalText
		response["files_touched"] = outcome.FilesTouched
		response["verified"] = outcome.Verified
		response["rounds"] = outcome.Rounds
		response["usage"] = outcome.Usage
	}

	if buildErr != nil {
		log.Printf("build failed (user %d, project %d): %v", userID, req.ProjectID, buildErr)
		response["error"] = friendlyAIError(buildErr)
		writeJSON(w, http.StatusOK, response)
		return
	}

	writeJSON(w, http.StatusOK, response)
}

// ── Streaming ─────────────────────────────────────────

// Stream runs one turn and reports progress as server-sent events.
func (h *PromptHandler) Stream(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req promptRequest
	if !h.validate(w, r, &req) {
		return
	}

	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the prompt")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "this server cannot stream responses")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	if req.Mode == "plan" {
		h.streamPlan(ctx, w, flusher, prompt.ID, req.ProjectID, req.Prompt)
		h.store.IncrementPrompts(userID)
		return
	}

	h.streamBuild(ctx, w, flusher, req, prompt.ID, userID)
}

// streamPlan proxies the model's stream to the client, then parses the finished
// text into a plan.
func (h *PromptHandler) streamPlan(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, promptID, projectID int64, userPrompt string) {
	// The system prompt was previously omitted here while the output was still
	// parsed as a plan, so plan mode had no instructions at all.
	stream := h.ai.ChatStream(ctx, ai.SystemPromptPlan, h.planHistory(projectID, userPrompt))

	var full strings.Builder
	sawError := false

	for event := range stream {
		switch event.Type {
		case "thinking.start", "thinking.delta", "thinking.end":
			// Plan replies are JSON; the model's private reasoning is not the
			// reply. Streaming it verbatim dumped raw chain-of-thought into the
			// chat. Nothing is forwarded here — the parsed plan carries the
			// message the user actually reads.
		case "text.start", "text.end":
			// The streamed text is the raw JSON envelope, not something the user
			// should watch form. It is buffered below and reaches the client only
			// as the parsed plan/message.
		case "text.delta":
			if td, ok := event.Data.(ai.StreamTextDelta); ok {
				full.WriteString(td.Text)
			}
		case "error":
			sawError = true
			writeSSE(w, "error", map[string]string{"message": event.Error})
			h.store.UpdatePromptResponse(promptID, "Error: "+event.Error)
		}
		flusher.Flush()
	}

	if sawError || ctx.Err() != nil {
		writeSSE(w, "done", map[string]string{"status": "error"})
		flusher.Flush()
		return
	}

	// The model is asked for JSON. Send the human-readable message to the client
	// as its own event so the UI never has to display raw JSON, and hand over the
	// structured plan alongside it.
	raw := ai.StripCodeBlocks(full.String())
	var planResp ai.PlanResponse
	if json.Unmarshal([]byte(raw), &planResp) == nil && (planResp.Message != "" || planResp.Plan != nil) {
		h.savePlan(projectID, &planResp)
		payload, _ := json.Marshal(planResp)
		h.store.UpdatePromptResponse(promptID, string(payload))
		writeSSE(w, "plan", planResp)
	} else if raw != "" {
		h.store.UpdatePromptResponse(promptID, raw)
		// The model did not return a parseable plan. Surface its reply verbatim
		// rather than leaving the chat staring at a blank turn.
		writeSSE(w, "text.delta", ai.StreamTextDelta{Text: raw})
	}

	writeSSE(w, "done", map[string]string{"status": "success"})
	flusher.Flush()
}

// streamBuild runs the agent loop while forwarding its events to the client.
func (h *PromptHandler) streamBuild(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, req promptRequest, promptID, userID int64) {
	type result struct {
		outcome *orchestrator.BuildOutcome
		err     error
	}
	resultCh := make(chan result, 1)
	emitterCh := make(chan *events.SessionEmitter, 1)

	go func() {
		outcome, err := h.runBuild(ctx, req.ProjectID, promptID, userID, req.Prompt, emitterCh)
		resultCh <- result{outcome, err}
	}()

	// runBuild publishes its emitter as soon as the session exists.
	var emitter *events.SessionEmitter
	select {
	case emitter = <-emitterCh:
	case res := <-resultCh:
		// Failed before the session was created.
		h.finishBuildStream(w, flusher, res.outcome, res.err)
		return
	case <-ctx.Done():
		return
	}

	var res result
	var got bool

drain:
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-emitter.Events():
			h.writeBuildEvent(w, evt)
			flusher.Flush()
		case res = <-resultCh:
			got = true
			break drain
		}
	}

	// The loop has returned but events queued just before it finished are still
	// buffered. Deliver them before the terminal event.
	for emitter.Pending() > 0 {
		select {
		case evt := <-emitter.Events():
			h.writeBuildEvent(w, evt)
		default:
		}
	}
	flusher.Flush()

	if got {
		h.finishBuildStream(w, flusher, res.outcome, res.err)
	}
}

func (h *PromptHandler) finishBuildStream(w http.ResponseWriter, flusher http.Flusher, outcome *orchestrator.BuildOutcome, err error) {
	if err != nil {
		writeSSE(w, "error", map[string]string{"message": friendlyAIError(err)})
	}
	payload := map[string]interface{}{"status": "success"}
	if err != nil {
		payload["status"] = "failed"
	}
	if outcome != nil {
		payload["message"] = outcome.FinalText
		payload["files_touched"] = outcome.FilesTouched
		payload["verified"] = outcome.Verified
		payload["rounds"] = outcome.Rounds
	}
	writeSSE(w, "done", payload)
	flusher.Flush()
}

// writeBuildEvent maps an internal event onto the SSE vocabulary the IDE knows.
func (h *PromptHandler) writeBuildEvent(w http.ResponseWriter, evt events.Event) {
	switch evt.Type {
	case events.PhaseEvent:
		writeSSE(w, "phase", map[string]interface{}{"phase": evt.Phase, "status": evt.Status})
	case events.ToolCallEvent:
		writeSSE(w, "tool_call", map[string]interface{}{
			"tool": evt.Tool, "path": evt.Path, "name": evt.Name, "status": evt.Status,
		})
	case events.VerifyEvent:
		writeSSE(w, "verify", map[string]interface{}{
			"stage": evt.Tool, "status": evt.Status, "error": evt.Error,
		})
	case events.TextEvent:
		// The agent's prose, forwarded as it works. Previously the build stream
		// carried no text at all and the stored response was the literal string
		// "Build completed".
		writeSSE(w, "text.delta", ai.StreamTextDelta{Text: evt.Text})
	case events.CommandOutputEvent:
		// Live subprocess output from run_command / typecheck / lint / build_apk,
		// so the terminal pane shows what the agent's commands are printing.
		writeSSE(w, "command_output", map[string]string{"text": evt.Text})
	case events.ThinkingEvent:
		writeSSE(w, "thinking.delta", ai.StreamTextDelta{Text: evt.Text})
	case events.UsageEvent:
		writeSSE(w, "usage", evt.Data)
	case events.TaskUpdateEvent:
		writeSSE(w, "task_update", map[string]interface{}{
			"id": evt.Tool, "description": evt.Name, "status": evt.Status, "result": evt.Error,
		})
	case events.RebuildEvent:
		writeSSE(w, "rebuild", map[string]interface{}{"status": evt.Status, "detail": evt.Error})
	case events.GuardrailEvent:
		writeSSE(w, "guardrail_reject", map[string]interface{}{
			"tool": evt.Tool, "status": evt.Status, "error": evt.Error,
		})
	case events.ErrorEvent:
		writeSSE(w, "error", map[string]string{"message": evt.Error})
	case events.DoneEvent:
		// The terminal event is written by finishBuildStream, which knows the
		// outcome. Swallowing it here keeps the client from finishing early.
	}
}

// ── The build itself ──────────────────────────────────

// runBuild executes one agentic turn: replay the stored conversation, run the
// loop, persist what happened, and checkpoint the files it changed.
//
// If emitterCh is non-nil the session's emitter is published to it as soon as
// the session exists, so a streaming caller can start forwarding events.
func (h *PromptHandler) runBuild(
	ctx context.Context,
	projectID, promptID, userID int64,
	userPrompt string,
	emitterCh chan<- *events.SessionEmitter,
) (*orchestrator.BuildOutcome, error) {
	workspacePath, err := h.workspacePath(projectID)
	if err != nil {
		return nil, err
	}

	sess := h.orch.CreateSession(promptID, projectID, workspacePath)
	defer h.orch.RemoveSession(promptID)
	defer sess.Emitter.Close()

	if emitterCh != nil {
		emitterCh <- sess.Emitter
	} else {
		// No streaming client (non-SSE build request). The emitter's channel is
		// never closed and Emit blocks once the buffer fills, so someone has to
		// keep draining it or a verbose command output can stall the build.
		go func() {
			for {
				select {
				case <-sess.Emitter.Done():
					return
				case <-sess.Emitter.Events():
				}
			}
		}()
	}

	// The output writer mirrors the sink for the command-running tools, so a
	// tool's subprocess output appears in the terminal pane as it happens.
	bs := h.newBuildSession(workspacePath, projectID, h.adbTargetFor(projectID), commandOutputWriter{emitter: sess.Emitter})

	history := h.conversation(projectID)
	userTurn := ai.UserText(userPrompt)
	history = append(history, userTurn)

	outcome, runErr := h.orch.RunAgentLoop(ctx, sess, orchestrator.RunOptions{
		SystemPrompt: h.systemPrompt(),
		History:      history,
		Tools:        bs.defs,
		Execute:      bs.executor(),
		ReadOnlyTool: bs.reg.IsReadOnly,
		FilesTouched: bs.fs.Touched,
		Exists:       bs.fs.Exists,
	})

	// Everything below runs whether or not the loop succeeded: a failed turn
	// still changed files and still cost tokens, and losing that record is how
	// the next turn ends up contradicting this one.
	h.persistTurn(projectID, promptID, userID, userTurn, outcome)
	h.checkpoint(projectID, promptID, userPrompt, bs.fs.Journal())

	if outcome != nil && len(outcome.FilesTouched) > 0 {
		h.checkNativeRebuild(ctx, projectID, workspacePath, outcome.FilesTouched, sess)
	}

	return outcome, runErr
}

// persistTurn records the turn's messages, its final text and its token cost.
func (h *PromptHandler) persistTurn(projectID, promptID, userID int64, userTurn ai.Message, outcome *orchestrator.BuildOutcome) {
	if err := h.store.AppendMessage(projectID, promptID, userTurn.Role,
		ai.MarshalBlocks(userTurn.Blocks), 0, 0); err != nil {
		log.Printf("project %d: could not store the user turn: %v", projectID, err)
	}

	if outcome == nil {
		return
	}

	for _, m := range outcome.Messages {
		if err := h.store.AppendMessage(projectID, promptID, m.Role,
			ai.MarshalBlocks(m.Blocks), 0, 0); err != nil {
			log.Printf("project %d: could not store a turn: %v", projectID, err)
			break
		}
	}

	response := outcome.FinalText
	if response == "" {
		if len(outcome.FilesTouched) > 0 {
			response = "Changed: " + strings.Join(outcome.FilesTouched, ", ")
		} else {
			response = "No changes were made."
		}
	}
	h.store.UpdatePromptResponse(promptID, response)
	h.store.RecordPromptTokens(promptID, userID,
		outcome.Usage.InputTokens, outcome.Usage.OutputTokens, outcome.Usage.CacheReadTokens)
	h.store.IncrementPrompts(userID)

	// Keep the stored conversation bounded. The tail is what matters for
	// continuity; the whole history would eventually dominate the database.
	h.store.TrimConversation(projectID, 400)
}

// checkpoint stores the pre-change content of every file the turn touched, so
// the change can be undone afterwards.
func (h *PromptHandler) checkpoint(projectID, promptID int64, label string, journal []tools.FileSnapshot) {
	if len(journal) == 0 {
		return
	}
	files := make([]store.CheckpointFile, 0, len(journal))
	for _, s := range journal {
		files = append(files, store.CheckpointFile{Path: s.Path, Content: s.Content, Existed: s.Existed})
	}
	if _, err := h.store.CreateCheckpoint(projectID, promptID, truncateLabel(label, 160), files); err != nil {
		log.Printf("project %d: could not save a checkpoint: %v", projectID, err)
		return
	}
	h.store.PruneCheckpoints(projectID, 30)
}

func truncateLabel(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// conversation replays the project's stored turns.
func (h *PromptHandler) conversation(projectID int64) []ai.Message {
	stored, err := h.store.GetConversation(projectID, historyTurns)
	if err != nil {
		log.Printf("project %d: could not load the conversation: %v", projectID, err)
		return nil
	}

	msgs := make([]ai.Message, 0, len(stored))
	for _, m := range stored {
		blocks := ai.UnmarshalBlocks(m.Blocks)
		if len(blocks) == 0 {
			continue
		}
		msgs = append(msgs, ai.Message{Role: m.Role, Blocks: blocks})
	}

	// A replayed conversation must not begin with tool results or end mid tool
	// call — both providers reject an orphaned tool_use or tool_result.
	return ai.TrimToValidHistory(msgs)
}

// planHistory builds the flat history Plan mode uses. Plan mode calls no tools,
// so only the prose turns are relevant.
func (h *PromptHandler) planHistory(projectID int64, userPrompt string) []ai.PromptMessage {
	var history []ai.PromptMessage

	if summary := h.memorySummary(projectID); summary != "" {
		history = append(history, ai.PromptMessage{
			Role:    "user",
			Content: "Context on this project so far:\n\n" + summary,
		})
		history = append(history, ai.PromptMessage{
			Role:    "assistant",
			Content: "Got it — I have the current state of the project in mind.",
		})
	}

	for _, m := range h.conversation(projectID) {
		if text := strings.TrimSpace(m.Text()); text != "" {
			history = append(history, ai.PromptMessage{Role: m.Role, Content: text})
		}
	}

	return append(history, ai.PromptMessage{Role: "user", Content: userPrompt})
}

// systemPrompt returns the build-mode system prompt plus the native-module
// allowlist, when the platform has one.
func (h *PromptHandler) systemPrompt() string {
	prompt := ai.SystemPromptBuild
	if h.devClient != nil {
		if al := h.devClient.Allowlist(); al != nil {
			prompt += "\n\n" + al.FormatForPrompt()
		}
	}
	return prompt
}

// ── Plan persistence ──────────────────────────────────

// savePlan stores the plan on the project. The plan itself is the durable
// record; writing one memory row per screen duplicated it and then went stale as
// soon as the app changed.
func (h *PromptHandler) savePlan(projectID int64, resp *ai.PlanResponse) {
	if resp == nil || resp.Plan == nil {
		return
	}
	planJSON, err := json.Marshal(resp.Plan)
	if err != nil {
		return
	}
	if err := h.store.UpdateProjectPlan(projectID, string(planJSON)); err != nil {
		log.Printf("project %d: could not save the plan: %v", projectID, err)
	}
}

// ── Read endpoints ────────────────────────────────────

func (h *PromptHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid prompt id")
		return
	}

	prompt, err := h.store.GetPrompt(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	// A prompt is readable only through the project that owns it.
	if _, ok := h.ownedProject(w, r, prompt.ProjectID); !ok {
		return
	}

	writeJSON(w, http.StatusOK, prompt)
}

func (h *PromptHandler) GetMemory(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}

	memories, err := h.store.GetAllMemory(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load project memory")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories": memories,
		"summary":  h.memorySummary(projectID),
	})
}

// GetConversation returns the stored turns for a project so the IDE can restore
// the chat after a reload.
func (h *PromptHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}

	stored, err := h.store.GetConversation(projectID, historyTurns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the conversation")
		return
	}

	type turn struct {
		Role   string     `json:"role"`
		Blocks []ai.Block `json:"blocks"`
		At     string     `json:"at"`
	}
	out := make([]turn, 0, len(stored))
	for _, m := range stored {
		out = append(out, turn{Role: m.Role, Blocks: ai.UnmarshalBlocks(m.Blocks), At: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"turns": out})
}

// ClearConversation forgets the stored turns for a project, starting the chat
// fresh without touching the code.
func (h *PromptHandler) ClearConversation(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	if err := h.store.ClearConversation(projectID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not clear the conversation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// ── Checkpoints ───────────────────────────────────────

// ListCheckpoints returns the undo points for a project, newest first.
func (h *PromptHandler) ListCheckpoints(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}

	checkpoints, err := h.store.ListCheckpoints(projectID, 30)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load checkpoints")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"checkpoints": checkpoints})
}

// RestoreCheckpoint puts the workspace back to the state a checkpoint captured.
func (h *PromptHandler) RestoreCheckpoint(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}

	checkpointID, err := strconv.ParseInt(chi.URLParam(r, "checkpointID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid checkpoint id")
		return
	}

	cp, err := h.store.GetCheckpoint(checkpointID)
	if err != nil {
		writeError(w, http.StatusNotFound, "checkpoint not found")
		return
	}
	// A checkpoint id from another project must not restore into this one.
	if cp.ProjectID != projectID {
		writeError(w, http.StatusNotFound, "checkpoint not found")
		return
	}

	workspacePath, err := h.workspacePath(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not open the workspace")
		return
	}

	snaps := make([]tools.FileSnapshot, 0, len(cp.Files))
	for _, f := range cp.Files {
		snaps = append(snaps, tools.FileSnapshot{Path: f.Path, Content: f.Content, Existed: f.Existed})
	}

	if err := tools.NewFS(workspacePath).Restore(snaps); err != nil {
		writeError(w, http.StatusInternalServerError, "could not restore every file: "+err.Error())
		return
	}

	paths := make([]string, 0, len(cp.Files))
	for _, f := range cp.Files {
		paths = append(paths, f.Path)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "restored",
		"restored": paths,
	})
}

// ── Native rebuild ────────────────────────────────────

// checkNativeRebuild notices when a turn added a dependency with native code and
// kicks off a background dev-client rebuild. The installed client keeps working
// while that runs; if the rebuild fails, the last known good APK goes back on.
func (h *PromptHandler) checkNativeRebuild(ctx context.Context, projectID int64, workspacePath string, changed []string, sess *orchestrator.SessionState) {
	if h.devClient == nil {
		return
	}

	pkgChange, err := devclient.DetectPackageChanges(workspacePath, changed)
	if err != nil || pkgChange == nil || len(pkgChange.Added) == 0 {
		return
	}

	needsRebuild, reason := h.devClient.CheckRebuildNeeded(workspacePath, pkgChange.Added)
	if !needsRebuild {
		return
	}

	adbTarget := h.adbTargetFor(projectID)
	if adbTarget == "" {
		// Nothing to install onto. Tell the user rather than silently skipping:
		// their next preview would otherwise crash on a missing native module.
		sess.Emitter.EmitRebuild("deferred", reason+" — start a device to pick up the new native module")
		return
	}

	log.Printf("project %d: native rebuild needed: %s", projectID, reason)
	sess.Emitter.EmitRebuild("needed", reason)

	// The rebuild outlives this request, so it gets its own context. Using
	// r.Context() here would kill the rebuild the moment the client disconnected
	// and leave the device on a dev client that cannot load the new module.
	rebuildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)

	h.devClient.StartRebuild(rebuildCtx, projectID, workspacePath, adbTarget)
	sess.Emitter.EmitRebuild("started", "rebuilding the dev client in the background: "+reason)

	go func() {
		defer cancel()
		h.watchRebuild(rebuildCtx, projectID, adbTarget, sess)
	}()
}

// watchRebuild polls a running rebuild and reports how it ends.
func (h *PromptHandler) watchRebuild(ctx context.Context, projectID int64, adbTarget string, sess *orchestrator.SessionState) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sess.Emitter.Done():
			// Nobody is listening any more. The rebuild itself continues; only
			// the reporting stops.
			return
		case <-ticker.C:
		}

		state := h.devClient.GetRebuildState(projectID)
		if state == nil {
			return
		}

		switch state.Status {
		case "success":
			sess.Emitter.EmitRebuild("success", "installed a new dev client: "+filepath.Base(state.NewAPK))
			log.Printf("project %d: native rebuild succeeded", projectID)
			h.devClient.ClearRebuild(projectID)
			return

		case "failed":
			sess.Emitter.EmitRebuild("failed", "rebuild failed: "+state.Error)
			log.Printf("project %d: native rebuild failed: %s", projectID, state.Error)

			if state.LastGoodAPK != "" {
				if err := h.devClient.InstallDevClient(ctx, adbTarget); err != nil {
					sess.Emitter.EmitRebuild("fallback_failed", "could not reinstall the previous dev client: "+err.Error())
					log.Printf("project %d: fallback reinstall failed: %v", projectID, err)
				} else {
					sess.Emitter.EmitRebuild("fallback_success", "restored the previous dev client")
				}
			}

			h.devClient.ClearRebuild(projectID)
			return
		}
	}
}

// ── Helpers ───────────────────────────────────────────

func (h *PromptHandler) workspacePath(projectID int64) (string, error) {
	if h.workspaceDir == "" {
		return "", fmt.Errorf("no workspace directory is configured on this server")
	}
	dir := filepath.Join(h.workspaceDir, strconv.FormatInt(projectID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not open the project workspace: %w", err)
	}
	return dir, nil
}

// memorySummary condenses project memory for a prompt.
func (h *PromptHandler) memorySummary(projectID int64) string {
	memories, err := h.store.GetAllMemory(projectID)
	if err != nil || len(memories) == 0 {
		return ""
	}

	const maxEntries = 25
	if len(memories) > maxEntries {
		memories = memories[len(memories)-maxEntries:]
	}

	var b strings.Builder
	for _, m := range memories {
		content := strings.Join(strings.Fields(m.Content), " ")
		if len(content) > 240 {
			content = content[:240] + "…"
		}
		fmt.Fprintf(&b, "- [%s] %s\n", m.EntryType, content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeSSE(w http.ResponseWriter, eventType string, data interface{}) {
	payload := []byte("{}")
	if data != nil {
		if encoded, err := json.Marshal(data); err == nil {
			payload = encoded
		}
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
}

// friendlyAIError turns a provider or loop failure into something worth showing
// a user, without hiding what actually happened.
func friendlyAIError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "429"), strings.Contains(msg, "rate limit"):
		return "The AI service is rate limiting us right now. Wait a moment and try again."
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return "The AI service rejected our credentials. Check the server's AI_API_KEY."
	case strings.Contains(msg, "502"), strings.Contains(msg, "503"):
		return "The AI service is unavailable right now. This usually clears in a minute."
	case strings.Contains(msg, "504"), strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"):
		return "The AI service took too long to respond. Try again, or break the request into smaller steps."
	case strings.Contains(msg, "cancelled"), strings.Contains(msg, "context canceled"):
		return "The build was cancelled."
	case strings.Contains(msg, "without finishing"):
		return "The build ran out of rounds before finishing. Try a narrower request — one screen or one feature at a time."
	case strings.Contains(msg, "does not compile"):
		return msg
	default:
		return msg
	}
}
