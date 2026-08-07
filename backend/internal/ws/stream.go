package ws

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"image/jpeg"
	"image/png"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/seekersstudio/backend/internal/adb"
)

const (
	deviceWidth  = 1080
	deviceHeight = 2400
	jpegQuality  = 80
	frameFPS     = 10
	frameQueueSz = 3
)

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

type StreamHandler struct {
	adb *adb.Client
}

func NewStreamHandler() *StreamHandler {
	return &StreamHandler{
		adb: adb.New(),
	}
}

func (h *StreamHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.Handle)
	return r
}

func (h *StreamHandler) Handle(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("stream websocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("stream session started")

	frameQueue := make(chan []byte, frameQueueSz)
	var writeMu sync.Mutex
	done := make(chan struct{})

	// Capture loop — producer
	go func() {
		defer close(frameQueue)
		ticker := time.NewTicker(time.Second / frameFPS)
		defer ticker.Stop()

		var prevHash [32]byte
		for range ticker.C {
			pngData, err := h.adb.ScreenshotStream()
			if err != nil {
				log.Printf("screenshot failed: %v", err)
				continue
			}

			jpegData, err := encodeJPEG(pngData)
			if err != nil {
				log.Printf("jpeg encode failed: %v", err)
				continue
			}

			hash := sha256.Sum256(jpegData)
			if hash == prevHash {
				continue
			}
			prevHash = hash

			// Backpressure: drop oldest frame if queue full
			select {
			case frameQueue <- jpegData:
			default:
				select {
				case <-frameQueue:
				default:
				}
				frameQueue <- jpegData
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

	// WebSocket read loop — touch input
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("stream read ended: %v", err)
				return
			}

			var input InputMessage
			if err := json.Unmarshal(msg, &input); err != nil {
				continue
			}

			switch input.Type {
			case "tap":
				if err := h.adb.Tap(input.X, input.Y); err != nil {
					log.Printf("tap failed: %v", err)
				}
			case "swipe":
				duration := input.Duration
				if duration <= 0 {
					duration = 300
				}
				if err := h.adb.Swipe(input.X1, input.Y1, input.X2, input.Y2, duration); err != nil {
					log.Printf("swipe failed: %v", err)
				}
			case "key":
				if err := h.adb.Key(input.Keycode); err != nil {
					log.Printf("key failed: %v", err)
				}
			}
		}
	}()

	// Wait for write loop to finish (client disconnected or error)
	<-done

	writeMu.Lock()
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stream ended"))
	writeMu.Unlock()

	log.Printf("stream session ended")
}

func encodeJPEG(pngData []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
