package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/adb"
	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/store"
)

type SessionHandler struct {
	store       *store.Store
	adb         *adb.Client
	workspaceDir string
}

func NewSessionHandler(s *store.Store, a *adb.Client, workspaceDir string) *SessionHandler {
	return &SessionHandler{store: s, adb: a, workspaceDir: workspaceDir}
}

type createSessionRequest struct {
	ProjectID int64 `json:"project_id"`
}

func (h *SessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ProjectID == 0 {
		writeError(w, http.StatusBadRequest, "project_id required")
		return
	}

	// Check if ADB is connected
	if !h.adb.IsConnected() {
		writeError(w, http.StatusServiceUnavailable, "Redroid container not available")
		return
	}

	session, err := h.store.CreateSession(req.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Start Metro bundler for this session
	workspaceDir := h.workspaceDir + "/" + strconv.FormatInt(req.ProjectID, 10)
	metroServer, err := metro.Start(session.ID, workspaceDir)
	if err != nil {
		// Log but don't fail — session can still work without Metro
		log.Printf("WARN: metro start failed for session %d: %v", session.ID, err)
	}

	// Update state to running
	h.store.UpdateSessionState(session.ID, "running")
	session.State = "running"

	// Include Metro port in response
	response := map[string]interface{}{
		"id":         session.ID,
		"project_id": session.ProjectID,
		"state":      session.State,
		"started_at": session.StartedAt,
	}
	if metroServer != nil {
		response["metro_port"] = metroServer.Port()
	}

	writeJSON(w, http.StatusCreated, response)
}

func (h *SessionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	session, err := h.store.GetSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Include Metro port if running
	response := map[string]interface{}{
		"id":         session.ID,
		"project_id": session.ProjectID,
		"state":      session.State,
		"started_at": session.StartedAt,
	}
	if port, ok := metro.GetPort(id); ok {
		response["metro_port"] = port
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *SessionHandler) Pause(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	if err := h.store.UpdateSessionState(id, "paused"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to pause session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"state": "paused"})
}

func (h *SessionHandler) Resume(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	if err := h.store.UpdateSessionState(id, "running"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resume session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"state": "running"})
}

func (h *SessionHandler) Destroy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}

	// Stop Metro bundler
	metro.Stop(id)

	if err := h.store.UpdateSessionState(id, "destroyed"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to destroy session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"state": "destroyed"})
}
