package ws

import (
	"log"
	"net/http"
	"os/exec"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type TerminalHandler struct {
	wslDistro string
	target    string
}

func NewTerminalHandler() *TerminalHandler {
	return &TerminalHandler{
		wslDistro: "SeekerLinux",
		target:    "localhost:5555",
	}
}

func (h *TerminalHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.Handle)
	return r
}

func (h *TerminalHandler) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Spawn adb shell process via WSL
	args := []string{"-d", h.wslDistro, "-e", "adb", "-s", h.target, "shell"}
	cmd := exec.Command("wsl", args...)

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

	log.Printf("adb shell session started (pid %d)", cmd.Process.Pid)

	var mu sync.Mutex
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	// Read from process stdout → WebSocket
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

	// Read from process stderr → WebSocket
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

	// Read from WebSocket → process stdin
	// When this exits (client disconnects), close stdin to signal process to exit
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
		// Client disconnected — close stdin so shell exits
		stdin.Close()
	}()

	// Wait for both readers to finish (process exited)
	<-stdoutDone
	<-stderrDone
	cmd.Wait()

	log.Printf("adb shell session ended (pid %d)", cmd.Process.Pid)

	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shell exited"))
}
