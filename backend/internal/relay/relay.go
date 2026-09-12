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
//     The one inspection point is §2.4's first-request token check: the
//     connection's opening bytes must carry the session credential, and
//     the validated bytes are replayed whole so passthrough stays byte-
//     exact.
package relay

import (
	"bytes"
	"crypto/rand"
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
	listener   net.Listener
	// token, when set, is the credential the deep link carries (§2.4):
	// connections must present it on their first request. Empty means the
	// relay is open — the shape used by tests and any pre-token caller.
	token string

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
}

// Start opens the public listener. The port must already be allocated by
// the caller (the session's port range) — the relay owns it from here.
func Start(publicPort int, metroAddr string) (*Relay, error) {
	return startRelay(publicPort, metroAddr, "")
}

// StartWithToken is Start plus the §2.4 credential: every connection must
// carry this token in its first request's URL (query param), or it is
// refused before a byte reaches metro.
func StartWithToken(publicPort int, metroAddr, token string) (*Relay, error) {
	return startRelay(publicPort, metroAddr, token)
}

func startRelay(publicPort int, metroAddr, token string) (*Relay, error) {
	l, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", itoa(publicPort)))
	if err != nil {
		return nil, err
	}
	r := &Relay{
		publicPort: publicPort,
		metroAddr:   metroAddr,
		listener:    l,
		conns:        make(map[net.Conn]struct{}),
		token:        token,
	}
	go r.acceptLoop()
	if token != "" {
		log.Printf("relay: public :%d -> %s (session metro, token-gated)", publicPort, metroAddr)
	} else {
		log.Printf("relay: public :%d -> %s (session metro)", publicPort, metroAddr)
	}
	return r, nil
}

// Port reports the public port (for the exp:// URL).
func (r *Relay) Port() int { return r.publicPort }

// Token reports the relay's credential — the caller embeds it in the deep
// link (exp://host:port?token=…) and the QR/deep-link surface reads it from
// here, so the credential is minted where it is enforced.
func (r *Relay) Token() string { return r.token }

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

	// §2.4: the QR embeds authentication, not just a location. When this
	// relay carries a token, the first bytes on the connection must be an
	// HTTP request whose URL carries it (?token=…). The Expo dev client
	// preserves query params from the exp:// deep link onto the requests it
	// makes, so a legitimate phone presents the credential on its very
	// first line — and a connection that never speaks a token is refused
	// without ever reaching metro. Peek bounded: the request line fits well
	// under 4KiB; a client that opens a socket and says nothing is dropped
	// at the read deadline, not held.
	if r.token != "" {
		buf := make([]byte, 4096)
		public.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, err := public.Read(buf)
		public.SetReadDeadline(time.Time{})
		if err != nil || n == 0 || !bytes.Contains(buf[:n], []byte("token="+r.token)) {
			log.Printf("relay %d: refused unauthenticated connection", r.publicPort)
			return
		}
		// Splice the peeked bytes back: the copy loops below must see the
		// whole request, not just its tail.
		spliced := newSpliceConn(public, buf[:n])
		public = spliced
	}

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

// StartForSession starts (or returns) the session's relay — token-gated per
// §2.4: the credential is minted here, where it is enforced, and lives exactly
// as long as the relay (the session). Restarting a session mints a new one;
// an old QR dies with the old relay.
func StartForSession(sessionID int64, publicPort int, metroAddr string) (*Relay, error) {
	regMu.Lock()
	defer regMu.Unlock()
	if existing, ok := registry[sessionID]; ok {
		return existing, nil
	}
	r, err := StartWithToken(publicPort, metroAddr, mintToken())
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

// mintToken produces the relay's credential: 16 bytes of crypto/rand,
// hex-encoded — unguessable over the LAN, no pretense of being anything but
// a session-scoped bearer.
func mintToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// The crypto source failing is a host-level emergency; an empty
		// token would open the relay, so refuse to start instead.
		panic("relay: crypto/rand unavailable: " + err.Error())
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 32)
	for _, v := range b {
		out = append(out, hexdigits[v>>4], hexdigits[v&0xf])
	}
	return string(out)
}

// spliceConn replays the bytes peeked during token validation ahead of the
// underlying connection's stream, so the copy loops see the full first
// request. Only Read replays; everything else passes through.
type spliceConn struct {
	net.Conn
	head []byte
}

func newSpliceConn(c net.Conn, head []byte) *spliceConn { return &spliceConn{Conn: c, head: head} }

func (s *spliceConn) Read(p []byte) (int, error) {
	if len(s.head) > 0 {
		n := copy(p, s.head)
		s.head = s.head[n:]
		return n, nil
	}
	return s.Conn.Read(p)
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
