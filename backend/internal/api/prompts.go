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
	"github.com/seekersstudio/backend/internal/knowledge"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/metrics"
	"github.com/seekersstudio/backend/internal/harness/approval"
	"github.com/seekersstudio/backend/internal/harness/skills"
	"github.com/seekersstudio/backend/internal/notify"
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
	knowledge    *knowledge.Registry
	// planPrompt is the Plan-mode system prompt, assembled once at startup with the
	// template's preinstalled-package list folded in. Building it here — the one
	// place that holds both the AI client and the knowledge registry — is what keeps
	// the plan and build modes from drifting on which packages already ship.
	planPrompt string
	// slog is the H1 session-log cache: one append-only event log per project,
	// dual-written alongside conversation_messages until the derivation read
	// path is flipped on everywhere.
	slog *sessionLogCache
	// notifier is E: the push notification surface. Fire-and-forget by law;
	// nil disables (tests construct without it).
	notifier notify.Notifier
	// skillCatalog is the H6 multi-source skills catalog, when the
	// composition attached one; read_skill resolves through it.
	skillCatalog *skills.Catalog
}

func NewPromptHandler(s *store.Store, a *ai.Client, workspaceDir string, orch *orchestrator.Orchestrator, dc *devclient.Manager, knowledge *knowledge.Registry, n notify.Notifier) *PromptHandler {
	var preinstalled []string
	if knowledge != nil {
		preinstalled = knowledge.PreinstalledList()
	}
	return &PromptHandler{
		store:        s,
		ai:           a,
		workspaceDir: workspaceDir,
		orch:         orch,
		devClient:    dc,
		knowledge:    knowledge,
		notifier:     n,
		planPrompt:   ai.PlanSystemPrompt(preinstalled),
		slog:         newSessionLogCache(),
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
// unlimited. It reads the shared quota table (quota.go) so the limit we enforce
// here is the same one usage.go reports and the pricing page advertises — this
// used to return Pro=2000 while everything else said 500.
func promptLimitFor(tier string) int {
	return quotaFor(tier).Prompts
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

	// H13: title the turn from its own prompt. The fallback (truncated
	// prompt) is stored synchronously — instant, no model call — and the LLM
	// title upgrades it after the turn, so the title never consumes the
	// turn's first model response or blocks the request.
	prompt, err := h.store.CreateTitledPrompt(req.ProjectID, req.Mode, req.Prompt, h.SessionTitleFallback(req.Prompt))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the prompt")
		return
	}

	// The request context, not Background: if the caller hangs up there is no
	// reason to keep spending tokens on their behalf.
	ctx := r.Context()

	if req.Mode == "plan" {
		planResp, err := h.ai.Plan(ctx, h.planPrompt, h.planHistory(req.ProjectID, req.Prompt))
		if err != nil {
			log.Printf("plan failed (user %d, project %d): %v", userID, req.ProjectID, err)
			h.store.UpdatePromptStatus(prompt.ID, "failed", 0)
			writeError(w, http.StatusBadGateway, friendlyAIError(err))
			return
		}
		h.savePlan(req.ProjectID, planResp)

		respBytes, _ := json.Marshal(planResp)
		h.store.UpdatePromptResponse(prompt.ID, string(respBytes))
		h.store.IncrementPrompts(userID)
		h.persistPlanExchange(req.ProjectID, prompt.ID, req.Prompt, planResp.Message, planJSON(planResp.Plan))
		// A plan reply that asks questions instead of settling a plan is not
		// done — waiting lets the Answer endpoint continue the planning. Same
		// rule as the streaming path; both doors must classify identically.
		status := "completed"
		if planResp.Plan == nil && planAsksQuestion(string(respBytes)) {
			status = "waiting"
		}
		h.store.UpdatePromptStatus(prompt.ID, status, 0)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":       prompt.ID,
			"mode":     "plan",
			"response": json.RawMessage(respBytes),
		})
		return
	}

	outcome, buildErr := h.runBuild(ctx, req.ProjectID, prompt.ID, userID, req.Prompt, nil,
		buildOptions{persistUserTurn: true, meterPrompt: true, mode: req.Mode})

	response := map[string]interface{}{
		"id":              prompt.ID,
		"mode":            "build",
		"message":         "",
		"files_touched":   []string{},
		"verified":        false,
		"rounds":          0,
		"remaining_tasks": []string{},
	}
	if outcome != nil {
		response["message"] = outcome.FinalText
		response["files_touched"] = outcome.FilesTouched
		response["verified"] = outcome.Verified
		response["rounds"] = outcome.Rounds
		response["usage"] = outcome.Usage
		response["remaining_tasks"] = outcome.RemainingTasks
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

	// H13: title the turn from its own prompt — see Create.
	prompt, err := h.store.CreateTitledPrompt(req.ProjectID, req.Mode, req.Prompt, h.SessionTitleFallback(req.Prompt))
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
		h.streamPlan(ctx, w, flusher, prompt.ID, req.ProjectID, userID, req.Prompt)
		h.store.IncrementPrompts(userID)
		return
	}

	h.streamBuild(ctx, w, flusher, req.ProjectID, prompt.ID, userID, req.Prompt,
		buildOptions{persistUserTurn: true, meterPrompt: true, mode: req.Mode})
}

