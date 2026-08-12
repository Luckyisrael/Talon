package redroid

import (
	"fmt"
	"sync"
)

// PortAllocator manages a pool of ADB ports for Redroid containers.
// It guarantees no two concurrent sessions share the same port.
type PortAllocator struct {
	minPort int
	maxPort int
	ports   map[int]int64 // port -> sessionID
	mu      sync.Mutex
}

// NewPortAllocator creates a port allocator for the given range.
func NewPortAllocator(minPort, maxPort int) *PortAllocator {
	return &PortAllocator{
		minPort: minPort,
		maxPort: maxPort,
		ports:   make(map[int]int64),
	}
}

// Allocate reserves a port for the given session.
// Returns an error if all ports are exhausted.
func (pa *PortAllocator) Allocate(sessionID int64) (int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	for port := pa.minPort; port <= pa.maxPort; port++ {
		if _, taken := pa.ports[port]; !taken {
			pa.ports[port] = sessionID
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free ADB ports available (range %d-%d exhausted)", pa.minPort, pa.maxPort)
}

// Release frees a port previously allocated to a session.
func (pa *PortAllocator) Release(sessionID int64) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	for port, sid := range pa.ports {
		if sid == sessionID {
			delete(pa.ports, port)
			return
		}
	}
}

// ReleasePort frees a specific port.
func (pa *PortAllocator) ReleasePort(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	delete(pa.ports, port)
}

// IsFull returns true if all ports in the pool are allocated.
func (pa *PortAllocator) IsFull() bool {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return len(pa.ports) >= (pa.maxPort - pa.minPort + 1)
}

// ActiveCount returns the number of currently allocated ports.
func (pa *PortAllocator) ActiveCount() int {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return len(pa.ports)
}

// PortForSession returns the port allocated to a session, or 0 if not found.
func (pa *PortAllocator) PortForSession(sessionID int64) int {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	for port, sid := range pa.ports {
		if sid == sessionID {
			return port
		}
	}
	return 0
}

// SessionForPort returns the session owning a port, or 0 if not found.
func (pa *PortAllocator) SessionForPort(port int) int64 {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.ports[port]
}

// Restore registers a port-session mapping (used during reconciliation).
func (pa *PortAllocator) Restore(port, sessionID int64) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	pa.ports[int(port)] = sessionID
}
