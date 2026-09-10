package tools

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// MetroStatus reports the bundler's state for the agent (DV1): what metro
// said most recently — bundling progress, the last reload, or the redbox
// error blocking the preview. The agent uses it to close the loop between
// "I changed code" and "what the device actually shows" without needing a
// screenshot round-trip.
//
// The StatusSource is the session's metro event tap. Absence (no device
// session, bundler never started) is reported honestly — the tool never
// fabricates a healthy bundler.
type MetroStatus struct {
	Source StatusSource
}

// StatusSource supplies the bundler's recent classified events. Implemented
// by the api layer over the metro package's per-session tap; an interface so
// the tool tests without a real bundler.
type StatusSource interface {
	// Recent returns up to n most recent metro events, oldest first.
	Recent(n int) []MetroEvent
	// Live reports whether a bundler is running for this session.
	Live() bool
}

// MetroEvent is the tool-facing mirror of metro.Event — the tool layer must
// not import the metro package (one-way dependency: api → tools, api →
// metro; tools → metro would tangle the tool registry into the bundler).
type MetroEvent struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Progress int    `json:"progress"`
	At       string `json:"at"`
}

func (t *MetroStatus) Name() string { return "metro_status" }

func (t *MetroStatus) Description() string {
	return "Report the Metro bundler's current state: bundling progress, the last hot reload, or the redbox/runtime error the preview is stuck on. " +
		"Use it after code changes to confirm the bundle rebuilt cleanly, or when the preview shows an error screen — the text is the error the device displays."
}

func (t *MetroStatus) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *MetroStatus) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	if t.Source == nil {
		return ToolResult{Content: "no bundler attached to this session"}, nil
	}
	if !t.Source.Live() {
		return ToolResult{Content: "the bundler is not running for this session — no device session is live"}, nil
	}
	events := t.Source.Recent(20)
	if len(events) == 0 {
		return ToolResult{Content: "the bundler is running but quiet — no bundle, reload, or error recorded yet"}, nil
	}
	out, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return ToolResult{Content: "could not render the bundler state"}, nil
	}
	return ToolResult{Content: string(out)}, nil
}

// Buffer is a bounded in-memory StatusSource: the api layer pumps the
// session's metro events into it at session start, the tool reads the tail.
// Bounded by construction — the ring keeps the newest n, drops the oldest.
type Buffer struct {
	mu    sync.Mutex
	ring  []MetroEvent
	limit int
	live  bool
}

func NewMetroBuffer(limit int) *Buffer {
	if limit < 1 {
		limit = 32
	}
	return &Buffer{limit: limit}
}

func (b *Buffer) Push(kind, text string, progress int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring = append(b.ring, MetroEvent{Kind: kind, Text: text, Progress: progress, At: time.Now().UTC().Format(time.RFC3339)})
	if len(b.ring) > b.limit {
		b.ring = b.ring[len(b.ring)-b.limit:]
	}
}

func (b *Buffer) SetLive(v bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live = v
}

func (b *Buffer) Recent(n int) []MetroEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.ring) {
		n = len(b.ring)
	}
	out := make([]MetroEvent, n)
	copy(out, b.ring[len(b.ring)-n:])
	return out
}

func (b *Buffer) Live() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live
}
