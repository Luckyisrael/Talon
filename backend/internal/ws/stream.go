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
	"github.com/seekersstudio/backend/internal/scrcpy"
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

// StreamMode is the negotiation frame's vocabulary. The server picks once
// per connection: h264 when the device's scrcpy video tap came up, jpeg
// otherwise (no WebCodecs, scrcpy video failed, or a pre-negotiation
// client). The client decodes whatever mode it was told.
const (
	// ModeH264/ModeJPEG: the negotiation vocabulary, exported for the
	// phase guard — the wire contract between the two transports.
	ModeH264 = "h264"
	ModeJPEG = "jpeg"
	modeH264 = ModeH264
	modeJPEG = ModeJPEG
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

// modeFrame tells the client what the binary frames carry.
type modeFrame struct {
	Mode string `json:"mode"`
	W    int    `json:"w,omitempty"`
	H    int    `json:"h,omitempty"`
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

	frameQueue := make(chan []byte, frameQueueSz)
	var writeMu sync.Mutex
	done := make(chan struct{})

	// ── Try the device's own H.264 encoder first (DV3) ──
	// The scrcpy video tap: the device encodes, the host relays. Failure is
	// non-fatal by design — the JPEG path below is the standing fallback and
	// the negotiation frame tells the client which one it is on.
	vc := scrcpy.NewVideoClient()
	if err := vc.Start(adbTarget); err == nil {
		if err := conn.WriteJSON(modeFrame{Mode: modeH264, W: vc.Width, H: vc.Height}); err == nil {
			log.Printf("session %d: h264 video (device encoder, %dx%d)", sessionID, vc.Width, vc.Height)
			h.pumpH264(sessionID, conn, vc, frameQueue, &writeMu, done)
			return
		}
		// The negotiation frame failed: the client never learned the mode,
		// and a mode-less binary stream is undecodable. Fall through.
	} else {
		log.Printf("session %d: scrcpy video unavailable (%v) — JPEG fallback", sessionID, err)
	}
	vc.Close()

	// ── The JPEG path (the original stream) ──
	if err := conn.WriteJSON(modeFrame{Mode: modeJPEG}); err != nil {
		log.Printf("session %d: negotiation frame failed: %v", sessionID, err)
		return
	}

	encoder := video.NewEncoder()
	defer encoder.Close()
	differ := video.NewFrameDiffer()

	// Capture loop — producer
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

		var consecutiveFailures int
		var lastAttempt time.Time
		var slowed bool

		for range ticker.C {
			if slowed && time.Since(lastAttempt) < slowRetryInterval {
				continue
			}

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

			if slowed {
				slowed = false
				log.Printf("session %d: device is back — resuming full-rate capture", sessionID)
			}
			consecutiveFailures = 0

			if differ.IsSame(rawRGBA) {
				framesSkipped++
				continue
			}

			jpegData, err := encoder.EncodeRaw(rawRGBA, width, height, jpegQuality)
			if err != nil {
				log.Printf("jpeg encode failed: %v", err)
				continue
			}

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

	<-done

	writeMu.Lock()
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "stream ended"))
	writeMu.Unlock()

	log.Printf("stream session ended for session %d", sessionID)
}

// pumpH264 relays the device's NAL stream to the client. Bounded like the
// JPEG queue: a slow client drops oldest frames — H.264's P-frames make a
// dropped-frame policy safe (the next keyframe realigns the decoder) but a
// stalled socket must never stall the device's encoder.
func (h *StreamHandler) pumpH264(sessionID int64, conn *websocket.Conn, vc *scrcpy.VideoClient, frameQueue chan []byte, writeMu *sync.Mutex, done chan struct{}) {
	defer vc.Close()

	go func() {
		defer close(frameQueue)
		buf := make([]byte, 32*1024)
		for {
			n, err := vc.ReadChunk(buf)
			if err != nil {
				log.Printf("session %d: h264 tap ended (%v)", sessionID, err)
				return
			}
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case frameQueue <- chunk:
			default:
				// Drop oldest: backpressure at the socket, not the encoder.
				select {
				case <-frameQueue:
				default:
				}
				frameQueue <- chunk
			}
		}
	}()

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

	<-done
	log.Printf("stream session ended for session %d (h264)", sessionID)
}
