package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/adb"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/redroid"
	"github.com/seekersstudio/backend/internal/store"
)

type SessionHandler struct {
	store        *store.Store
	manager      *redroid.Manager
	portAlloc    *redroid.PortAllocator
	workspaceDir string
	devClient    *devclient.Manager
	wallet       *devclient.WalletManager
}

func NewSessionHandler(s *store.Store, mgr *redroid.Manager, portAlloc *redroid.PortAllocator, workspaceDir string, dc *devclient.Manager, wm *devclient.WalletManager) *SessionHandler {
	return &SessionHandler{
		store:        s,
		manager:      mgr,
		portAlloc:    portAlloc,
		workspaceDir: workspaceDir,
		devClient:    dc,
		wallet:       wm,
	}
}

type createSessionRequest struct {
	ProjectID int64 `json:"project_id"`
}

// setupWalletForSession covers the wallet branch of session provisioning:
// pre-baked images skip straight to verification, otherwise the full setup
// runs (installs, PIN, verify, round-trip smoke). Extracted so Create can run
// it concurrently with the dev-client install.
func (h *SessionHandler) setupWalletForSession(sessionID int64, adbTarget string) error {
	if h.wallet.IsInstalledOnDevice(adbTarget) {
		log.Printf("wallet already installed on %s (pre-baked image), skipping install", adbTarget)
		if err := h.wallet.VerifyWallet(context.Background(), adbTarget); err != nil {
			log.Printf("WARN: wallet verification failed for session %d: %v", sessionID, err)
		}
		return nil
	}
	if err := h.wallet.SetupSession(context.Background(), adbTarget); err != nil {
		return err
	}
	log.Printf("mock wallet configured for session %d on %s", sessionID, adbTarget)
	return nil
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

	adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)

	// Wait for device boot before any ADB operations
	if err := h.waitForDeviceBoot(adbTarget, 90*time.Second); err != nil {
		log.Printf("WARN: device boot wait failed for session %d: %v", session.ID, err)
	}

	// Install dev-client APK and set up the mock wallet concurrently.
	// Separate packages, no ordering dependency — the wallet branch carries
	// the hard-failure semantics (§2.9), the dev-client branch stays
	// warn-and-continue. Metro starts after both (it reverse-forwards).
	var wg sync.WaitGroup
	var devClientErr, walletErr error
	if h.devClient != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.devClient.InstallDevClient(context.Background(), adbTarget); err != nil {
				devClientErr = err
			}
		}()
	}
	if h.wallet != nil && h.wallet.IsInstalled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			walletErr = h.setupWalletForSession(session.ID, adbTarget)
		}()
	}
	wg.Wait()

	if devClientErr != nil {
		log.Printf("WARN: dev-client install failed for session %d: %v (container will run without pre-built dev-client)", session.ID, devClientErr)
	} else if h.devClient != nil {
		log.Printf("dev-client installed for session %d on %s", session.ID, adbTarget)
	}
	if walletErr != nil {
		log.Printf("ERROR: mock wallet setup failed for session %d: %v", session.ID, walletErr)
		h.store.UpdateSessionState(session.ID, "destroyed")
		writeError(w, http.StatusServiceUnavailable, "wallet setup failed: "+walletErr.Error())
		return
	}

	// Start Metro bundler for this session
	workspaceDir := h.workspaceDir + "/" + strconv.FormatInt(req.ProjectID, 10)
	metroServer, err := metro.Start(session.ID, workspaceDir, adbTarget)
	if err != nil {
		log.Printf("WARN: metro start failed for session %d: %v", session.ID, err)
	}

	// Update state to running
	h.store.UpdateSessionState(session.ID, "running")
	session.State = "running"

	// Build response
	response := map[string]interface{}{
		"id":           session.ID,
		"project_id":   session.ProjectID,
		"state":        session.State,
		"started_at":   session.StartedAt,
		"adb_port":     containerInfo.ADBPort,
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

// Screenshot captures one PNG frame from the session's device for download
// or store-asset use. Same ownership posture as Get: session ids route here,
// and the adb target comes from the session row, never the request.
func (h *SessionHandler) Screenshot(w http.ResponseWriter, r *http.Request) {
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
	if session.ADBPort == 0 {
		writeError(w, http.StatusConflict, "no device attached to this session")
		return
	}
	png, err := adb.New(fmt.Sprintf("localhost:%d", session.ADBPort)).Screenshot()
	if err != nil {
		writeError(w, http.StatusBadGateway, "screenshot failed: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(http.StatusOK)
	w.Write(png)
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

	// Meter the device time this session consumed before we tear it down.
	// Device-minutes were reported to users but never accumulated —
	// IncrementDeviceMinutes had no callers, so the settings meter always read
	// zero while the UI presented it as a real number. Charge the wall-clock
	// life of the session to the owning user's current cycle. This is coarse on
	// purpose: paused time still counts, and a session the server never sees
	// destroyed (crash, forced kill) is never metered. Precise, *enforced*
	// metering is Phase 10; coarse-but-real beats always-zero and stops the
	// meter from lying. The state guard keeps a repeated Destroy idempotent.
	if session.State != "destroyed" && !session.StartedAt.IsZero() {
		if minutes := time.Since(session.StartedAt).Minutes(); minutes > 0 {
			if project, perr := h.store.GetProject(session.ProjectID); perr == nil {
				if err := h.store.IncrementDeviceMinutes(project.UserID, minutes); err != nil {
					log.Printf("WARN: could not meter device minutes for session %d: %v", id, err)
				}
			} else {
				log.Printf("WARN: could not resolve project %d to meter device minutes for session %d: %v", session.ProjectID, id, perr)
			}
		}
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

// waitForDeviceBoot polls until the device reports boot_completed=1.
func (h *SessionHandler) waitForDeviceBoot(adbTarget string, timeout time.Duration) error {
	log.Printf("waiting for device boot completion (target: %s)", adbTarget)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pollCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(pollCtx, "adb", "-s", adbTarget, "shell", "getprop", "sys.boot_completed")
		output, err := cmd.CombinedOutput()
		cancel()
		// A wedged adbd must not wedge provisioning: the per-command timeout
		// converts a hung shell into an empty poll, and the outer deadline
		// still bounds the whole wait.
		if err == nil && strings.TrimSpace(string(output)) == "1" {
			// Extra 3s for services to initialize after boot_completed
			time.Sleep(3 * time.Second)
			log.Printf("device boot completed (target: %s)", adbTarget)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("device did not boot within %v (target: %s)", timeout, adbTarget)
}