// resumePrompt is the synthetic instruction a resumed turn injects. It is not
// stored and not shown — it only points the model back at the restored
// transcript so it continues the interrupted work instead of restarting it.
const resumePrompt = "Continue from where you left off. Review what you have already done in this turn, finish the work that remains, and don't repeat steps that already completed."

// Resume continues an interrupted build turn. The turn keeps its original
// prompt row and transcript; resuming replays the persisted conversation (which
// already carries every completed round, thanks to incremental persistence) and
// lets the agent keep going, streaming exactly as a fresh turn does.
//
// This is resume-from-persisted-state, not re-attach to a live stream: it is
// valid only once the previous run has ended (crash, disconnect, or Stop). A
// turn still streaming elsewhere is rejected rather than run twice.
func (h *PromptHandler) Resume(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

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

	// A prompt is reachable only through the project that owns it.
	if _, ok := h.ownedProject(w, r, prompt.ProjectID); !ok {
		return
	}

	// Only build turns run an agent loop; a plan turn has nothing to resume.
	if prompt.Mode != "build" {
		writeError(w, http.StatusBadRequest, "only build turns can be resumed")
		return
	}

	// A turn that already finished, or was deliberately discarded, has nothing
	// to continue. The restored context plus the on-disk files let the agent
	// take fresh instructions instead — no resume needed.
	if prompt.Status == "completed" || prompt.Status == "abandoned" {
		writeError(w, http.StatusConflict, "this turn has already finished")
		return
	}

	// A turn still streaming in another request must not be resumed underneath
	// it: two loops against one prompt row and one workspace would corrupt both.
	if h.orch.GetSession(id) != nil {
		writeError(w, http.StatusConflict, "this turn is still running")
		return
	}

	if !h.checkQuota(w, userID) {
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

	// Mark it running again for the duration of the resumed loop; finalizeTurn
	// writes the terminal status when the loop returns.
	h.store.UpdatePromptStatus(id, "running", prompt.Round)

	// A turn interrupted by a server restart lost its in-memory session
	// state (probe counts, cooldowns, the crash index). The transcript and
	// files survived, so the model sees what it did — this note tells it not
	// to trust everything it sees: re-verify completed work before building
	// on it. A graceful pause (budget stop) needs no such caution.
	instruction := resumePrompt
	if prompt.Status == "interrupted" {
		instruction = resumePrompt + "\n\nNote: the server restarted mid-turn, " +
			"so working state (what you had verified, probed, or latched) was lost. " +
			"Re-check the files and the checklist before continuing — do not assume " +
			"work that appears done was actually verified."
	}

	// The synthetic prompt is neither persisted nor metered — resuming an
	// interrupted turn is a continuation, not a new prompt.
	h.streamBuild(r.Context(), w, flusher, prompt.ProjectID, id, userID, instruction,
		buildOptions{persistUserTurn: false, meterPrompt: false, mode: "build"})
}

// answerRequest is the body of POST /v1/prompts/{id}/answer: the user's reply
// to the question a paused ask_user turn asked.
type answerRequest struct {
	Answer string `json:"answer"`
}

// Answer resumes a turn paused on ask_user by supplying the user's reply. The
// reply becomes the tool_result for the pending ask_user call and the loop
// continues from there, streaming exactly as a fresh turn does.
//
// Like Resume this is resume-from-persisted-state, not re-attach to a live
// stream: it is valid only once the paused run has ended and the turn is
// genuinely waiting. A turn in any other status, or one still streaming
// elsewhere, is rejected rather than run twice.
func (h *PromptHandler) Answer(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid prompt id")
		return
	}

	var body answerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	body.Answer = strings.TrimSpace(body.Answer)
	if body.Answer == "" {
		writeError(w, http.StatusBadRequest, "answer is required")
		return
	}
	if len(body.Answer) > maxPromptLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("that answer is %d characters; the limit is %d", len(body.Answer), maxPromptLength))
		return
	}

	prompt, err := h.store.GetPrompt(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	// A prompt is reachable only through the project that owns it.
	if _, ok := h.ownedProject(w, r, prompt.ProjectID); !ok {
		return
	}

	// A turn paused on ask_user is answerable in either mode. Build turns
	// resume the loop with the answer as the pending tool_result; plan turns
	// have no tool call to answer — the reply continues the planning as a new
	// plan request carrying the conversation so far, which is exactly how plan
	// Q&A worked before, minus the dead "only build turns" error.
	if prompt.Status != "waiting" {
		writeError(w, http.StatusConflict, "this turn is not waiting for an answer")
		return
	}

	// A turn still streaming in another request must not be resumed underneath
	// it: two loops against one prompt row and one workspace would corrupt both.
	if h.orch.GetSession(id) != nil {
		writeError(w, http.StatusConflict, "this turn is still running")
		return
	}

	if !h.checkQuota(w, userID) {
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

	// Mark it running again for the duration of the resumed loop; finalizeTurn
	// writes the terminal status when the loop returns.
	h.store.UpdatePromptStatus(id, "running", prompt.Round)

	// An answer that approves a device build (e.g. "yes, build the APK and
	// install it") binds to this prompt's lifecycle: text-scanning guardrails
	// never see tool_result turns, so the approval is recorded explicitly and
	// the build_apk gate reads it. Without this, every approval looped back
	// to another ask_user — the approval spin.
	if orchestrator.WantsDeviceBuild(body.Answer) {
		// H4: the grant routes through the approval seam so the decision is
		// audited to the session log (one ApprovalDecided event per yes) —
		// the same seam every sensitive path will use. The table write stays
		// the binding record the guardrail reads.
		if err := h.store.GrantApproval(prompt.ProjectID, id, store.ScopeDeviceBuild); err != nil {
			log.Printf("WARN: could not record device-build approval for prompt %d: %v", id, err)
		} else {
			if lg := h.slog.get(h.store, prompt.ProjectID); lg != nil {
				audit := approvalAudit(lg, id)
				if audit != nil {
					audit(approval.AuditRecord{
						Scope:    store.ScopeDeviceBuild,
						Action:  "user answered a device-build approval: " + firstLine(body.Answer),
						Outcome: approval.AllowedOnce,
						PromptID: id,
					})
				}
			}
		}
	}

	// The answer is injected as the tool_result for the pending ask_user call, not
	// appended as a fresh user prompt. It is real user input, so it is persisted;
	// it is not metered, because the question and its answer are one prompt,
	// already charged when the turn first ran.
	//
	// Plan turns never had a tool call to answer: the reply continues the
	// planning, streaming a fresh plan request that carries the conversation
	// so far (the question already lives in it). Metered like the original
	// plan turn — this is the second half of one planning exchange.
	if prompt.Mode == "plan" {
		h.streamPlan(r.Context(), w, flusher, id, prompt.ProjectID, userID, body.Answer)
		return
	}
	h.streamBuild(r.Context(), w, flusher, prompt.ProjectID, id, userID, body.Answer,
		buildOptions{persistUserTurn: false, meterPrompt: false, answer: true, mode: "build"})
}

// Discard marks an interrupted turn as abandoned so a later reload stops
// offering to resume it. It leaves the transcript and the files exactly as they
// are; only the run-status marker changes, out of the set that offers Resume.
func (h *PromptHandler) Discard(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := h.ownedProject(w, r, prompt.ProjectID); !ok {
		return
	}

	// Discarding a turn that is still streaming would let the running loop
	// overwrite the marker when it finishes — refuse rather than race it.
	if h.orch.GetSession(id) != nil {
		writeError(w, http.StatusConflict, "this turn is still running")
		return
	}

	if err := h.store.UpdatePromptStatus(id, "abandoned", prompt.Round); err != nil {
		writeError(w, http.StatusInternalServerError, "could not discard the turn")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "abandoned"})
}

