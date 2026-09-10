package metro

import (
	"strings"
	"testing"
)

// The classifier vocabulary, from recorded metro output. Pure — no bundler
// runs in this test.
func TestClassify(t *testing.T) {
	cases := []struct {
		line string
		kind string
		text string
		prog int
	}{
		{"Starting Metro Bundler", "bundle_start", "bundling", 0},
		{"Bundling index.js  45%", "bundle_progress", "", 45},
		{"Bundling index.js 100% (1234ms)", "bundle_progress", "", 100},
		{"Bundling index.js", "bundle_progress", "", 0}, // no % → 0, not fabricated
		{"Bundled 4318ms", "bundle_done", "bundle ready", 0},
		{"Reloading app...", "reloaded", "app reloaded", 0},
		{"SyntaxError: /app/screen.tsx: Unexpected token (10:5)", "error", "", 0},
		{"Unable to resolve module ./utils from /app/screen.tsx", "error", "", 0},
		{"ERROR  Metro Bundler has encountered an error", "error", "", 0},
		{"error: cannot find module 'x'", "error", "", 0},
		{"nothing actionable here", "log", "", 0},
	}
	for _, c := range cases {
		ev := classify(c.line)
		if ev.Kind != c.kind {
			t.Errorf("classify(%q).Kind = %q, want %q", c.line, ev.Kind, c.kind)
			continue
		}
		if c.text != "" && ev.Text != c.text {
			t.Errorf("classify(%q).Text = %q, want %q", c.line, ev.Text, c.text)
		}
		if c.prog > 0 && ev.Progress != c.prog {
			t.Errorf("classify(%q).Progress = %d, want %d", c.line, ev.Progress, c.prog)
		}
	}
	if ev := classify("Bundling index.js"); ev.Progress != 0 {
		t.Errorf("percent-less line fabricated %d", ev.Progress)
	}
}

// extractPercent: bounded, honest, no partial digits.
func TestExtractPercent(t *testing.T) {
	if p := extractPercent("Bundling index.js  7%"); p != 7 {
		t.Errorf("7 got %d", p)
	}
	if p := extractPercent("no percent"); p != 0 {
		t.Errorf("none got %d", p)
	}
	if p := extractPercent("% leading"); p != 0 {
		t.Errorf("lone %% got %d", p)
	}
}

// The emitter: bounded fan-out, drop-oldest, no stall on a wedged consumer.
func TestEmitterBounded(t *testing.T) {
	e := &emitter{}
	ch := e.subscribe()
	defer e.unsubscribe(ch)

	// Fill past the buffer: the pump must not block.
	for i := 0; i < subscriberBuffer+20; i++ {
		e.emit(Event{Kind: "log", Text: strings.Repeat("x", 100)})
	}
	// The consumer kept the newest events, dropped the oldest.
	got := 0
	for len(ch) > 0 {
		<-ch
		got++
	}
	if got == 0 {
		t.Fatal("drop-oldest dropped everything; the newest event must land")
	}
	// Second subscriber joins late and only sees the future.
	ch2 := e.subscribe()
	defer e.unsubscribe(ch2)
	e.emit(Event{Kind: "reloaded"})
	select {
	case ev := <-ch2:
		if ev.Kind != "reloaded" {
			t.Errorf("late subscriber got %q", ev.Kind)
		}
	default:
		t.Error("late subscriber missed the event")
	}
}
