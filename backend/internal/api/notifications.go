package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// NotificationHandler serves the in-app notification history and the
// device registration surface. The history is the honest record: every
// product notification lands here whether or not OneSignal is configured.
type NotificationHandler struct {
	store *store.Store
}

func NewNotificationHandler(st *store.Store) *NotificationHandler {
	return &NotificationHandler{store: st}
}

// List returns the user's most recent notifications, newest first.
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	_, _, pushConfigured := notify.Config()
	items, err := h.store.ListNotifications(userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load notifications")
		return
	}
	if items == nil {
		items = []store.Notification{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"notifications": items,
		// The honest answer to "will these also push?":
		"push_configured": pushConfigured,
	})
}

// MarkRead stamps one notification read.
func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification id")
		return
	}
	if err := h.store.MarkNotificationRead(userID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not mark the notification read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RegisterToken stores the device's OneSignal subscription id for the
// signed-in user. The body is {onesignal_id}; an empty id unregisters.
func (h *NotificationHandler) RegisterToken(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	var req struct {
		OneSignalID string `json:"onesignal_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	if len(req.OneSignalID) > 128 {
		writeError(w, http.StatusBadRequest, "that push token is too long")
		return
	}
	if err := h.store.SetOneSignalID(userID, req.OneSignalID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the push token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