// streamPlan proxies the model's stream to the client, then parses the finished
// text into a plan.
func (h *PromptHandler) streamPlan(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, promptID, projectID, userID int64, userPrompt string) {
	// The system prompt was previously omitted here while the output was still
	// parsed as a plan, so plan mode had no instructions at all.
	stream := h.ai.ChatStream(ctx, h.planPrompt, h.planHistory(projectID, userPrompt))

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
		// Give a plan turn that errored or was cut off a terminal status too, so a
		// later reload does not offer to resume it. Plan turns are not resumable;
		// the marker just keeps the row from looking interrupted forever.
		if ctx.Err() != nil {
			h.store.UpdatePromptStatus(promptID, "cancelled", 0)
		} else {
			h.store.UpdatePromptStatus(promptID, "failed", 0)
		}
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
		h.persistPlanExchange(projectID, promptID, userPrompt, planResp.Message, planJSON(planResp.Plan))
		writeSSE(w, "plan", planResp)
	} else if raw != "" {
		h.store.UpdatePromptResponse(promptID, raw)
		// The model did not return a parseable plan. Surface its reply verbatim
		// rather than leaving the chat staring at a blank turn.
		h.persistPlanExchange(projectID, promptID, userPrompt, raw, nil)
		writeSSE(w, "text.delta", ai.StreamTextDelta{Text: raw})
	}

	// A plan reply that asks questions instead of settling a plan is not done:
	// mark it waiting so the Answer endpoint can continue the planning with the
	// user's reply, exactly as build turns continue after ask_user. The
	// distinction is structural — a plan turn that produced screens is settled
	// even if its message also asks something minor.
	waiting := planResp.Plan == nil && planAsksQuestion(raw)
	if waiting {
		writeSSE(w, "done", map[string]string{"status": "waiting"})
		h.store.UpdatePromptStatus(promptID, "waiting", 0)
		// E: the plan turn is holding for the user's reply — the input-needed
		// push, plan variant.
		h.dispatchNotification(userID, notify.KindInputNeeded,
			"Your plan needs an answer", "The agent asked a question before settling the plan.")
	} else {
		writeSSE(w, "done", map[string]string{"status": "success"})
		h.store.UpdatePromptStatus(promptID, "completed", 0)
		// E: the plan is settled — push the moment it is ready to review.
		h.dispatchNotification(userID, notify.KindPlanReady,
			"Your plan is ready", "Review it and send the build when you are happy.")
	}
	flusher.Flush()
}

