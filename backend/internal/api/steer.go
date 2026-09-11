package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	harnessagent "github.com/luckyisrael/talon/backend/internal/harness/agent"
)

// The H2 steer route: deliver a mid-turn message to a running build. The
// text lands in the running loop's steer lane and joins the next model
// request — the user redirects a build without aborting it. This file holds
// the handler; the lane itself lives on the orchestrator session, and the
// loop drains it at every round boundary.

// steerRequest is the POST body: the message and how it routes.
type steerRequest struct {
	// Message is the text to deliver. Required.
	Message string `json:"message"`
	// Class is "steer" (redirect the running build) or "inject" (add context
	// without redirecting). Default steer — the common case is a redirect.
	Class string `json:"class"`
}

// Steer delivers a message to a running turn. Unlike Answer and Resume it
// does NOT run a loop or stream: the running loop keeps streaming on its
// original SSE connection, and the steer arrives to the user through that
// stream as a marked turn. This handler is a mailbox drop, not a driver.
func (h *PromptHandler) Steer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid prompt id")
		return
	}

	var body steerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	if len(body.Message) > maxPromptLength {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("that message is %d characters; the limit is %d", len(body.Message), maxPromptLength))
		return
	}
	class := harnessagent.Steer
	switch body.Class {
	case "", "steer":
		class = harnessagent.Steer
	case "inject":
		class = harnessagent.Inject
	default:
		writeError(w, http.StatusBadRequest, "class must be \"steer\" or \"inject\"")
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

	// The turn must be live: steer delivers to a running loop. A finished or
	// paused turn has no loop to deliver to — the client should send a new
	// prompt (followup) instead.
	ag := h.orch.Agent(id)
	if ag == nil {
		writeError(w, http.StatusConflict, "this turn is not running — send it as a new prompt instead")
		return
	}

	seq := ag.Steer(class, body.Message)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"prompt_id": id,
		"delivered": true,
		"sequence":  seq,
		"class":     class,
	})
}
