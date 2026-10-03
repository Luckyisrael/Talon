package relay

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// dialFrom connects to addr from a specific source address. Trust in the
// relay is per-phone, so a test that models two phones needs two source
// hosts — 127.0.0.1/2/3 are all loopback and all reachable on Linux.
func dialFrom(addr, local string) (net.Conn, error) {
	laddr, err := net.ResolveTCPAddr("tcp", local+":0")
	if err != nil {
		return nil, err
	}
	d := net.Dialer{LocalAddr: laddr, Timeout: 2 * time.Second}
	return d.Dial("tcp", addr)
}

// A relay passes bytes both ways and dies cleanly. Real sockets, no mocks —
// the relay is transport and its test is bytes on the wire.
func TestRelayPassthrough(t *testing.T) {
	// A fake metro: an echo server on a random local port.
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro listen: %v", err)
	}
	defer metro.Close()
	go func() {
		for {
			c, err := metro.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(c)
		}
	}()

	// The relay on another random port.
	r, err := Start(freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer r.Stop()

	// A "phone" connects to the relay and echoes prove the path.
	phone, err := net.DialTimeout("tcp", r.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("phone dial: %v", err)
	}
	defer phone.Close()

	msg := []byte("GET /index.bundle?platform=android HTTP/1.1\r\n\r\n")
	if _, err := phone.Write(msg); err != nil {
		t.Fatalf("phone write: %v", err)
	}
	buf := make([]byte, len(msg))
	phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(phone, buf); err != nil {
		t.Fatalf("phone read: %v", err)
	}
	if string(buf) != string(msg) {
		t.Errorf("passthrough mangled the bytes: %q", buf)
	}
}

// Stop drops live connections and frees the port: the session teardown law.
func TestRelayStopCutsConnections(t *testing.T) {
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro listen: %v", err)
	}
	defer metro.Close()

	r, err := Start(freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}

	phone, err := net.DialTimeout("tcp", r.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("phone dial: %v", err)
	}
	defer phone.Close()

	r.Stop()

	// The live phone connection is cut: the read side errors or EOFs.
	phone.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := phone.Read(buf); err == nil {
		// One grace byte may slip through the close race; a second read
		// must fail. The law is "the socket cannot outlive the relay".
		phone.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := phone.Read(buf); err == nil {
			t.Error("fault: connection survived relay Stop")
		}
	}

	// The listener is gone: a fresh dial is refused.
	if _, err := net.DialTimeout("tcp", r.listener.Addr().String(), 1*time.Second); err == nil {
		t.Error("fault: relay port still accepting after Stop")
	}

	// Idempotent.
	r.Stop()
}

