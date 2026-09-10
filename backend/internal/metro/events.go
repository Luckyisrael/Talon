package metro

import (
	"strings"
	"sync"
)

// Event is one meaningful thing metro said, classified from its stdout.
// Metro's raw output is unstructured; this classifier turns it into the
// small typed vocabulary the preview badge and the agent's metro_status
// tool consume. Lines that say nothing actionable are Kind log and never
// leave the server logs.
type Event struct {
	// Kind: bundle_start | bundle_progress | bundle_done | bundle_failed |
	// reloaded | error | log
	Kind string
	// Text is the human line — for errors, the message the redbox shows.
	Text string
	// Progress is the bundling percentage when the line carried one.
	Progress int
}

// Subscribers receive classified events. The channel is dropped-oldest
// bounded: a slow consumer (nobody reading) must not stall metro's
// stdout pump — the pump is the process's lifeline.
const subscriberBuffer = 64

// Exported wrappers for evals and cross-package tests: the vocabulary and
// the emitter behavior are contracts, tested from the phase guards.
func ClassifyLine(line string) Event { return classify(line) }
func NewEmitter() *emitter         { return &emitter{} }
func (e *emitter) Subscribe() chan Event { return e.subscribe() }
func (e *emitter) Emit(ev Event)    { e.emit(ev) }

// SubscriberBuffer is the documented bound — evals assert against it.
const SubscriberBuffer = subscriberBuffer

// emitter fans one metro stdout line out to every subscriber. It lives on
// the Server so the pump goroutine never touches the registry mutex.
type emitter struct {
	mu   sync.Mutex
	subs []chan Event
}

func (e *emitter) subscribe() chan Event {
	ch := make(chan Event, subscriberBuffer)
	e.mu.Lock()
	e.subs = append(e.subs, ch)
	e.mu.Unlock()
	return ch
}

func (e *emitter) unsubscribe(ch chan Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, s := range e.subs {
		if s == ch {
			e.subs = append(e.subs[:i], e.subs[i+1:]...)
			break
		}
	}
}

func (e *emitter) emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ch := range e.subs {
		select {
		case ch <- ev:
		default:
			// Drop the oldest: the newest event is the most relevant, and
			// a wedged consumer degrades to "recent" instead of stalling
			// the pump.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// classify turns one metro output line into an Event. Pure function — the
// unit tests cover the whole vocabulary from recorded metro output without
// a bundler running.
//
// The patterns are metro's own console strings (Metro v0.80-era, the
// template's bundled CLI). When expo updates metro and the wording shifts,
// lines fall through to Kind log — the badge shows the last known state
// instead of lying, and the missing-classifier is a test away from repair.
func classify(line string) Event {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.Contains(trimmed, "Starting Metro Bundler"),
		strings.Contains(trimmed, "Bundler is bundling"):
		return Event{Kind: "bundle_start", Text: "bundling"}

	case strings.HasPrefix(trimmed, "Bundling"), // "Bundling index.js  45%"
		strings.Contains(trimmed, "bundle progress"):
		return Event{Kind: "bundle_progress", Text: trimmed, Progress: extractPercent(trimmed)}

	case strings.Contains(trimmed, "Bundled"),
		strings.Contains(trimmed, "bundle complete"),
		strings.Contains(trimmed, " Bundled "):
		return Event{Kind: "bundle_done", Text: "bundle ready"}

	// Metro's reload notices: "Reloading app..." on --[r]eload, and the
	// file-watcher change lines that precede a rebuild. What the human
	// needs is "the preview is about to flash with your change."
	case strings.Contains(trimmed, "Reloading"),
		strings.Contains(trimmed, "app has been reloaded"),
		strings.Contains(trimmed, "Reloaded"):
		return Event{Kind: "reloaded", Text: "app reloaded"}

	// Redbox is the runtime error surface. Metro prints the error itself;
	// the badge renders the banner and the agent's metro_status tool gets
	// the same text. Symbolication noise ("at ", "in ", stack frames)
	// collapses into the one error event that started the block.
	case strings.Contains(trimmed, "SyntaxError"),
		strings.Contains(trimmed, "TypeError"),
		strings.Contains(trimmed, "ReferenceError"),
		strings.Contains(trimmed, "Unable to resolve module"),
		strings.Contains(trimmed, "Metro Bundler has encountered an error"),
		strings.Contains(trimmed, "ERROR"), // metro logs errors with this prefix
		strings.Contains(trimmed, "error:") && len(trimmed) > len("error:"):
		return Event{Kind: "error", Text: trimmed}

	default:
		return Event{Kind: "log", Text: trimmed}
	}
}

// extractPercent pulls the trailing percentage out of a progress line.
// "Bundling index.js 45%" → 45. Absence reports 0 — the badge shows the
// spinner without a number rather than a fabricated one.
func extractPercent(s string) int {
	i := strings.LastIndex(s, "%")
	if i < 0 {
		return 0
	}
	start := i
	for start > 0 && s[start-1] >= '0' && s[start-1] <= '9' {
		start--
	}
	if start == i {
		return 0
	}
	n := 0
	for _, c := range s[start:i] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