// planAsksQuestion reports whether a plan-mode reply is asking the user
// something rather than settling a plan. Plan responses are JSON; the
// heuristic reads the human-readable message for the shape a blocking
// question takes (an interrogative or an explicit "which/what/… do you want"
// construction). It runs only when the reply carries no plan, so a settled
// plan with rhetorical phrasing never trips it.
func planAsksQuestion(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	msg := raw
	var probe struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &probe) == nil {
		msg = probe.Message
	}
	msg = strings.ToLower(msg)
	if len(msg) > 2000 {
		msg = msg[len(msg)-2000:] // questions live at the end
	}
	markers := []string{"which one", "which do you", "what do you want", "do you want",
		"do you need", "should i", "would you rather", "?", "your call", "you decide"}
	for _, m := range markers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// streamBuild runs the agent loop while forwarding its events to the client.
func (h *PromptHandler) streamBuild(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, projectID, promptID, userID int64, userPrompt string, bo buildOptions) {
	type result struct {
		outcome *orchestrator.BuildOutcome
		err     error
	}
	resultCh := make(chan result, 1)
	emitterCh := make(chan *events.SessionEmitter, 1)

	go func() {
		outcome, err := h.runBuild(ctx, projectID, promptID, userID, userPrompt, emitterCh, bo)
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
			// E: the ask_user pause is the one moment the product needs
			// the user mid-turn — push it (fire-and-forget; the in-app
			// history is always recorded).
			if evt.Type == events.QuestionEvent {
				h.dispatchNotification(userID, notify.KindInputNeeded,
					"Seeker needs your input", evt.Text)
			}
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
		// E: the turn's terminal state is the other moment worth a push —
		// success (with the verification verdict) or failure (with the
		// reason). A paused turn is not terminal; the question push above
		// already fired.
		if res.outcome == nil || !res.outcome.Paused {
			if res.err == nil {
				verified := res.outcome != nil && res.outcome.Verified
				h.dispatchNotification(userID, notify.KindBuildSuccess,
					"Build finished", fmt.Sprintf("verified: %v", verified))
			} else {
				h.dispatchNotification(userID, notify.KindBuildFailed,
					"Build failed", friendlyAIError(res.err))
			}
		}
	}
}

// dispatchNotification is the one send path: dark-safe (nil notifier), and
// fire-and-forget by law — a push must never block or fail the turn.
func (h *PromptHandler) dispatchNotification(userID int64, kind notify.Kind, title, body string) {
	if h.notifier == nil {
		return
	}
	notify.Dispatch(h.store, h.notifier, userID, kind, title, body)
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
		payload["skipped_gates"] = outcome.SkippedGates
		payload["rounds"] = outcome.Rounds
		payload["remaining_tasks"] = outcome.RemainingTasks
		// A turn paused on ask_user ended cleanly but is not done: it is waiting for
		// the user's answer to resume. Say so rather than "success", so the client
		// keeps the turn open and shows the question instead of a finished turn.
		if outcome.Paused {
			payload["status"] = "waiting"
		} else if outcome.Exhausted {
			// Reached the round budget: the live turn ended cleanly (nil error, so no
			// error event above) but is not done. 'paused' tells the client to show it
			// as resumable — Resume continues it — rather than as a success or a fault.
			payload["status"] = "paused"
		} else if outcome.Stalled {
			payload["status"] = "stalled"
		}
	}
	writeSSE(w, "done", payload)
	flusher.Flush()
}

