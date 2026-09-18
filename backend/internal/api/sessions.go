package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/adb"
	"github.com/luckyisrael/talon/backend/internal/devclient"
	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/device"
	"github.com/luckyisrael/talon/backend/internal/metro"
	"github.com/luckyisrael/talon/backend/internal/relay"
	"github.com/luckyisrael/talon/backend/internal/redroid"
	"github.com/luckyisrael/talon/backend/internal/store"
)

type SessionHandler struct {
	store        *store.Store
	manager      *redroid.Manager
	portAlloc    *redroid.PortAllocator
	// relayAlloc hands the DV2 phone-relay its public ports — a disjoint
	// range from the containers' (main wires 17555-18555 vs 15555-16555).
	relayAlloc   *redroid.PortAllocator
	// pool is the DV4 warm pool — pre-booted containers claimed on create.
	// nil means no pool (WARM_POOL_SIZE=0): create boots cold, the pre-DV4
	// behavior.
	pool         *redroid.Pool
	workspaceDir string
	devClient    *devclient.Manager
	wallet       *devclient.WalletManager
	// notifier is E: reaper pause/destroy pushes. Fire-and-forget by law.
	notifier notify.Notifier
}

func NewSessionHandler(s *store.Store, mgr *redroid.Manager, portAlloc *redroid.PortAllocator, relayAlloc *redroid.PortAllocator, pool *redroid.Pool, workspaceDir string, dc *devclient.Manager, wm *devclient.WalletManager, n notify.Notifier) *SessionHandler {
	return &SessionHandler{
		store:        s,
		manager:      mgr,
		portAlloc:    portAlloc,
		relayAlloc:   relayAlloc,
		pool:         pool,
		workspaceDir: workspaceDir,
		devClient:    dc,
		wallet:       wm,
		notifier:     n,
	}
}

