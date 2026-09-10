package metro

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Server struct {
	port         int
	cmd          *exec.Cmd
	ready        chan struct{}
	workspaceDir string
	target       string
	// events fans classified stdout/stderr lines to subscribers (DV1): the
	// SSE bridge to the preview badge, and the agent's metro_status tool.
	// Nil only in tests that never start a real process.
	events *emitter
}

var (
	sessions   = make(map[int64]*Server)
	sessionsMu sync.Mutex
)

func FindFreePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func Start(sessionID int64, workspaceDir string, adbTarget string) (*Server, error) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	if _, exists := sessions[sessionID]; exists {
		return nil, fmt.Errorf("metro already running for session %d", sessionID)
	}

	port, err := FindFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}

	if adbTarget == "" {
		adbTarget = "localhost:5555"
	}

	s := &Server{
		port:         port,
		ready:        make(chan struct{}),
		workspaceDir: workspaceDir,
		target:       adbTarget,
		events:       &emitter{},
	}

	// Start Metro directly on native Linux (no WSL wrapper).
	// --host lan binds 0.0.0.0: the DV2 relay dials in over TCP from outside
	// localhost, and LAN phones reach the bundler directly when on-network.
	// The adb reverse forward (for the cloud device) still works either way.
	args := []string{"expo", "start", "--host", "lan", "--port", fmt.Sprintf("%d", port), "--non-interactive"}
	s.cmd = exec.Command("npx", args...)
	s.cmd.Dir = workspaceDir

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := s.cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := s.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start metro: %w", err)
	}

	// Monitor stdout for ready signal and classify every line (DV1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		// Metro lines carry paths and occasional stack dumps; the default
		// 64KB cap is generous but a redbox error block can exceed it.
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[metro %d] %s", sessionID, line)
			if ev := classify(line); ev.Kind != "log" {
				s.events.emit(ev)
			}
			if strings.Contains(line, "Metro waiting on") ||
				strings.Contains(line, "ready") ||
				strings.Contains(line, "Waiting on") {
				select {
				case <-s.ready:
				default:
					close(s.ready)
				}
			}
		}
	}()

	// Log stderr — and classify (DV1): metro prints bundler errors here
	// while stdout stays chatty about progress. The ready signal never
	// rides stderr, so this pump only feeds logs and events.
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[metro %d err] %s", sessionID, line)
			if ev := classify(line); ev.Kind != "log" {
				s.events.emit(ev)
			}
		}
	}()

	sessions[sessionID] = s

	// Wait for Metro to be ready (with timeout)
	select {
	case <-s.ready:
		log.Printf("metro ready for session %d on port %d", sessionID, port)
	case <-time.After(60 * time.Second):
		log.Printf("metro startup timeout for session %d, continuing anyway", sessionID)
	}

	// Setup ADB reverse port forwarding
	s.setupADBReverse()

	return s, nil
}

func (s *Server) setupADBReverse() {
	args := []string{"-s", s.target, "reverse", fmt.Sprintf("tcp:%d", s.port), fmt.Sprintf("tcp:%d", s.port)}
	if err := exec.Command("adb", args...).Run(); err != nil {
		log.Printf("adb reverse failed: %v", err)
	} else {
		log.Printf("adb reverse set: tcp:%d -> tcp:%d", s.port, s.port)
	}
}

func Stop(sessionID int64) {
	sessionsMu.Lock()
	s, exists := sessions[sessionID]
	if !exists {
		sessionsMu.Unlock()
		return
	}
	delete(sessions, sessionID)
	sessionsMu.Unlock()

	// Kill Metro process
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}

	// Remove ADB reverse
	args := []string{"-s", s.target, "reverse", "--remove", fmt.Sprintf("tcp:%d", s.port)}
	exec.Command("adb", args...).Run()

	log.Printf("metro stopped for session %d", sessionID)
}

func GetPort(sessionID int64) (int, bool) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	s, exists := sessions[sessionID]
	if !exists {
		return 0, false
	}
	return s.port, true
}

func StopAll() {
	sessionsMu.Lock()
	ids := make([]int64, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sessionsMu.Unlock()

	for _, id := range ids {
		Stop(id)
	}
}

func (s *Server) Port() int {
	return s.port
}

// Subscribe receives this server's classified events until unsubscribed.
// The caller drains or the emitter drops-oldest — never blocks metro.
func (s *Server) Subscribe() (chan Event, func()) {
	ch := s.events.subscribe()
	return ch, func() { s.events.unsubscribe(ch) }
}

// SubscribeSession resolves a running metro by session id. Nil when the
// session has no bundler (not started, or start failed) — callers report
// that honestly instead of fabricating state.
func SubscribeSession(sessionID int64) (chan Event, func()) {
	sessionsMu.Lock()
	s, exists := sessions[sessionID]
	sessionsMu.Unlock()
	if !exists || s.events == nil {
		return nil, nil
	}
	return s.Subscribe()
}