// writeBuildEvent maps an internal event onto the SSE vocabulary the IDE knows.
func (h *PromptHandler) writeBuildEvent(w http.ResponseWriter, evt events.Event) {
	switch evt.Type {
	case events.PhaseEvent:
		// prompt_id on every phase frame lets the client capture the running
		// turn's id from the first event — the steer route is addressed by it,
		// and before this only the question and done frames carried it.
		writeSSE(w, "phase", map[string]interface{}{"phase": evt.Phase, "status": evt.Status, "prompt_id": evt.PromptID})
	case events.ToolCallEvent:
		writeSSE(w, "tool_call", map[string]interface{}{
			"id": evt.ID, "tool": evt.Tool, "path": evt.Path, "name": evt.Name, "status": evt.Status,
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
	case events.QuestionEvent:
		// The agent called ask_user: the turn is pausing. prompt_id is the turn
		// the answer resumes (POST /answer is keyed by it); id is the pending
		// ask_user tool_use the answer resolves; the frontend echoes id back so
		// the reply lands on the right call. choices, when present, are the
		// suggested answers to offer.
		writeSSE(w, "question", map[string]interface{}{
			"prompt_id": evt.PromptID, "id": evt.ID, "question": evt.Text, "choices": evt.Data,
		})
	case events.ThinkingEvent:
		writeSSE(w, "thinking.delta", ai.StreamTextDelta{Text: evt.Text})
	case events.DecisionEvent:
		// The agent recorded an architecture decision or a durable memory. The
		// whole payload (an events.Decision) rides in Data; the client renders it
		// inline as a card matching the "What the agent knows" panel.
		writeSSE(w, "decision", evt.Data)
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

// buildOptions varies runBuild between a fresh turn and a resumed one.
//
// persistUserTurn stores the user's prompt before the loop starts. A fresh turn
// sets it so the prompt survives a crash mid-turn; a resume leaves it false
// because the "keep going" prompt it injects is synthetic and must not appear in
// the transcript.
//
// meterPrompt counts the turn against the monthly prompt quota. A fresh turn
// sets it; a resume leaves it false so continuing an interrupted turn is not
// charged as a second prompt. (Token accounting is additive and runs either way,
// so a resumed turn's tokens are still recorded.)
//
// answer marks the resume of a turn paused on ask_user. Instead of appending the
// prompt as a new user text turn, runBuild injects it as the tool_result for the
// pending ask_user call, so the model reads it as the answer to its question.
type buildOptions struct {
	persistUserTurn bool
	meterPrompt     bool
	answer          bool
	// mode is "plan" or "build": routing classifies the turn from it.
	mode string
}

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
	bo buildOptions,
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

	// Ground the model in what already exists before the first round: capture a
	// one-time map of the project's files, their exports, and their imports, and
	// hand it to the session so the enriched system prompt carries it in the cached
	// prefix. On a fresh project the map is empty and nothing is injected. This is
	// what stops an iterative build from re-discovering the layout file by file.
	if projectMap := tools.ProjectMapText(workspacePath); projectMap != "" {
		sess.SetProjectMapFP(projectMap, tools.ProjectFingerprint(workspacePath))
		sess.Emitter.EmitToolCall("", "context_build", "", "Scanned the project", "completed")
	}

	// Push the settled plan to the client as a checklist before the first round,
	// so the developer sees every screen and feature the build will work through —
	// checked off live as the agent finishes them — not just the ones that change.
	sess.EmitPlanSeed()

	history := h.turnHistory(projectID, promptID, userPrompt, bo)

	turnStart := time.Now()
	outcome, runErr := h.orch.RunAgentLoop(ctx, sess, orchestrator.RunOptions{
		SystemPrompt: h.systemPrompt(),
		Mode:         bo.mode,
		History:      history,
		Tools:        bs.defs,
		Execute:      bs.executor(),
		ReadOnlyTool: bs.reg.IsReadOnly,
		FilesTouched: bs.fs.Touched,
		Exists:       bs.fs.Exists,
		// Flush each round's turns as the loop advances so an interrupted run
		// keeps the rounds that already finished — the difference between a crash
		// costing one round and costing the whole turn.
		Persist: func(msgs []ai.Message) {
			lg := h.slog.get(h.store, projectID)
			for _, m := range msgs {
				if err := h.store.AppendMessage(projectID, promptID, m.Role,
					ai.MarshalBlocks(m.Blocks), 0, 0); err != nil {
					log.Printf("project %d: could not persist a round: %v", projectID, err)
					return
				}
				// H1 dual-write: the same turn lands in the append-only log so
				// replay and shadow-compaction have the full record.
				if m.Role == "user" {
					appendLoggedUser(lg, m, promptID)
				} else {
					appendLoggedAssistant(lg, m, promptID)
				}
			}
		},
		// H8: at a compaction boundary, shadow the session log to the same
		// boundary so derivation (which replaces shadowed surface events with
		// the summary) and the wire conversation stay honest with each other.
		// The summary mirrors what Compact told the model.
		OnCompacted: func(before, after int) {
			lg := h.slog.get(h.store, projectID)
			if lg == nil {
				return
			}
			if err := lg.Shadow(lg.Seq(),
				fmt.Sprintf("context compacted from %d to %d turns; earlier files are on disk and re-readable", before, after),
				"wire compaction"); err != nil {
				log.Printf("project %d: could not shadow the session log at compaction: %v", projectID, err)
			}
		},
	})

	// Everything below runs whether or not the loop succeeded: a failed turn
	// still changed files and still cost tokens, and losing that record is how
	// the next turn ends up contradicting this one.
	h.finalizeTurn(projectID, promptID, userID, outcome, runErr, bo.meterPrompt, time.Since(turnStart))
	h.checkpoint(projectID, promptID, userPrompt, bs.fs.Journal())

	if outcome != nil && len(outcome.FilesTouched) > 0 {
		h.checkNativeRebuild(ctx, projectID, workspacePath, outcome.FilesTouched, sess)
	}

	return outcome, runErr
}

// turnHistory assembles the conversation the loop replays for this turn and
// persists the turn that starts it. It has three shapes:
//
//   - a fresh turn (persistUserTurn) appends the user's prompt as a text turn
//     and stores it up front, so a crash mid-turn still leaves the request that
//     began it;
//   - an answer to a paused ask_user (answer) injects the prompt as the
//     tool_result for the pending call and stores that answer turn — it is real
//     user input, part of the transcript;
//   - a plain resume (neither) replays the stored turns as they are and adds the
//     synthetic "keep going" prompt, which is deliberately not persisted.
//
// The loop's own rounds are persisted separately through the Persist callback,
// so nothing here is stored twice.
func (h *PromptHandler) turnHistory(projectID, promptID int64, userPrompt string, bo buildOptions) []ai.Message {
	if bo.answer {
		// Resuming a turn paused on ask_user. The raw tail ends with the
		// unanswered tool_use; answer it in place rather than letting the trim
		// drop it as an orphan and losing the question the user is replying to.
		stored := h.storedConversation(projectID)
		if askID, allIDs, ok := ai.PendingToolUse(stored, "ask_user"); ok {
			answerTurn := ai.AnswerTurn(askID, allIDs, userPrompt)
			h.persistTurn(projectID, promptID, answerTurn, "answer")
			// AnsweredHistory keeps the tail — the answered tool_use and the answer
			// itself. TrimToValidHistory must NOT be used here: it back-trims until
			// the history ends on an assistant turn, which would throw away the very
			// answer we are resuming with (a user tool_result turn).
			return ai.AnsweredHistory(stored, answerTurn)
		}
		// The pending question is gone — already trimmed away, or the turn was not
		// actually waiting. Fall through and take the answer as a fresh instruction
		// so the turn still moves rather than dead-ending.
	}

	history := h.conversation(projectID)
	userTurn := ai.UserText(userPrompt)
	// A fresh prompt sent while an earlier turn is still waiting on ask_user:
	// the replay trim drops the trailing orphan tool_use, so without this the
	// model never sees the question the user may be replying to ("go with your
	// fallback" is meaningless without it). Carry the pending question forward
	// inside the fresh prompt it may answer.
	if q, ok := ai.PendingQuestion(h.storedConversation(projectID)); ok {
		userTurn = ai.UserText(userPrompt + "\n\n[Context: an earlier turn is still waiting on this " +
			"question from you: \"" + q + "\" If this message answers or relates to it, act on that " +
			"connection; otherwise proceed with the message as a new request.]")
	}
	history = append(history, userTurn)

	// A fresh turn stores its prompt up front so a crash mid-turn still leaves the
	// request that began it; an answer that fell through stores its text too (it
	// is real user input). Only a plain resume's synthetic prompt stays
	// unpersisted. The loop's own turns are persisted round by round through
	// Persist, so nothing here is stored twice.
	if bo.persistUserTurn || bo.answer {
		h.persistTurn(projectID, promptID, userTurn, "user turn")
	}
	return history
}

// persistTurn stores one turn of the transcript, logging rather than failing the
// build if the write does not land. The same turn dual-writes into the H1
// session log; a log failure logs too and never fails the build.
func (h *PromptHandler) persistTurn(projectID, promptID int64, m ai.Message, label string) {
	if err := h.store.AppendMessage(projectID, promptID, m.Role, ai.MarshalBlocks(m.Blocks), 0, 0); err != nil {
		log.Printf("project %d: could not store the %s: %v", projectID, label, err)
	}
	lg := h.slog.get(h.store, projectID)
	if m.Role == "user" {
		appendLoggedUser(lg, m, promptID)
	} else {
		appendLoggedAssistant(lg, m, promptID)
	}
}

// finalizeTurn writes the turn's terminal state after the loop returns: its
// run-status marker, its final text and its token cost. The turn's messages are
// already stored — the user prompt up front and each round through the loop's
// Persist callback — so this does not re-append them; doing so would double the
// transcript.
func (h *PromptHandler) finalizeTurn(projectID, promptID, userID int64, outcome *orchestrator.BuildOutcome, runErr error, meterPrompt bool, elapsed time.Duration) {
	round := 0
	if outcome != nil {
		round = outcome.Rounds
	}
	// The marker is what a later reload reads to decide whether to offer Resume.
	// It is written from the loop's outcome: nil error is a finished turn, a
	// cancellation is a deliberate stop or a dropped connection, anything else
	// failed. Two states return a nil error but are not done: a turn paused on
	// ask_user records 'waiting' (the answer endpoint resumes it), and a turn that
	// reached its round budget while still working records 'paused' (the Resume
	// endpoint continues it). Both are resumable; neither is an error.
	status := terminalStatus(runErr)
	if outcome != nil {
		switch {
		case outcome.Paused:
			status = "waiting"
		case outcome.Exhausted:
			status = "paused"
		case outcome.Stalled:
			// A stalled turn is terminal and deliberately not resumable:
			// resuming would most likely resume the ramble. The transcript
			// persists, so a fresh prompt can continue differently. 'stalled'
			// matches no resume offer, by design.
			status = "stalled"
		}
	}
	if err := h.store.UpdatePromptStatus(promptID, status, round); err != nil {
		log.Printf("project %d: could not record run status: %v", projectID, err)
	}

	if outcome == nil {
		return
	}

	response := outcome.FinalText
	if response == "" {
		switch {
		case outcome.Paused:
			// A paused turn is waiting on the user, not finished — the question is
			// carried in the transcript and the question event, so this stands in
			// only where the model asked without any preceding prose. "No changes
			// were made." would be a lie: the turn is mid-flight.
			response = "Waiting for your answer to continue."
		case outcome.Exhausted:
			// Reached the round budget mid-work. The turn is neither done nor
			// broken; its files are saved and it can be resumed. Say that rather
			// than listing changed files as if the work were finished.
			response = "Paused after reaching the build's round budget — your work so far is saved. Press Resume to continue."
		case len(outcome.FilesTouched) > 0:
			response = "Changed: " + strings.Join(outcome.FilesTouched, ", ")
		default:
			response = "No changes were made."
		}
	}
	h.store.UpdatePromptResponse(promptID, response)
	// Persist the gates that could not run, so "verified" stays honest past
	// the live stream: a reload or a later GET reports them instead of
	// forgetting the turn was only statically verified.
	if outcome.Verified {
		h.store.UpdatePromptSkippedGates(promptID, outcome.SkippedGates)
	}
	// Token accounting is additive, so a resumed turn correctly adds its own
	// cost onto the interrupted turn's. The prompt count is not: only a fresh
	// turn increments it, or resuming would charge a second prompt.
	h.store.RecordPromptTokens(promptID, userID,
		outcome.Usage.InputTokens, outcome.Usage.OutputTokens, outcome.Usage.CacheReadTokens)
	// Dollar cost lands on the same rows as the tokens — one ledger. The model
	// priced is the one that actually served the turn, not the default.
	model := outcome.RoutedModel
	if model == "" {
		model = h.ai.Model()
	}
	cost := ai.CostUSD(model,
		outcome.Usage.InputTokens, outcome.Usage.OutputTokens, outcome.Usage.CacheReadTokens,
		ai.DefaultPricing())
	if err := h.store.RecordPromptRouting(promptID, userID, cost, model, outcome.RouteReason); err != nil {
		log.Printf("project %d: could not record routing cost: %v", projectID, err)
	}
	// Operational turn metrics: wall time, tokens, dollars. A turn that ended
	// in a real error counts against reliability; paused, waiting, exhausted,
	// and cancelled turns are deliberate stops, not failures.
	metrics.Default.ObserveTurn(runErr == nil,
		elapsed.Milliseconds(),
		int64(outcome.Usage.InputTokens), int64(outcome.Usage.OutputTokens),
		int64(cost*1e6))
	if meterPrompt {
		h.store.IncrementPrompts(userID)
	}

	// Keep the stored conversation bounded. The tail is what matters for
	// continuity; the whole history would eventually dominate the database.
	h.store.TrimConversation(projectID, 400)

	// H13: upgrade the session title with the LLM path once the turn is
	// fully terminal (completed or failed — never waiting/paused, whose
	// resumes must not race a title call against the provider script) and
	// only when the row still carries the fallback (the first upgrade wins;
	// a resumed turn does not re-title). One bounded request, off the
	// request path. A failure keeps the fallback: the title is decoration,
	// never a cost center.
	if status == "completed" || status == "failed" {
		if p, err := h.store.GetPrompt(promptID); err == nil && p != nil && p.Title == truncateTitleForAPI(p.Prompt) {
			go func(promptText string) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if title := h.SessionTitle(ctx, promptText); title != "" && title != promptText {
					_ = h.store.UpdatePromptTitle(promptID, title)
				}
			}(p.Prompt)
		}
	}
}

// terminalStatus maps the loop's return into the run-status vocabulary the
// prompts row records. A cancellation (deliberate Stop or a dropped connection,
// both surfaced as a wrapped context error) is distinct from a genuine failure
// so the UI can offer Resume for the former without treating it as an error.
func terminalStatus(runErr error) string {
	switch {
	case runErr == nil:
		return "completed"
	case strings.Contains(runErr.Error(), "cancelled"), strings.Contains(runErr.Error(), "context canceled"):
		return "cancelled"
	default:
		return "failed"
	}
}

// persistPlanExchange records a plan-mode turn so a reload restores it. Plan
// mode calls no tools, so the exchange is just the user's prompt and the
// assistant's reply: a text block with the human-readable message (which is
// what gets replayed to the model as context and shown as prose) and, when the
// model returned a structured plan, a plan block the restore path rebuilds into
// a plan card. It mirrors how the build path persists its turns (the user turn,
// then each round's messages) — without it, the first interactions on a project
// (planning) vanished on refresh while build turns survived.
func (h *PromptHandler) persistPlanExchange(projectID, promptID int64, userPrompt, message string, plan json.RawMessage) {
	lg := h.slog.get(h.store, projectID)
	userTurn := ai.UserText(userPrompt)
	if err := h.store.AppendMessage(projectID, promptID, userTurn.Role,
		ai.MarshalBlocks(userTurn.Blocks), 0, 0); err != nil {
		log.Printf("project %d: could not store the plan prompt: %v", projectID, err)
		return
	}
	appendLoggedUser(lg, userTurn, promptID)

	// Guarantee a non-empty message so the assistant turn always has replayable
	// text — an assistant turn carrying only a plan block would serialise to an
	// empty content array, which both providers reject.
	if strings.TrimSpace(message) == "" {
		message = "Here's the plan."
	}
	assistant := ai.AssistantPlan(message, plan)
	if err := h.store.AppendMessage(projectID, promptID, assistant.Role,
		ai.MarshalBlocks(assistant.Blocks), 0, 0); err != nil {
		log.Printf("project %d: could not store the plan reply: %v", projectID, err)
	}
	appendLoggedAssistant(lg, assistant, promptID)

	h.store.TrimConversation(projectID, 400)
}

// planJSON marshals a plan for storage, returning nil when there is nothing to
// store (so persistPlanExchange stores a text-only reply).
func planJSON(plan *ai.AppPlan) json.RawMessage {
	if plan == nil {
		return nil
	}
	b, err := json.Marshal(plan)
	if err != nil {
		return nil
	}
	return b
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

// storedConversation replays the project's stored turns verbatim, without the
// validity trim. The answer path needs the raw tail — a turn paused on ask_user
// ends with an unanswered tool_use that TrimToValidHistory would drop as an
// orphan, which is the one turn resume must keep so it can answer it.
//
// H1: when SEEKERSTUDIO_SESSIONLOG=derive, the read comes from the append-only
// session log's projection instead of conversation_messages — the flag flip
// the dual-write migration ends on. Off (the default) reads the legacy table
// exactly as before; both paths were proven byte-equivalent by the bridge
// round-trip tests before this switch existed.
func (h *PromptHandler) storedConversation(projectID int64) []ai.Message {
	if derived := h.deriveFromLog(projectID); derived != nil {
		return derived
	}
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
	return msgs
}

// conversation replays the project's stored turns, trimmed to a shape both
// providers accept: a replayed conversation must not begin with tool results or
// end mid tool call — an orphaned tool_use or tool_result is a 400.
func (h *PromptHandler) conversation(projectID int64) []ai.Message {
	return ai.TrimToValidHistory(h.storedConversation(projectID))
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

	// Active architecture decisions are part of "what the agent knows" — the
	// read-only memory panel shows them next to the curated memory entries.
	// Failing to load them is not worth failing the whole request over; the
	// panel just shows memory without decisions.
	decisions, err := h.store.GetActiveADRs(projectID)
	if err != nil {
		log.Printf("project %d: could not load architecture decisions: %v", projectID, err)
		decisions = nil
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories":  memories,
		"summary":   h.memorySummary(projectID),
		"decisions": decisions,
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

	resp := map[string]interface{}{"turns": out}

	// The latest prompt's run status is what the IDE reads to decide whether to
	// offer "Resume / Discard". A turn left 'running' (server crash), 'cancelled'
	// (Stop or a dropped connection), 'failed', or 'paused' (reached its round
	// budget while still working) is resumable; 'completed' and 'abandoned' are
	// not. 'waiting' resumes by answering the question, not this way. Its absence
	// is not worth failing the whole restore.
	if latest, err := h.store.GetLatestPrompt(projectID); err != nil {
		log.Printf("project %d: could not load the latest prompt: %v", projectID, err)
	} else if latest != nil {
		resp["latest"] = map[string]interface{}{
			"id":     latest.ID,
			"mode":   latest.Mode,
			"status": latest.Status,
			"round":  latest.Round,
		}
	}

	// H13: the session token meter — the per-turn in/out the TurnFooter shows,
	// rolled per session. The chat rail renders this once, live-updated by the
	// per-turn usage SSE events; this is the reload restore.
	if in, out, cacheRead, err := h.store.ProjectTokenUsage(projectID); err == nil {
		resp["usage"] = map[string]int64{"in": in, "out": out, "cache_read": cacheRead}
	}

	writeJSON(w, http.StatusOK, resp)
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

// firstLine returns the first line of s, trimmed — audit records stay compact.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncateTitleForAPI mirrors the store's fallback-title shape so the
// upgrade gate can tell a fallback title from an upgraded one without
// storing a flag column: the fallback is exactly the truncated prompt.
func truncateTitleForAPI(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		cut := s[:80]
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
		s = cut
	}
	if s == "" {
		s = "Untitled turn"
	}
	return s
}
