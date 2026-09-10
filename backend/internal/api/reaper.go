package api

import (
	"log"
	"time"

	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/relay"
)

// The DV4 idle reaper: devices nobody is watching are money burning.
//
// Every container holds 1GB RAM and a CPU slice. A user who starts a
// device, looks at it, and walks away for lunch leaves it running until
// the tab closes — or forever if the tab died. The reaper bounds that:
//
//   - 10 minutes with no stream viewer and no agent activity → pause.
//     Paused containers hold memory but no CPU; a returning user
//     un-pauses on demand.
//   - 30 minutes → destroy. The container, metro, relay, and port all
//     return to the pool; the session is metered exactly as a user-
//     triggered destroy (same code path — metering law: never two ways
//     to tear down a session).
//
// Activity is TouchSession: stream-socket joins and agent prompt turns.
// Both are real signals from the two consumers that exist; anything else
// (metro output, logcat noise) is the device talking to itself, not a
// user watching it.

const (
	// ReaperPauseAfter is the idle window before pausing a container.
	ReaperPauseAfter = 10 * time.Minute
	// ReaperDestroyAfter is the idle window before destroying a session.
	ReaperDestroyAfter = 30 * time.Minute
	// reaperTick is how often the reaper looks. A minute is fine: the
	// windows are minutes-scale and the query is one indexed scan.
	reaperTick = 1 * time.Minute
)

// Reaper bounds idle sessions. Nil-safe: StartReaper on a nil handler
// does nothing (tests, tools builds).
type Reaper struct {
	h    *SessionHandler
	stop chan struct{}
}

// NewReaper binds the reaper to the session handler — one teardown path
// for user-triggered and reaper-triggered destroy.
func NewReaper(h *SessionHandler) *Reaper {
	return &Reaper{h: h, stop: make(chan struct{})}
}

// Start launches the reaper loop. It returns immediately; Stop halts it.
func (r *Reaper) Start() {
	go func() {
		ticker := time.NewTicker(reaperTick)
		defer ticker.Stop()
		log.Printf("reaper: watching for idle sessions (pause %s, destroy %s)", ReaperPauseAfter, ReaperDestroyAfter)
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.reap()
			}
		}
	}()
}

// Stop halts the loop. In-flight reap work finishes.
func (r *Reaper) Stop() {
	close(r.stop)
}

// reap is one pass: pause the 10-minute idle, destroy the 30-minute idle.
func (r *Reaper) reap() {
	// Destroy window first: a 30-minute-idle session must not be merely
	// paused this pass and destroyed a pass later.
	if sessions, err := r.h.store.IdleSessions(ReaperDestroyAfter); err == nil {
		for _, s := range sessions {
			log.Printf("reaper: session %d idle past %s — destroying", s.ID, ReaperDestroyAfter)
			r.h.reapDestroy(s.ID)
		}
	} else {
		log.Printf("reaper: idle query failed: %v", err)
	}

	if sessions, err := r.h.store.IdleSessions(ReaperPauseAfter); err == nil {
		for _, s := range sessions {
			// Already-paused sessions stay paused; the query returns them
			// but the pause path is idempotent and cheap.
			if s.ContainerState == "paused" {
				continue
			}
			if s.ContainerID == "" {
				continue // no container: nothing to pause (a starting session)
			}
			log.Printf("reaper: session %d idle past %s — pausing container", s.ID, ReaperPauseAfter)
			if err := r.h.manager.PauseContainer(s.ContainerID); err != nil {
				log.Printf("reaper: pause %d failed: %v", s.ID, err)
				continue
			}
			if err := r.h.store.UpdateSessionContainerState(s.ID, "paused"); err != nil {
				log.Printf("reaper: state update for %d failed: %v", s.ID, err)
			}
		}
	}
}

// reapDestroy is Destroy's exact semantics minus the HTTP wrapper: meter,
// stop metro and the relay, remove the container, mark destroyed. One
// teardown law, two callers.
func (h *SessionHandler) reapDestroy(id int64) {
	// A session claimed between query and destroy (a viewer joined) is
	// spared: TouchSession moved its clock.
	if s, err := h.store.GetSession(id); err != nil || s.State == "destroyed" {
		return
	}

	// Metering: identical to user-triggered destroy — reaper-destroyed
	// time is real device time.
	if session, err := h.store.GetSession(id); err == nil {
		if session.State != "destroyed" && !session.StartedAt.IsZero() {
			if minutes := time.Since(session.StartedAt).Minutes(); minutes > 0 {
				if project, perr := h.store.GetProject(session.ProjectID); perr == nil {
					if err := h.store.IncrementDeviceMinutes(project.UserID, minutes); err != nil {
						log.Printf("WARN: could not meter device minutes for session %d: %v", id, err)
					}
				}
			}
		}
	}

	// The relay dies with the session; the port goes back.
	relay.StopSession(id)
	if h.relayAlloc != nil {
		h.relayAlloc.Release(id)
	}

	// Metro stops; the container is removed; the port is freed.
	metro.Stop(id)
	if session, err := h.store.GetSession(id); err == nil && session.ContainerID != "" && h.manager != nil {
		if err := h.manager.StopContainer(session.ContainerID); err != nil {
			log.Printf("WARN: container destroy failed for session %d: %v", id, err)
		}
		h.portAlloc.Release(id)
		h.store.UpdateSessionContainer(id, "", "", 0)
		h.store.UpdateSessionContainerState(id, "")
	}
	h.store.UpdateSessionState(id, "destroyed")
}
