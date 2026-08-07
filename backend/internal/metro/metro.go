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
	port    int
	cmd     *exec.Cmd
	ready   chan struct{}
	workspaceDir string
	wslDistro    string
	target       string
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

func Start(sessionID int64, workspaceDir string) (*Server, error) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()

	if _, exists := sessions[sessionID]; exists {
		return nil, fmt.Errorf("metro already running for session %d", sessionID)
	}

	port, err := FindFreePort()
	if err != nil {
		return nil, fmt.Errorf("failed to find free port: %w", err)
	}

	s := &Server{
		port:         port,
		ready:        make(chan struct{}),
		workspaceDir: workspaceDir,
		wslDistro:    "SeekerLinux",
		target:       "localhost:5555",
	}

	// Start Metro via WSL
	args := []string{"-d", s.wslDistro, "-e", "npx", "expo", "start", "--port", fmt.Sprintf("%d", port), "--non-interactive"}
	s.cmd = exec.Command("wsl", args...)
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

	// Monitor stdout for "Metro waiting on" or similar ready signal
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("[metro %d] %s", sessionID, line)
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

	// Log stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[metro %d err] %s", sessionID, scanner.Text())
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
	args := []string{"-d", s.wslDistro, "-e", "adb", "-s", s.target, "reverse", fmt.Sprintf("tcp:%d", s.port), fmt.Sprintf("tcp:%d", s.port)}
	if err := exec.Command("wsl", args...).Run(); err != nil {
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
	args := []string{"-d", s.wslDistro, "-e", "adb", "-s", s.target, "reverse", "--remove", fmt.Sprintf("tcp:%d", s.port)}
	exec.Command("wsl", args...).Run()

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
