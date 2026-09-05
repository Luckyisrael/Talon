package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/store"
)

// TriageHandler serves the Play Billing cutover triage: one structured record
// per project holding the cutover decision, the cutover date, and the dollar
// exposure. Ownership-gated, not tier-gated — triage informs planning, and
// planning is not a Pro feature. Exposure travels as integer cents so the
// form's dollars survive the round trip exactly.
type TriageHandler struct {
	store *store.Store
}

func NewTriageHandler(s *store.Store) *TriageHandler {
	return &TriageHandler{store: s}
}

func (h *TriageHandler) gateTriage(w http.ResponseWriter, r *http.Request, projectID int64) (*store.Project, bool) {
	project, err := h.store.GetProject(projectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// GET /v1/projects/{id}/triage — returns the report, or null when none has
// been recorded yet. Null is not an error: the form starts blank.
func (h *TriageHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.gateTriage(w, r, projectID); !ok {
		return
	}
	report, err := h.store.LoadTriageReport(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the triage report")
		return
	}
	if report == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type triageRequest struct {
	Decision      string `json:"decision"`
	CutoverDate   string `json:"cutover_date"`
	ExposureCents int64  `json:"exposure_cents"`
	Notes         string `json:"notes"`
}

// PUT /v1/projects/{id}/triage — records or revises the report. The decision
// is required; everything else may be blank while the triage is still open.
func (h *TriageHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.gateTriage(w, r, projectID); !ok {
		return
	}
	var req triageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	if strings.TrimSpace(req.Decision) == "" {
		writeError(w, http.StatusBadRequest, "decision is required")
		return
	}
	if req.ExposureCents < 0 {
		writeError(w, http.StatusBadRequest, "exposure cannot be negative")
		return
	}
	report, err := h.store.SaveTriageReport(projectID, strings.TrimSpace(req.Decision), strings.TrimSpace(req.CutoverDate), req.ExposureCents, req.Notes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the triage report")
		return
	}
	writeJSON(w, http.StatusOK, report)
}
