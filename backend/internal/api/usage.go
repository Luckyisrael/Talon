package api

import (
	"encoding/json"
	"net/http"

	"github.com/seekersstudio/backend/internal/store"
)

type UsageHandler struct {
	store *store.Store
}

func NewUsageHandler(s *store.Store) *UsageHandler {
	return &UsageHandler{store: s}
}

type usageResponse struct {
	PromptsUsed   int     `json:"prompts_used"`
	PromptsLimit  int     `json:"prompts_limit"`
	DeviceMinutes float64 `json:"device_minutes"`
	DeviceLimit   int     `json:"device_limit"`
	Tier          string  `json:"tier"`
}

func (h *UsageHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get user")
		return
	}

	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get usage")
		return
	}

	resp := usageResponse{
		PromptsUsed:   usage.PromptsUsed,
		PromptsLimit:  20,
		DeviceMinutes: usage.DeviceMinutes,
		DeviceLimit:   60,
		Tier:          user.Tier,
	}

	if user.Tier == "pro" {
		resp.PromptsLimit = 500
		resp.DeviceLimit = 1500
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
