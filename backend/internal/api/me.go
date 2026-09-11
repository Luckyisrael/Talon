package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// MeHandler serves the authenticated user's own profile: identity, current tier,
// subscription detail, and billing history. It is the single place the settings
// page reads real server state from, replacing the old localStorage-email
// workaround (Phase 9 rule 2.6 — one source of truth for what the user is on).
type MeHandler struct {
	store *store.Store
}

func NewMeHandler(s *store.Store) *MeHandler {
	return &MeHandler{store: s}
}

// billingEventView is the history entry the profile shows: what happened and
// when. The raw provider payload stays in the audit log, out of the UI.
type billingEventView struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type meResponse struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	// Tier is the enforced tier (users.tier), driven only by verified webhooks —
	// the same value the quota layer reads, so the profile cannot claim a tier the
	// system does not actually grant.
	Tier         string              `json:"tier"`
	CreatedAt    time.Time           `json:"created_at"`
	Subscription *store.Subscription `json:"subscription,omitempty"`
	History      []billingEventView  `json:"history"`
}

// profile assembles the full profile for a user from the three sources of truth:
// the user row (identity + enforced tier), the subscription (renewal, last-4,
// pending cancel), and the billing log (history). Shared by Get and Update so both
// return an identical, current shape.
func (h *MeHandler) profile(userID int64) (*meResponse, error) {
	user, err := h.store.GetUserByID(userID)
	if err != nil {
		return nil, err
	}
	sub, err := h.store.GetSubscriptionByUser(userID)
	if err != nil {
		return nil, err
	}
	events, err := h.store.ListBillingEvents(userID, 50)
	if err != nil {
		return nil, err
	}
	history := make([]billingEventView, 0, len(events))
	for _, e := range events {
		history = append(history, billingEventView{Type: e.Type, CreatedAt: e.CreatedAt})
	}
	return &meResponse{
		ID:           user.ID,
		Email:        user.Email,
		DisplayName:  user.DisplayName,
		Tier:         user.Tier,
		CreatedAt:    user.CreatedAt,
		Subscription: sub,
		History:      history,
	}, nil
}

// Get returns the current user's profile.
func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	profile, err := h.profile(GetUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load profile")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// Update changes the editable profile fields. Only the display name is editable:
// email is the login identity, and tier is provider-driven and must never be set
// from a request body (that is what SetTier + the webhook path are for).
func (h *MeHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if len(name) > 100 {
		writeError(w, http.StatusBadRequest, "display name must be 100 characters or fewer")
		return
	}
	if _, err := h.store.UpdateUser(userID, name); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update profile")
		return
	}
	profile, err := h.profile(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load profile")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
