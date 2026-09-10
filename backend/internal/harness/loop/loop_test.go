package loop

import (
	"errors"
	"testing"
	"time"
)

// The decision constructors produce exactly what the loop acts on: action,
// model, latch, backoff, reason. A listener the loop cannot interpret is a
// silent wrong behavior, so the shapes are pinned here.
func TestDecisions(t *testing.T) {
	term := Terminal("unrecoverable")
	if term.Action != ErrorTerminal || term.Reason != "unrecoverable" {
		t.Errorf("Terminal malformed: %+v", term)
	}

	fallback := RetryOnModel("cheap-model", true, "primary failed")
	if fallback.Action != ErrorRetryOnModel || fallback.RetryModel != "cheap-model" || !fallback.Latch {
		t.Errorf("RetryOnModel malformed: %+v", fallback)
	}

	backoff := RetryAfter(5*time.Second, "throttled")
	if backoff.Action != ErrorRetryWithBackoff || backoff.Backoff != 5*time.Second {
		t.Errorf("RetryAfter malformed: %+v", backoff)
	}
}

// The listener contract: fellBack lets the composition refuse a second
// fallback retry on the same turn — the loop passes it, the listener decides.
func TestListenerReceivesContext(t *testing.T) {
	var sawModel string
	var sawFellBack bool
	var listener RequestErrorListener = func(round int, model string, err error, fellBack bool) RequestErrorDecision {
		sawModel = model
		sawFellBack = fellBack
		return Terminal("already fell back once")
	}
	got := listener(3, "primary-model", errors.New("boom"), true)
	if got.Action != ErrorTerminal {
		t.Errorf("listener answer changed: %+v", got)
	}
	if sawModel != "primary-model" || !sawFellBack {
		t.Errorf("listener context wrong: model=%q fellBack=%v", sawModel, sawFellBack)
	}
}
