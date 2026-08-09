package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/redroid"
	"github.com/seekersstudio/backend/internal/store"
)

type SessionHandler struct {
	store       *store.Store
	manager     *redroid.Manager
	portAlloc   *redroid.PortAllocator
	workspaceDir string
	devClient   *devclient.Manager
	wallet      *devclient.WalletManager
}

func NewSessionHandler(s *store.Store, mgr *redroid.Manager, portAlloc *redroid.PortAllocator, workspaceDir string, dc *devclient.Manager, wm *devclient.WalletManager) *SessionHandler {
	return &SessionHandler{
		store:       s,
		manager:     mgr,
		portAlloc:   portAlloc,
		workspaceDir: workspaceDir,
		devClient:   dc,
		wallet:      wm,
	}
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

	// Check host capacity
	if h.portAlloc.IsFull() {
		writeError(w, http.StatusServiceUnavailable, "host at capacity — no available device slots")
		return
	}

	// Create DB session record first (we need the ID for container naming)
	session, err := h.store.CreateSession(req.ProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}

	// Create Redroid container for this session
	if h.manager == nil {
		writeError(w, http.StatusServiceUnavailable, "container manager not available")
		return
	}

	containerInfo, err := h.manager.CreateContainer(session.ID)
	if err != nil {
		log.Printf("ERROR: container creation failed for session %d: %v", session.ID, err)
		h.store.UpdateSessionState(session.ID, "destroyed")
		writeError(w, http.StatusServiceUnavailable, "failed to create device container: "+err.Error())
		return
	}

	// Persist container metadata
	h.store.UpdateSessionContainer(session.ID, containerInfo.ContainerID, containerInfo.ContainerName, containerInfo.ADBPort)
	h.store.UpdateSessionContainerState(session.ID, "running")

	// Install dev-client APK into the container
	if h.devClient != nil {
		adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)
		if err := h.devClient.InstallDevClient(context.Background(), adbTarget); err != nil {
			log.Printf("WARN: dev-client install failed for session %d: %v (container will run without pre-built dev-client)", session.ID, err)
		} else {
			log.Printf("dev-client installed for session %d on %s", session.ID, adbTarget)
		}
	}

	// Setup mock wallet (hard failure if not available — §2.9)
	if h.wallet != nil && h.wallet.IsInstalled() {
		adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)
		if err := h.wallet.SetupSession(context.Background(), adbTarget); err != nil {
			log.Printf("ERROR: mock wallet setup failed for session %d: %v", session.ID, err)
			h.store.UpdateSessionState(session.ID, "destroyed")
			writeError(w, http.StatusServiceUnavailable, "wallet setup failed: "+err.Error())
			return
		}
		log.Printf("mock wallet configured for session %d on %s", session.ID, adbTarget)
	}

	// Start Metro bundler for this session
	workspaceDir := h.workspaceDir + "/" + strconv.FormatInt(req.ProjectID, 10)
	adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)
	metroServer, err := metro.Start(session.ID, workspaceDir, adbTarget)
	if err != nil {
		log.Printf("WARN: metro start failed for session %d: %v", session.ID, err)
	}

	// Update state to running
	h.store.UpdateSessionState(session.ID, "running")
	session.State = "running"

	// Build response
	response := map[string]interface{}{
		"id":          session.ID,
		"project_id":  session.ProjectID,
		"state":       session.State,
		"started_at":  session.StartedAt,
		"adb_port":    containerInfo.ADBPort,
		"container_id": containerInfo.ContainerID,
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
		"id":              session.ID,
		"project_id":      session.ProjectID,
		"state":           session.State,
		"started_at":      session.StartedAt,
		"adb_port":        session.ADBPort,
		"container_id":    session.ContainerID,
		"container_state": session.ContainerState,
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

	session, err := h.store.GetSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Pause the Docker container
	if session.ContainerID != "" && h.manager != nil {
		if err := h.manager.PauseContainer(session.ContainerID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to pause container: "+err.Error())
			return
		}
		h.store.UpdateSessionContainerState(id, "paused")
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

	session, err := h.store.GetSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Unpause the Docker container
	if session.ContainerID != "" && h.manager != nil {
		if err := h.manager.UnpauseContainer(session.ContainerID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resume container: "+err.Error())
			return
		}
		h.store.UpdateSessionContainerState(id, "running")
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

	session, err := h.store.GetSession(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Stop Metro bundler
	metro.Stop(id)

	// Destroy the Docker container
	if session.ContainerID != "" && h.manager != nil {
		if err := h.manager.StopContainer(session.ContainerID); err != nil {
			log.Printf("WARN: container destroy failed for session %d: %v", id, err)
		}
		// Release the port
		h.portAlloc.Release(id)
		// Clear container metadata
		h.store.UpdateSessionContainer(id, "", "", 0)
		h.store.UpdateSessionContainerState(id, "")
	}

	if err := h.store.UpdateSessionState(id, "destroyed"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to destroy session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"state": "destroyed"})
}
