package ws

import (
	"log"
	"net/http"
	"os/exec"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/luckyisrael/talon/backend/internal/auth"
	"github.com/luckyisrael/talon/backend/internal/store"
)

type TerminalHandler struct {
	store   *store.Store
	authSvc *auth.Service
}

func NewTerminalHandler(authSvc *auth.Service, s *store.Store) *TerminalHandler {
	return &TerminalHandler{
		store:   s,
		authSvc: authSvc,
	}
}

func (h *TerminalHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", h.Handle)
	return r
}

func (h *TerminalHandler) Handle(w http.ResponseWriter, r *http.Request) {
	device, ok := authorize(w, r, h.authSvc, h.store)
	if !ok {
		return
	}
	sessionID, adbTarget := device.ID, device.ADBTarget

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Spawn adb shell process directly (native Linux). The command dies when the
	// request context does, so a disconnected browser cannot leave a shell behind.
	cmd := exec.CommandContext(r.Context(), "adb", "-s", adbTarget, "shell")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Printf("stdin pipe failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Failed to create shell: "+err.Error()))
		return
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("stdout pipe failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Failed to create shell: "+err.Error()))
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		log.Printf("stderr pipe failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Failed to create shell: "+err.Error()))
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("process start failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Failed to start shell: "+err.Error()))
		return
	}

	log.Printf("adb shell session started (session %d, target %s, pid %d)", sessionID, adbTarget, cmd.Process.Pid)

	var mu sync.Mutex
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	// Read from process stdout -> WebSocket
	go func() {
		defer close(stdoutDone)
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				mu.Lock()
				conn.WriteMessage(websocket.BinaryMessage, buf[:n])
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Read from process stderr -> WebSocket
	go func() {
		defer close(stderrDone)
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				mu.Lock()
				conn.WriteMessage(websocket.BinaryMessage, buf[:n])
				mu.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Read from WebSocket -> process stdin
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				break
			}
			mu.Lock()
			stdin.Write(message)
			mu.Unlock()
		}
		stdin.Close()
	}()

	<-stdoutDone
	<-stderrDone
	cmd.Wait()

	log.Printf("adb shell session ended (session %d, pid %d)", sessionID, cmd.Process.Pid)

	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"))
}
