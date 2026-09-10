package relay

import (
	"io"
	"net"
	"testing"
	"time"
)

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
