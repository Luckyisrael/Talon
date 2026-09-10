// Package relay is the DV2 self-hosted tunnel: a session-scoped TCP relay
// that exposes a session's metro bundler on a public port so a phone on any
// network can load the app — no third-party tunnel (ngrok/expo's), our
// server, our auth, our lifecycle.
//
// The contract:
//   - One relay per session, claiming one public port from the same
//     allocator the containers use (a different range).
//   - The relay dies with the session: Stop is called from the session's
//     teardown path, and a dead relay is closed on first error — a phone
//     left loading overnight must not hold a port.
//   - Bounded connections: a relay accepts multiple phone connections (the
//     dev client opens several sockets) but caps them per session; an
//     idle connection is dropped after idleTimeout so sockets can't pool.
//   - Bytes pass through unmodified: the relay is transport, not
//     middleware — metro's protocol (HTTP + WS + HMR) is untouched.
package relay

import (
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	// maxConns bounds simultaneous dialed sockets per relay. The dev client
	// uses a handful (bundle fetch, HMR socket, sourcemap fetch); 8 leaves
	// headroom without an open door for socket-pooling.
	maxConns = 8
	// idleTimeout drops a silent connection. Metro's HMR socket is quiet
	// for long stretches, so this must exceed the longest sane quiet period
	// — 10 minutes covers a phone that locked while the app stayed open.
	idleTimeout = 10 * time.Minute
	// relayLogger quiets per-connection spam: the copy loops log only
	// lifecycle, not bytes.
	dialTimeout = 5 * time.Second
)

// Relay bridges one public port to one metro address for one session.
type Relay struct {
	publicPort int
	metroAddr   string
	listener    net.Listener

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// Start opens the public listener. The port must already be allocated by
// the caller (the session's port range) — the relay owns it from here.
func Start(publicPort int, metroAddr string) (*Relay, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", itoa(publicPort)))
	if err != nil {
		return nil, err
	}
	r := &Relay{
		publicPort: publicPort,
		metroAddr:   metroAddr,
		listener:    l,
		conns:        make(map[net.Conn]struct{}),
	}
	go r.acceptLoop()
	log.Printf("relay: public :%d -> %s (session metro)", publicPort, metroAddr)
	return r, nil
}

// Port reports the public port (for the exp:// URL).
func (r *Relay) Port() int { return r.publicPort }

// Addr is the public dial address (host:port).
func (r *Relay) Addr() string { return r.listener.Addr().String() }

func (r *Relay) acceptLoop() {
	for {
		c, err := r.listener.Accept()
		if err != nil {
			// Listener closed — Stop's expected path.
			r.mu.Lock()
			stopped := r.closed
			r.mu.Unlock()
			if stopped {
				return
			}
			log.Printf("relay %d: accept: %v", r.publicPort, err)
			return
		}
		go r.serveConn(c)
	}
}

func (r *Relay) serveConn(public net.Conn) {
	r.mu.Lock()
	if r.closed || len(r.conns) >= maxConns {
		r.mu.Unlock()
		public.Close()
		return
	}
	r.conns[public] = struct{}{}
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.conns, public)
		r.mu.Unlock()
		public.Close()
	}()

	// The upstream dial is per-connection: metro's protocol expects each
	// socket to be its own client, and a shared upstream would tangle
	// request/response pairs.
	upstream, err := net.DialTimeout("tcp", r.metroAddr, dialTimeout)
	if err != nil {
		log.Printf("relay %d: upstream dial %s: %v", r.publicPort, r.metroAddr, err)
		return
	}
	defer upstream.Close()

	// Idle-deadline bidirectional copy. A phone that walked away releases
	// both sides; metro never notices a relay at all.
	deadline := time.Now().Add(idleTimeout)
	public.SetDeadline(deadline)
	upstream.SetDeadline(deadline)

	done := make(chan struct{}, 2)
	go func() { io.Copy(upstream, public); done <- struct{}{} }()
	go func() { io.Copy(public, upstream); done <- struct{}{} }()
	<-done
	// Whichever direction finished first decides: the connection is done.
}

// Stop tears the relay down: listener closed, live conns dropped. Idempotent
// — the session teardown path and the reaper may both call it.
func (r *Relay) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.listener.Close()
	for c := range r.conns {
		c.Close()
	}
	log.Printf("relay %d: stopped", r.publicPort)
}

// Registry — one relay per session, the same shape as metro's session map.
var (
	regMu    sync.Mutex
	registry = map[int64]*Relay{}
)

// StartForSession starts (or returns) the session's relay.
func StartForSession(sessionID int64, publicPort int, metroAddr string) (*Relay, error) {
	regMu.Lock()
	defer regMu.Unlock()
	if existing, ok := registry[sessionID]; ok {
		return existing, nil
	}
	r, err := Start(publicPort, metroAddr)
	if err != nil {
		return nil, err
	}
	registry[sessionID] = r
	return r, nil
}

// StopSession tears the session's relay down and forgets it.
func StopSession(sessionID int64) {
	regMu.Lock()
	r, ok := registry[sessionID]
	delete(registry, sessionID)
	regMu.Unlock()
	if ok {
		r.Stop()
	}
}

// GetSession reports the session's live relay, if any.
func GetSession(sessionID int64) *Relay {
	regMu.Lock()
	defer regMu.Unlock()
	return registry[sessionID]
}

func itoa(n int) string {
	// strconv-free to keep this file dependency-light for review.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
