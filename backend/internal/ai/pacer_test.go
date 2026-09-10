package ai

import (
	"context"
	"os"
	"testing"
	"time"
)

// The pacer is the defense against rate-limited gateways (8 req/min tiers):
// every test pins a law the live E2E depends on.
func TestPacerNilWhenUnset(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "")
	if p := newRequestPacer(); p != nil {
		t.Fatal("no env set — pacer must be nil (zero behavior change)")
	}
}

func TestPacerNilOnGarbage(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "not-a-duration")
	if p := newRequestPacer(); p != nil {
		t.Fatal("garbage interval — pacer must be nil, never a panic or 0s loop")
	}
}

func TestPacerNilOnZeroOrNegative(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "0s")
	if p := newRequestPacer(); p != nil {
		t.Fatal("zero interval is not pacing — must be nil")
	}
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "-5s")
	if p := newRequestPacer(); p != nil {
		t.Fatal("negative interval would sleep on the second call — must be nil")
	}
}

func TestPacerWaitsBetweenCalls(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "60ms")
	p := newRequestPacer()
	if p == nil {
		t.Fatal("valid interval must build a pacer")
	}

	ctx := context.Background()
	start := time.Now()
	// First call: no wait (no prior call).
	if err := p.wait(ctx); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	// Second call: must block ~interval.
	if err := p.wait(ctx); err != nil {
		t.Fatalf("second wait: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Fatalf("second call must respect the interval: elapsed %v < 50ms", elapsed)
	}
}

func TestPacerSerializesConcurrentWaits(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "40ms")
	p := newRequestPacer()
	if p == nil {
		t.Fatal("pacer must exist")
	}

	ctx := context.Background()
	// Three concurrent waits must be spaced by the interval: the last
	// finisher takes >= 2 intervals of wall time.
	start := time.Now()
	done := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			done <- p.wait(ctx)
		}()
	}
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
	if el := time.Since(start); el < 70*time.Millisecond {
		t.Fatalf("3 concurrent waits must serialize to >= 2 intervals: %v", el)
	}
}

func TestPacerContextCancelPropagates(t *testing.T) {
	t.Setenv("AI_MIN_REQUEST_INTERVAL", "10s")
	p := newRequestPacer()
	if p == nil {
		t.Fatal("pacer must exist")
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := p.wait(ctx); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	if err := p.wait(ctx); err == nil {
		t.Fatal("canceled context must surface an error, not sleep 10s")
	} else {
		t.Logf("canceled after %v: %v", time.Since(start), err)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("cancel must propagate promptly: %v", el)
	}
}

// The env knob must be off by default — evals and unit tests that mock the
// gateway must never pay the pacing sleep.
func TestPacerDefaultOff(t *testing.T) {
	os.Unsetenv("AI_MIN_REQUEST_INTERVAL")
	if p := newRequestPacer(); p != nil {
		t.Fatal("unset env must mean nil pacer")
	}
}
