package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (h *PromptHandler) ListAuditLog(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	rows, err := h.store.ListAuditLog(projectID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read audit log")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"entries": rows})
}
