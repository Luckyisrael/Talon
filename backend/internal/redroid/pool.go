// The DV4 warm pool: containers booted before they are needed.
//
// Session create pays ~2m13s waiting for Android to boot. A warm pool
// moves that cost off the user's path: background workers keep N
// containers booted (ADB-ready, dev-client installed), and session create
// claims one — renaming it into the session's name so the rest of the
// lifecycle (reconcile, pause/resume, destroy) sees an ordinary session
// container.
//
// The pool is deliberately small (default 1): each warm container holds
// 1GB RAM and a CPU slice while idle, and on an 8-core/14GB host with
// MAX_CONTAINERS=5, one warm slot is the sweet spot between a fast claim
// and honest headroom. WARM_POOL_SIZE=0 disables the pool entirely —
// create then always boots cold, exactly the pre-DV4 behavior.
package redroid

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Pool keeps warm containers ready to claim.
type Pool struct {
	mgr  *Manager
	size int

	mu    sync.Mutex
	warm  map[string]*WarmEntry // containerID -> entry
	claim chan struct{}         // signal: a slot freed, refill it
	stop  chan struct{}
}

// WarmEntry is one booted container awaiting a session.
type WarmEntry struct {
	ContainerID string
	Port        int
	BootedAt    time.Time
}

// NewPool starts the refill workers. Size 0 returns a pool that does
// nothing — the honest no-op, not a nil pointer every caller must check.
func NewPool(mgr *Manager, size int) *Pool {
	p := &Pool{
		mgr:   mgr,
		size:  size,
		warm:  make(map[string]*WarmEntry),
		claim: make(chan struct{}, size),
		stop:  make(chan struct{}),
	}
	if size > 0 {
		go p.worker()
	}
	return p
}

// worker keeps the pool full: boots a container whenever a slot is free.
// One worker at a time — booting is 2+ minutes of mostly-wait; parallel
// boots would spike CPU for no claim-latency gain.
func (p *Pool) worker() {
	// Boot the first batch immediately, then react to claims.
	for i := 0; i < p.size; i++ {
		p.boot()
	}
	for {
		select {
		case <-p.stop:
			return
		case <-p.claim:
			p.boot()
		}
	}
}

// boot creates one warm container and registers it. Failures log and
// leave the slot empty — the next claim refills; a pool miss is the cold
// path, which always works.
func (p *Pool) boot() {
	// An ephemeral negative session id keeps the allocator and container
	// name honest (a warm container belongs to no session yet). The name
	// is rewritten on claim.
	id, err := p.mgr.portAlloc.Allocate(poolSessionID())
	if err != nil {
		log.Printf("warm pool: port allocation failed: %v", err)
		return
	}
	info, err := p.mgr.CreateContainerWithPort(poolSessionID(), id)
	if err != nil {
		p.mgr.portAlloc.ReleasePort(id)
		log.Printf("warm pool: boot failed (pool stays cold for now): %v", err)
		return
	}
	p.mu.Lock()
	p.warm[info.ContainerID] = &WarmEntry{
		ContainerID: info.ContainerID,
		Port:        info.ADBPort,
		BootedAt:    time.Now(),
	}
	p.mu.Unlock()
	log.Printf("warm pool: container ready (port %d, %d/%d warm)", info.ADBPort, len(p.Warm()), p.size)
}

// poolSessionIDSeq hands each warm boot a unique negative id — negative
// so it can never collide with a real session (store ids start at 1).
var poolSessionIDSeq int64 = -1

func poolSessionID() int64 {
	poolSessionIDSeq--
	return poolSessionIDSeq
}

// Warm returns the current warm entries (for status and the reaper).
func (p *Pool) Warm() []*WarmEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*WarmEntry, 0, len(p.warm))
	for _, e := range p.warm {
		out = append(out, e)
	}
	return out
}

// Claim takes a warm container for a session, renaming it into the
// session's name — from then on it IS a session container and the rest of
// the lifecycle treats it as one. Returns nil when the pool is empty:
// the caller's cold path boots instead (never an error — a pool miss is
// not a failure, just slower).
func (p *Pool) Claim(sessionID int64) *ContainerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, entry := range p.warm {
		delete(p.warm, id)
		// Rename the container to the session's convention so reconcile,
		// pause, and destroy all see an ordinary session container.
		info, err := p.mgr.retagWarmContainer(entry, sessionID)
		if err != nil {
			// A stale warm entry (container died while idle): drop it,
			// keep claiming through the rest of the pool.
			log.Printf("warm pool: claim retag failed (stale entry dropped): %v", err)
			continue
		}
		// Signal the worker to refill the freed slot (non-blocking: the
		// buffered channel holds one signal per slot).
		select {
		case p.claim <- struct{}{}:
		default:
		}
		log.Printf("warm pool: claimed warm container for session %d (booted %s ago)", sessionID, time.Since(entry.BootedAt).Round(time.Second))
		return info
	}
	return nil
}

// Stop drains the pool: warm containers are removed, ports released.
// Called from the server's shutdown path — a pool container must not
// outlive the process that named it.
func (p *Pool) Stop() {
	close(p.stop)
	p.mu.Lock()
	entries := make([]*WarmEntry, 0, len(p.warm))
	for id, e := range p.warm {
		entries = append(entries, e)
		delete(p.warm, id)
	}
	p.mu.Unlock()
	for _, e := range entries {
		p.mgr.forceRemove(e.ContainerID)
		// The port was allocated under the pool's negative id; release by
		// the container's actual port value via the allocator's session
		// key — ReleasePort by value.
		p.mgr.portAlloc.ReleaseByPort(e.Port)
	}
}

// ── Manager side: the two hooks the pool needs ──

// CreateContainerWithPort is CreateContainer with the port pre-allocated
// (the pool allocates before boot so a claim never rebinds ports).
func (m *Manager) CreateContainerWithPort(sessionID int64, port int) (*ContainerInfo, error) {
	return m.createContainer(sessionID, port)
}

// retagWarmContainer renames a claimed warm container into the session's
// name and updates the allocator's booking.
func (m *Manager) retagWarmContainer(entry *WarmEntry, sessionID int64) (*ContainerInfo, error) {
	ctx := context.Background()
	name := fmt.Sprintf("%s%d", containerPrefix, sessionID)
	// Docker rename: the container keeps its ID, ports, and booted state.
	if err := m.client.ContainerRename(ctx, entry.ContainerID, name); err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	// Rebook the port under the real session id.
	m.portAlloc.ReleaseByPort(entry.Port)
	if _, err := m.portAlloc.Allocate(sessionID); err != nil {
		return nil, fmt.Errorf("rebook port: %w", err)
	}
	// The port allocator may hand back a different port on rebook; if it
	// did, the container's binding must match — rebook by exact value.
	m.portAlloc.rebookTo(sessionID, entry.Port)
	return &ContainerInfo{
		ContainerID:   entry.ContainerID,
		ContainerName: name,
		ADBPort:       entry.Port,
		SessionID:     sessionID,
		State:         "running",
		CreatedAt:     entry.BootedAt,
	}, nil
}
