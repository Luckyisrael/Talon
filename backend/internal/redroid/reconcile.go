package redroid

import (
	"context"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// containerAction is what the reconciler owes a session's container:
// destroy it, resume it from pause, or recover it as-is.
type containerAction int

const (
	actionDestroy containerAction = iota
	actionResume
	actionRecover
)

// decideAction maps (exists, docker state) to the reconciler's duty. Pure
// so the pause-vs-destroy judgement is unit-testable without Docker: paused
// means sleeping (resume), anything halted means dead (destroy), running
// means recover. Unknown states fail toward destroy — a container in a state
// we do not recognize is not one we should trust a session to.
func decideAction(exists bool, state string) containerAction {
	if !exists {
		return actionDestroy
	}
	switch state {
	case "running":
		return actionRecover
	case "paused":
		return actionResume
	default:
		return actionDestroy
	}
}

// Reconcile compares DB session state against actual Docker container state.
// It recovers tracking of running containers and removes orphans.
// This runs on server startup to handle containers that survived a backend restart.
func (m *Manager) Reconcile(s *store.Store) error {
	log.Println("reconciler: starting reconciliation sweep")

	// 1. Get all active sessions from DB (state != "destroyed")
	activeSessions, err := s.GetActiveSessions()
	if err != nil {
		log.Printf("reconciler: failed to get active sessions: %v", err)
		return err
	}

	// 2. List all talon-redroid-* containers from Docker
	dockerContainers, err := m.ListContainers()
	if err != nil {
		log.Printf("reconciler: failed to list docker containers: %v", err)
		return err
	}

	// Build lookup maps
	dbByContainerID := map[string]*store.Session{}
	for i := range activeSessions {
		if activeSessions[i].ContainerID != "" {
			dbByContainerID[activeSessions[i].ContainerID] = &activeSessions[i]
		}
	}

	dockerByID := map[string]ContainerInfo{}
	for _, c := range dockerContainers {
		dockerByID[c.ContainerID] = c
	}

	// 3. For each DB session: verify container exists and is running
	recovered := 0
	markedDestroyed := 0
	for _, session := range activeSessions {
		if session.ContainerID == "" {
			// Session never had a container — skip
			continue
		}

		info, exists := dockerByID[session.ContainerID]
		switch decideAction(exists, info.State) {
		case actionDestroy:
			// Container gone, or exited/crashed — genuinely dead, not sleeping.
			log.Printf("reconciler: session %d container %s missing/stopped (state=%s) → marking destroyed",
				session.ID, shortID(session.ContainerID), info.State)
			s.UpdateSessionState(session.ID, "destroyed")
			s.UpdateSessionContainer(session.ID, "", "", 0)
			m.portAlloc.Release(session.ID)
			markedDestroyed++
		case actionResume:
			// Paused by the reaper for idleness, but the session row is
			// still live — resume it rather than destroying work in
			// progress. Destroying paused containers is what killed
			// mid-turn device flows (gradle compiling while adb sat idle).
			if err := m.UnpauseContainer(session.ContainerID); err != nil {
				log.Printf("reconciler: session %d container %s unpause failed (%v) → marking destroyed",
					session.ID, shortID(session.ContainerID), err)
				s.UpdateSessionState(session.ID, "destroyed")
				s.UpdateSessionContainer(session.ID, "", "", 0)
				m.portAlloc.Release(session.ID)
				markedDestroyed++
				continue
			}
			s.UpdateSessionContainerState(session.ID, "running")
			m.portAlloc.Restore(int64(info.ADBPort), session.ID)
			log.Printf("reconciler: session %d container %s resumed from pause (port %d)",
				session.ID, shortID(session.ContainerID), info.ADBPort)
			recovered++
		default: // actionRecover
			// Container is running — recover port allocation
			m.portAlloc.Restore(int64(info.ADBPort), session.ID)
			log.Printf("reconciler: session %d container %s recovered (port %d)",
				session.ID, shortID(session.ContainerID), info.ADBPort)
			recovered++
		}
	}

	// 4. For each Docker container: if no DB session claims it, it's an orphan
	orphaned := 0
	for _, info := range dockerContainers {
		if _, claimed := dbByContainerID[info.ContainerID]; !claimed {
			log.Printf("reconciler: orphan container %s (session %d, port %d) → removing",
				shortID(info.ContainerID), info.SessionID, info.ADBPort)
			m.forceRemove(info.ContainerID)
			orphaned++
		}
	}

	// 5. Also check for the old legacy container
	m.removeLegacyContainer()

	log.Printf("reconciler: complete — recovered=%d, destroyed=%d, orphaned=%d", recovered, markedDestroyed, orphaned)
	return nil
}

// removeLegacyContainer removes the old single shared container if it exists.
func (m *Manager) removeLegacyContainer() {
	ctx := context.Background()
	opts := container.RemoveOptions{Force: true}
	if err := m.client.ContainerRemove(ctx, "redroid-talon-dev", opts); err != nil {
		if !client.IsErrNotFound(err) {
			log.Printf("reconciler: failed to remove legacy container: %v", err)
		}
	} else {
		log.Println("reconciler: removed legacy container redroid-talon-dev")
	}
}

// StartCleanupLoop runs a periodic reconciliation sweep.
func (m *Manager) StartCleanupLoop(s *store.Store, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := m.Reconcile(s); err != nil {
				log.Printf("reconciler: sweep failed: %v", err)
			}
		}
	}()
	log.Printf("reconciler: cleanup loop started (interval=%v)", interval)
}

// shortID truncates a container id for logs without panicking on short or
// test-injected ids.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
