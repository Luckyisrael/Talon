package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	device, ok := authorize(w, r, h.authSvc, h.store)
	if !ok {
		return
	}
	sessionID, adbTarget := device.ID, device.ADBTarget

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
					if keycode == 0 {
						// Sending KEYCODE_UNKNOWN down the control socket looks
						// like a delivered keypress and does nothing. Say so.
						inputErr = fmt.Errorf("unknown keycode %q", input.Keycode)
					} else {
						inputErr = controlClient.Key(keycode)
					}
				} else {
					inputErr = adbKey(adbTarget, input.Keycode)
				}
			case "rotate":
				// Rotation is device settings, not a keyevent, so it bypasses
				// the control socket and always goes over adb shell.
				inputErr = adbRotate(adbTarget)
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

// adbRotate toggles the device between portrait and landscape. Redroid
// ignores user_rotation at runtime (fixed framebuffer), so rotation goes
// through `wm size` override — the only rotation this device honors. The
// stream already re-sizes frames from what arrives, so the preview follows.
func adbRotate(target string) error {
	out, err := exec.Command("adb", "-s", target, "shell", "wm", "size").CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not read display size: %w", err)
	}
	size := decideRotation(string(out))
	args := []string{"-s", target, "shell", "wm", "size", size}
	if out, err := exec.Command("adb", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("could not rotate: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var wmSizeRe = regexp.MustCompile(`(\d+)x(\d+)`)

// decideRotation picks the toggled size from `wm size` output, which reports
// the physical size and, when overridden, the override size. The effective
// (override if present, else physical) landscape flips to portrait 1080x2400
// and anything else flips to landscape 2400x1080.
func decideRotation(wmSizeOutput string) string {
	matches := wmSizeRe.FindAllStringSubmatch(wmSizeOutput, -1)
	if len(matches) == 0 {
		return "1080x2400"
	}
	last := matches[len(matches)-1]
	var w, h int
	fmt.Sscanf(last[1]+"x"+last[2], "%dx%d", &w, &h)
	if w > h {
		return "1080x2400"
	}
	return "2400x1080"
}

func parseKeycode(keycode string) int {
	// Map common key names to Android keycodes
	keycodes := map[string]int{
		"BACK":        4,
		"HOME":        3,
		"APP_SWITCH":  187,
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
