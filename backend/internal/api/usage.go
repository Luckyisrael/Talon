package api

import (
	"encoding/json"
	"net/http"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/store"
)

type UsageHandler struct {
	store *store.Store
	ai    *ai.Client
}

func NewUsageHandler(s *store.Store, a *ai.Client) *UsageHandler {
	return &UsageHandler{store: s, ai: a}
}

type usageResponse struct {
	PromptsUsed   int     `json:"prompts_used"`
	PromptsLimit  int     `json:"prompts_limit"`
	DeviceMinutes float64 `json:"device_minutes"`
	DeviceLimit   int     `json:"device_limit"`
	CostUSD       float64 `json:"cost_usd"`
	Tier          string  `json:"tier"`
	// PacingSeconds is the enforced minimum interval between AI gateway
	// calls. A turn's wall-clock is dominated by this when it is set —
	// the frontend shows it so a long build reads as pacing, not a hang.
	PacingSeconds int `json:"pacing_seconds"`
}

func (h *UsageHandler) Get(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

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

	// One source of truth for tier limits (quota.go). This path only reports
	// the numbers; promptLimitFor enforces the same ones. They used to be
	// written out separately here and had drifted (Pro showed 500 here but
	// enforced 2000).
	quota := quotaFor(user.Tier)
	resp := usageResponse{
		PromptsUsed:   usage.PromptsUsed,
		PromptsLimit:  quota.Prompts,
		DeviceMinutes: usage.DeviceMinutes,
		DeviceLimit:   quota.DeviceMinutes,
		CostUSD:       usage.CostUSD,
		Tier:          user.Tier,
		PacingSeconds: int(h.ai.PacingInterval().Seconds()),
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
