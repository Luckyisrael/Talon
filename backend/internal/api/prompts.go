package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/store"
)

type PromptHandler struct {
	store *store.Store
	ai    *ai.Client
}

func NewPromptHandler(s *store.Store, a *ai.Client) *PromptHandler {
	return &PromptHandler{store: s, ai: a}
}

type promptRequest struct {
	ProjectID int64  `json:"project_id"`
	Prompt    string `json:"prompt"`
	Mode      string `json:"mode"` // "plan" or "build"
}

func (h *PromptHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req promptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Prompt == "" || req.Mode == "" {
		writeError(w, http.StatusBadRequest, "prompt and mode required")
		return
	}

	if req.Mode != "plan" && req.Mode != "build" {
		writeError(w, http.StatusBadRequest, "mode must be 'plan' or 'build'")
		return
	}

	// Check usage limits
	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check usage")
		return
	}

	if usage.PromptsUsed >= 20 {
		writeError(w, http.StatusTooManyRequests, "free tier limit reached (20 prompts/month)")
		return
	}

	// Create prompt record
	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	// Build conversation history
	history := []ai.PromptMessage{
		{Role: "user", Content: req.Prompt},
	}

	var response string

	if req.Mode == "plan" {
		planResp, err := h.ai.Plan(context.Background(), history)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "AI request failed: "+err.Error())
			return
		}
		respBytes, _ := json.Marshal(planResp)
		response = string(respBytes)

		// Save plan to project if plan is returned
		if planResp.Plan != nil {
			planJSON, _ := json.Marshal(planResp.Plan)
			h.store.UpdateProjectPlan(req.ProjectID, string(planJSON))
		}
	} else {
		buildResp, err := h.ai.Build(context.Background(), history)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "AI request failed: "+err.Error())
			return
		}
		respBytes, _ := json.Marshal(buildResp)
		response = string(respBytes)
	}

	// Update prompt with response
	h.store.UpdatePromptResponse(prompt.ID, response)
	h.store.IncrementPrompts(userID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       prompt.ID,
		"mode":     req.Mode,
		"response": json.RawMessage(response),
	})
}

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

	writeJSON(w, http.StatusOK, prompt)
}
