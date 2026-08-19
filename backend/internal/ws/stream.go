package ws

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/seekersstudio/backend/internal/adb"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/video"
)

const (
	deviceWidth  = 1080
	deviceHeight = 2400
	jpegQuality  = 80
	frameFPS     = 30
	frameQueueSz = 3

	// Screencap failure handling. When the device dies mid-session every attempt
	// fails; hammering ADB at frame rate turned a dead device into a 30-line-per-
	// second log flood that only stopped when the tab closed. After this many
	// consecutive failures the loop backs off to slowRetryInterval instead.
	failureThreshold  = 10
	slowRetryInterval = 1 * time.Second
)

type StreamHandler struct {
	store   *store.Store
	authSvc *auth.Service
}

func NewStreamHandler(authSvc *auth.Service, s *store.Store) *StreamHandler {
	return &StreamHandler{
		store:   s,
		authSvc: authSvc,
	}
}

func (h *StreamHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{id}", h.Handle)
	return r
}

func (h *StreamHandler) Handle(w http.ResponseWriter, r *http.Request) {
	device, ok := authorize(w, r, h.authSvc, h.store)
	if !ok {
		return
	}
	sessionID, adbTarget := device.ID, device.ADBTarget
	adbClient := adb.New(adbTarget)

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("stream websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("stream session started for session %d (target %s)", sessionID, adbTarget)

	// Initialize video pipeline
	encoder := video.NewEncoder()
	defer encoder.Close()
	differ := video.NewFrameDiffer()

	frameQueue := make(chan []byte, frameQueueSz)
	var writeMu sync.Mutex
	done := make(chan struct{})

	// Capture loop — producer
	// Uses raw screencap + fast JPEG encoding + frame diffing.
	// This is significantly faster than the old PNG-based pipeline because:
	// 1. No on-device PNG compression (raw RGBA from framebuffer)
	// 2. No PNG decode on host
	// 3. Fast frame diffing skips identical frames (static screen = minimal traffic)
	// 4. JPEG encoding from raw data is faster than PNG→JPEG conversion
	go func() {
		defer close(frameQueue)
		ticker := time.NewTicker(time.Second / frameFPS)
		defer ticker.Stop()

		// FPS measurement
		var framesSent, framesSkipped int
		fpsTicker := time.NewTicker(1 * time.Second)
		defer fpsTicker.Stop()

		go func() {
			for range fpsTicker.C {
				if framesSent > 0 || framesSkipped > 0 {
					log.Printf("session %d: FPS=%.1f (sent=%d, skipped=%d)", sessionID, float64(framesSent), framesSent, framesSkipped)
				}
				framesSent = 0
				framesSkipped = 0
			}
		}()

		// consecutiveFailures tracks screencap errors so the loop can back off
		// when the device goes away instead of logging at frame rate forever.
		var consecutiveFailures int
		var lastAttempt time.Time
		var slowed bool

		for range ticker.C {
			// Once throttled, only try every slowRetryInterval — the ticker still
			// fires at frame rate, so gate on elapsed time rather than the tick.
			if slowed && time.Since(lastAttempt) < slowRetryInterval {
				continue
			}

			// Raw screencap: no PNG flag = raw RGBA pixels
			rawRGBA, width, height, err := adbClient.ScreenshotRaw()
			lastAttempt = time.Now()
			if err != nil {
				consecutiveFailures++
				if !slowed && consecutiveFailures >= failureThreshold {
					slowed = true
					log.Printf("session %d: device unreachable (%d consecutive failures: %v) — retrying every %s until it returns", sessionID, consecutiveFailures, err, slowRetryInterval)
				}
				continue
			}

			// The device answered again; resume full-rate capture.
			if slowed {
				slowed = false
				log.Printf("session %d: device is back — resuming full-rate capture", sessionID)
			}
			consecutiveFailures = 0

			// Frame diffing: skip identical frames
			if differ.IsSame(rawRGBA) {
				framesSkipped++
				continue
			}

			// Fast JPEG encode from raw RGBA
			jpegData, err := encoder.EncodeRaw(rawRGBA, width, height, jpegQuality)
			if err != nil {
				log.Printf("jpeg encode failed: %v", err)
				continue
			}

			// Backpressure: drop oldest frame if queue full
			select {
			case frameQueue <- jpegData:
				framesSent++
			default:
				select {
				case <-frameQueue:
				default:
				}
				frameQueue <- jpegData
				framesSent++
			}
		}
	}()

	// WebSocket write loop — consumer
	go func() {
		defer func() { done <- struct{}{} }()
		for frame := range frameQueue {
			writeMu.Lock()
			err := conn.WriteMessage(websocket.BinaryMessage, frame)
			writeMu.Unlock()
			if err != nil {
				log.Printf("stream write failed: %v", err)
				return
			}
		}
	}()

	// Note: Touch input is now handled by a separate WebSocket at /v1/sessions/{id}/input
	// This decoupling ensures input continues working even if video capture stalls.

	// Wait for write loop to finish (client disconnected or error)
	<-done

	writeMu.Lock()
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stream ended"))
	writeMu.Unlock()

	log.Printf("stream session ended for session %d", sessionID)
}
