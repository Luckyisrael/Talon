package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/scrcpy"
	"github.com/seekersstudio/backend/internal/store"
)

// InputMessage represents a touch/key input from the browser.
type InputMessage struct {
	Type     string `json:"type"`
	X        int    `json:"x,omitempty"`
	Y        int    `json:"y,omitempty"`
	X1       int    `json:"x1,omitempty"`
	Y1       int    `json:"y1,omitempty"`
	X2       int    `json:"x2,omitempty"`
	Y2       int    `json:"y2,omitempty"`
	Duration int    `json:"duration,omitempty"`
	Keycode  string `json:"keycode,omitempty"`
}

// InputHandler handles the decoupled input WebSocket.
// This is fully independent of video — input continues working even if
// video capture is stalled, degraded, or disabled entirely.
type InputHandler struct {
	store   *store.Store
	authSvc *auth.Service
}

func NewInputHandler(authSvc *auth.Service, s *store.Store) *InputHandler {
	return &InputHandler{
		store:   s,
		authSvc: authSvc,
	}
}

func (h *InputHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", h.Handle)
	return r
}

func (h *InputHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Authenticate via query param (browsers can't set WS headers)
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
		return
	}
	if _, err := h.authSvc.ValidateToken(token); err != nil {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return
	}

	// Resolve session → ADB target
	sessionID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid session id"}`, http.StatusBadRequest)
		return
	}

	session, err := h.store.GetSession(sessionID)
	if err != nil || session.ADBPort == 0 {
		http.Error(w, `{"error":"session not found or no device attached"}`, http.StatusNotFound)
		return
	}

	adbTarget := fmt.Sprintf("localhost:%d", session.ADBPort)

	// Initialize scrcpy control client for persistent input
	controlClient := scrcpy.NewControlClient(adbTarget)
	if err := controlClient.Start(); err != nil {
		log.Printf("WARNING: scrcpy control client failed to start for session %d: %v", sessionID, err)
		// Fall back to ADB shell input (higher latency but works)
		controlClient = nil
	}
	defer func() {
		if controlClient != nil {
			controlClient.Close()
		}
	}()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("input websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("input session started for session %d (target %s)", sessionID, adbTarget)

	var writeMu sync.Mutex
	done := make(chan struct{})

	// WebSocket read loop — input dispatch
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("input read ended: %v", err)
				return
			}

			var input InputMessage
			if err := json.Unmarshal(msg, &input); err != nil {
				continue
			}

			var inputErr error
			var latency time.Duration
			start := time.Now()

			switch input.Type {
			case "tap":
				if controlClient != nil {
					inputErr = controlClient.Tap(input.X, input.Y)
				} else {
					inputErr = adbTap(adbTarget, input.X, input.Y)
				}
			case "swipe":
				duration := input.Duration
				if duration <= 0 {
					duration = 300
				}
				if controlClient != nil {
					inputErr = controlClient.Swipe(input.X1, input.Y1, input.X2, input.Y2, duration)
				} else {
					inputErr = adbSwipe(adbTarget, input.X1, input.Y1, input.X2, input.Y2, duration)
				}
			case "key":
				if controlClient != nil {
					keycode := parseKeycode(input.Keycode)
					inputErr = controlClient.Key(keycode)
				} else {
					inputErr = adbKey(adbTarget, input.Keycode)
				}
			}

			latency = time.Since(start)

			if inputErr != nil {
				log.Printf("input dispatch failed (type=%s, latency=%v): %v", input.Type, latency, inputErr)
			} else {
				method := "scrcpy"
				if controlClient == nil {
					method = "adb"
				}
				log.Printf("input dispatched (type=%s, method=%s, latency=%v)", input.Type, method, latency)
			}
		}
	}()

	// Wait for read loop to finish
	<-done

	writeMu.Lock()
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "input ended"))
	writeMu.Unlock()

	log.Printf("input session ended for session %d", sessionID)
}

// Fallback ADB input methods (used when scrcpy is unavailable)
func adbTap(target string, x, y int) error {
	args := []string{"-s", target, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y)}
	_, err := exec.Command("adb", args...).CombinedOutput()
	return err
}

func adbSwipe(target string, x1, y1, x2, y2, durationMs int) error {
	args := []string{"-s", target, "shell", "input", "swipe",
		strconv.Itoa(x1), strconv.Itoa(y1),
		strconv.Itoa(x2), strconv.Itoa(y2),
		strconv.Itoa(durationMs)}
	_, err := exec.Command("adb", args...).CombinedOutput()
	return err
}

func adbKey(target string, keycode string) error {
	args := []string{"-s", target, "shell", "input", "keyevent", keycode}
	_, err := exec.Command("adb", args...).CombinedOutput()
	return err
}

func parseKeycode(keycode string) int {
	// Map common key names to Android keycodes
	keycodes := map[string]int{
		"BACK":        4,
		"HOME":        3,
		"ENTER":       66,
		"DELETE":      67,
		"DPAD_UP":     19,
		"DPAD_DOWN":   20,
		"DPAD_LEFT":   21,
		"DPAD_RIGHT":  22,
		"DPAD_CENTER": 23,
		"VOLUME_UP":   24,
		"VOLUME_DOWN": 25,
		"POWER":       26,
		"TAB":         61,
		"ESCAPE":      111,
		"SPACE":       62,
	}

	if code, ok := keycodes[keycode]; ok {
		return code
	}

	// Try to parse as integer
	var code int
	if _, err := fmt.Sscanf(keycode, "%d", &code); err == nil {
		return code
	}

	return 0
}
