package ws

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
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

		for range ticker.C {
			// Raw screencap: no PNG flag = raw RGBA pixels
			rawRGBA, width, height, err := adbClient.ScreenshotRaw()
			if err != nil {
				log.Printf("raw screencap failed: %v", err)
				continue
			}

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
