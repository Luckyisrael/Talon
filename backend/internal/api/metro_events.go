package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/seekersstudio/backend/internal/metro"
)

// MetroHandler streams a project's live metro bundler events over SSE —
// DV1: bundle progress, hot reloads, and redbox errors for the preview
// badge. One connection per device session (the ws stream is video; this
// is the bundler's voice).
type MetroHandler struct {
	*PromptHandler
}

func NewMetroHandler(base *PromptHandler) *MetroHandler {
	return &MetroHandler{PromptHandler: base}
}

// Serve streams /v1/projects/{projectID}/metro.
func (h *MetroHandler) Serve(w http.ResponseWriter, r *http.Request) {
	project, ok := h.ownedProject(w, r, projectIDFromRoute(r))
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeError(w, http.StatusInternalServerError, "streaming is not available on this connection")
		return
	}

	// No live device session for this project: the honest answer is one
	// event saying so, then close. The frontend shows "no device running"
	// instead of an infinitely-pending badge.
	session, err := h.store.LatestLiveSession(project.ID)
	if err != nil || session == nil {
		writeSSE(w, "metro", map[string]any{"kind": "unavailable", "text": "no device session running"})
		flusher.Flush()
		return
	}

	events, unsubscribe := metro.SubscribeSession(session.ID)
	if events == nil {
		// A session exists but its bundler never started (metro start
		// failed at session create). Same honest shape.
		writeSSE(w, "metro", map[string]any{"kind": "unavailable", "text": "the bundler is not running for this session"})
		flusher.Flush()
		return
	}
	defer unsubscribe()

	// The ready frame: the badge starts in a known state even if metro has
	// been quiet since before the browser connected.
	writeSSE(w, "metro", map[string]any{"kind": "connected", "text": "bundler connected"})
	flusher.Flush()

	// r.Context() dies when the browser closes the SSE — the unsubscribe
	// then releases the emitter slot; nothing leaks per reconnect.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			writeSSE(w, "metro", map[string]any{
				"kind":     ev.Kind,
				"text":     ev.Text,
				"progress": ev.Progress,
			})
			flusher.Flush()
		case <-heartbeat.C:
			// Comment-only keepalive: proxies that idle-timeout quiet SSE
			// connections get bytes without the client seeing an event.
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