// notifyUser is the session surface's fire-and-forget send (the turn surface
// has its own; both converge on notify.Dispatch).
func (h *SessionHandler) notifyUser(userID int64, kind notify.Kind, title, body string) {
	if h.notifier == nil {
		return
	}
	notify.Dispatch(h.store, h.notifier, userID, kind, title, body)
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

	// DV4: a warm container is claimed first — booted in the background
	// minutes ago, renamed into this session's name. A pool miss is not an
	// error: the cold path below boots fresh, exactly the pre-pool flow.
	var containerInfo *redroid.ContainerInfo
	if h.pool != nil {
		containerInfo = h.pool.Claim(session.ID)
	}
	if containerInfo == nil {
		var err error
		containerInfo, err = h.manager.CreateContainer(session.ID)
		if err != nil {
			log.Printf("ERROR: container creation failed for session %d: %v", session.ID, err)
			h.store.UpdateSessionState(session.ID, "destroyed")
			writeError(w, http.StatusServiceUnavailable, "failed to create device container: "+err.Error())
			return
		}
	} else {
		log.Printf("session %d: claimed a warm container (boot was prepaid)", session.ID)
	}

	// Persist container metadata
	h.store.UpdateSessionContainer(session.ID, containerInfo.ContainerID, containerInfo.ContainerName, containerInfo.ADBPort)
	h.store.UpdateSessionContainerState(session.ID, "running")

	adbTarget := "localhost:" + strconv.Itoa(containerInfo.ADBPort)

	// Wait for device boot before any ADB operations
	if err := h.waitForDeviceBoot(adbTarget, device.BootBudget); err != nil {
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

	// DV2: the phone relay — a public TCP port bridging to this session's
	// metro, so a phone on any network can load the app. Starts only when
	// metro did; a session without a bundler has nothing to relay. The
	// relay dies with the session (Destroy) — a phone left loading
	// overnight must not hold a port.
	if metroServer != nil && h.relayAlloc != nil {
		relayPort, err := h.relayAlloc.Allocate(session.ID)
		if err != nil {
			log.Printf("WARN: relay port allocation failed for session %d: %v", session.ID, err)
		} else if _, err := relay.StartForSession(session.ID, relayPort, fmt.Sprintf("127.0.0.1:%d", metroServer.Port())); err != nil {
			h.relayAlloc.Release(session.ID)
			log.Printf("WARN: relay start failed for session %d: %v", session.ID, err)
		} else {
			log.Printf("session %d: phone relay live on public port %d", session.ID, relayPort)
		}
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

// LiveForProject serves GET /v1/projects/{projectID}/live-session: the
// project's most recent non-destroyed session, or 204 when none exists.
// This is the refresh lifeline — a page reload must re-attach to the device
// the user already has instead of silently starting a new one. Ownership is
// the project row's user id; a foreign project is 404, never a row.
func (h *SessionHandler) LiveForProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	project, err := h.store.GetProject(projectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	session, err := h.store.LatestLiveSession(projectID)
	if err != nil || session == nil {
		// No live session is the common case, not an error — the honest
		// empty is 204 with no body, so the frontend shows "idle" rather
		// than parsing an error.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// A page load is user activity — the same signal a stream join sends
	// (ws/stream.go). Without it, a user who refreshes every few minutes
	// would have their session reaped as idle despite watching it.
	_ = h.store.TouchSession(session.ID)

	response := map[string]interface{}{
		"id":              session.ID,
		"project_id":      session.ProjectID,
		"state":           session.State,
		"started_at":      session.StartedAt,
		"adb_port":        session.ADBPort,
		"container_id":    session.ContainerID,
		"container_state": session.ContainerState,
	}
	if port, ok := metro.GetPort(session.ID); ok {
		response["metro_port"] = port
	}

	writeJSON(w, http.StatusOK, response)
}

// Screenshot captures one PNG frame from the session's device for download
// or store-asset use. Same ownership posture as Get: session ids route here,
// and the adb target comes from the session row, never the request.
// A copy lands in the project's app/assets/screenshots/ (best-effort, never
// failing the request): captures used to live only in the caller's /tmp and
// died with it. Unreferenced PNGs are not bundled by Metro, so the capture
// history does not bloat the APK — and the agent can read its own captures
// back for vision self-review.
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
	h.persistCapture(session.ProjectID, id, png)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(png)))
	w.WriteHeader(http.StatusOK)
	w.Write(png)
}

// persistCapture files one screenshot under the project's capture history.
// Best-effort by contract: logging, never erroring — a failed write must
// not turn a good capture into a failed request.
func (h *SessionHandler) persistCapture(projectID, sessionID int64, png []byte) {
	dir := filepath.Join(h.workspaceDir, strconv.FormatInt(projectID, 10), "app", "assets", "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("capture persist: mkdir %s: %v", dir, err)
		return
	}
	name := fmt.Sprintf("session-%d-%s.png", sessionID, time.Now().UTC().Format("20060102-150405"))
	if err := os.WriteFile(filepath.Join(dir, name), png, 0o644); err != nil {
		log.Printf("capture persist: write %s: %v", name, err)
	}
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
	// DV2: the phone relay dies with the session, port released back.
	relay.StopSession(id)
	if h.relayAlloc != nil {
		h.relayAlloc.Release(id)
	}
	metro.Stop(id)

	// Destroy the Docker container
	if session.ContainerID != "" && h.manager != nil {
		if err := h.manager.StopContainer(session.ContainerID); err != nil {
			log.Printf("WARN: container destroy failed for session %d: %v", id, err)
		}
		// ContainerRemove returns before the daemon releases the port
		// binding; handing the port straight back to the pool races a
		// destroy→create pair into "address already in use" (caught live).
		// Wait bounded for the bind, then release either way — the next
		// claim retries its own bind and the reconciler sweeps stragglers.
		if port := h.portAlloc.PortOf(id); port != 0 {
			if !h.manager.WaitForPortFree(port, 15*time.Second) {
				log.Printf("WARN: session %d port %d still bound 15s after destroy — releasing anyway; reconciler will sweep", id, port)
			}
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