// The registry: one relay per session, StartForSession is idempotent,
// StopSession tears it down and forgets it.
func TestRegistryLifecycle(t *testing.T) {
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro listen: %v", err)
	}
	defer metro.Close()

	r1, err := StartForSession(999, freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	r2, err := StartForSession(999, freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if r1 != r2 {
		t.Error("fault: session got two relays")
	}
	if GetSession(999) != r1 {
		t.Error("fault: registry lookup missed")
	}
	StopSession(999)
	if GetSession(999) != nil {
		t.Error("fault: registry kept a stopped relay")
	}
	StopSession(999) // idempotent
}

// Conns cap: the ninth simultaneous phone is refused, the first eight pass.
func TestConnCap(t *testing.T) {
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro listen: %v", err)
	}
	defer metro.Close()
	accepted := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := metro.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	r, err := Start(freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer r.Stop()

	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < maxConns; i++ {
		c, err := net.DialTimeout("tcp", r.listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	// Let the relays dial upstream.
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; i < maxConns; i++ {
		select {
		case <-accepted:
		case <-time.After(time.Until(deadline)):
			t.Fatalf("upstream %d never dialed", i)
		}
	}

	// The cap-plus-one'th connection is dropped immediately.
	extra, err := net.DialTimeout("tcp", r.listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("extra dial should connect then be cut: %v", err)
	}
	defer extra.Close()
	extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := extra.Read(make([]byte, 1)); err == nil {
		t.Error("fault: connection beyond the cap was served")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// ── §2.4: the relay is a door with a key, not an open port ──

// tokenRelayFixture starts an echo "metro" plus a token-gated relay against it.
func tokenRelayFixture(t *testing.T) (*Relay, string) {
	t.Helper()
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro listen: %v", err)
	}
	t.Cleanup(func() { metro.Close() })
	go func() {
		for {
			c, err := metro.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(c)
		}
	}()
	r, err := StartWithToken(freePort(t), metro.Addr().String(), "deadbeefcafebabe")
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	t.Cleanup(r.Stop)
	return r, r.listener.Addr().String()
}

func TestTokenGateAcceptsValidAndRefusesBad(t *testing.T) {
	_, addr := tokenRelayFixture(t)

	// A request carrying the token passes and is answered (echo proves the
	// full first request survived the peek+splice).
	phone, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	req := "GET /index.bundle?platform=android&token=deadbeefcafebabe HTTP/1.1\r\n\r\n"
	if _, err := phone.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, len(req))
	if _, err := io.ReadFull(phone, buf); err != nil {
		t.Fatalf("token-carrying request got no answer: %v", err)
	}
	if string(buf) != req {
		t.Errorf("spliced request mangled: %q", buf)
	}

	// A different phone, with no token on its first request, is refused.
	// The relay trusts an address only after it presented the credential,
	// so an unauthenticated phone never reaches metro even after another
	// phone authenticated. Dial from a distinct source address: a real
	// phone is a distinct host, and trust is per-phone, not per-socket.
	sneak, err := dialFrom(addr, "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	defer sneak.Close()
	if _, err := sneak.Write([]byte("GET /index.bundle?platform=android HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	sneak.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := sneak.Read(make([]byte, 64)); err == nil {
		t.Error("tokenless request reached metro — the gate is open")
	}

	// A tampered token is refused too.
	tamper, err := dialFrom(addr, "127.0.0.3")
	if err != nil {
		t.Fatal(err)
	}
	defer tamper.Close()
	if _, err := tamper.Write([]byte("GET /x?token=deadbeefcafebabf HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	tamper.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := tamper.Read(make([]byte, 64)); err == nil {
		t.Error("tampered token reached metro")
	}
}

// An authenticated phone's later connections are already trusted: the Expo
// dev client drops the deep link's query param when it reduces the URL to
// host:port, so the credential can only be presented once — on the first
// request of the first connection. Bundle reloads and HMR reopen sockets
// that carry no token; they must still work.
func TestTokenTrustSurvivesReconnect(t *testing.T) {
	_, addr := tokenRelayFixture(t)

	first, err := dialFrom(addr, "127.0.0.4")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	req := "GET /index.bundle?platform=android&token=deadbeefcafebabe HTTP/1.1\r\n\r\n"
	if _, err := first.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	first.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(first, make([]byte, len(req))); err != nil {
		t.Fatalf("handshake request got no answer: %v", err)
	}

	// A later connection from the same phone carries no token — the client
	// rebuilt the URL from host:port alone. It must still reach metro.
	again, err := dialFrom(addr, "127.0.0.4")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	again.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := again.Write([]byte("GET /inspector/network HTTP/1.1\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Read(make([]byte, 32)); err != nil {
		t.Fatalf("authenticated phone's tokenless reconnect was refused: %v", err)
	}
}

// The token dies with the relay: StopSession then a fresh StartForSession
// mints a new credential — an old QR must not open a new session's door.
func TestTokenDiesWithRelay(t *testing.T) {
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { metro.Close() })
	go func() {
		for {
			c, err := metro.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(c)
		}
	}()

	r1, err := StartForSession(901, freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	old := r1.Token()
	StopSession(901)

	r2, err := StartForSession(901, freePort(t), metro.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer StopSession(901)
	if r2.Token() == "" || r2.Token() == old {
		t.Fatalf("token reuse across relay restarts: old=%q new=%q", old, r2.Token())
	}
}

// FM-RELAYa: an unreachable upstream owes the client an HTTP status, never
// a silent close. The relay is wired to a port nothing listens on — the
// exact window the server itself creates (log: "metro startup timeout …
// continuing anyway" at 05:47:33 wired the relay, metro began listening at
// 05:47:40 — seven seconds of refused dials the server made reachable).
//
// RED pre-fix: serveConn returns into a silent close — the phone reads zero
// bytes, the exact shape that killed an unguarded API fetch with
// "SocketError: other side closed" (journey3b.mjs:446). GREEN: a status
// line the client can classify and retry from.
func TestRelayAnswers503WhenUpstreamIsDown(t *testing.T) {
	dead := freePort(t)
	r, err := Start(freePort(t), fmt.Sprintf("127.0.0.1:%d", dead))
	if err != nil {
		t.Fatalf("relay start: %v", err)
	}
	defer r.Stop()

	phone, err := net.DialTimeout("tcp", r.listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("phone dial: %v", err)
	}
	defer phone.Close()

	if _, err := phone.Write([]byte("GET /index.bundle?platform=android HTTP/1.1\r\nHost: relay\r\n\r\n")); err != nil {
		t.Fatalf("phone write: %v", err)
	}
	phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, _ := io.ReadAll(phone)
	if len(resp) == 0 {
		t.Fatalf("FM-RELAYa: upstream down (%s) — the relay closed with ZERO bytes; an API client cannot classify the failure, and an unguarded fetch dies on exactly this (SocketError: other side closed)", r.metroAddr)
	}
	got := string(resp)
	if !strings.Contains(got, "503 Service Unavailable") {
		t.Errorf("want a 503 status line, got: %q", got)
	}
	if !strings.Contains(got, "Retry-After: 5") {
		t.Errorf("want Retry-After: 5 so the client knows a retry is cheap, got: %q", got)
	}
	if !strings.Contains(got, "Connection: close") {
		t.Errorf("want Connection: close, got: %q", got)
	}
}
