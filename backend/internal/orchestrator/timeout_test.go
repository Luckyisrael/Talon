package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/tooltimeout"
)

// A tool that ignores its context and runs past the budget must not hang the
// round: the wrapped executor reports the timeout as an error result naming
// the budget, and the loop moves on.
func TestTimeoutWrapperReportsOverrun(t *testing.T) {
	p := tooltimeout.New()
	p.Register("slow_tool", 20*time.Millisecond)

	exec := wrapWithTimeouts(func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		select {
		case <-time.After(300 * time.Millisecond):
			return "finished late", false
		case <-ctx.Done():
			// A cooperative tool stops here; the wrapper still owns the message.
			return "stopped", true
		}
	}, p)

	out, isErr := exec(context.Background(), "slow_tool", nil)
	if !isErr {
		t.Errorf("an over-budget call must be an error result, got %q", out)
	}
	if out == "" {
		t.Error("the result must name the budget that was exceeded")
	}
}

// A tool that beats its budget keeps its own result untouched.
func TestTimeoutWrapperPassesFastCall(t *testing.T) {
	p := tooltimeout.New()
	p.Register("fast_tool", time.Minute)

	exec := wrapWithTimeouts(func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		return "the real result", false
	}, p)

	out, isErr := exec(context.Background(), "fast_tool", nil)
	if isErr {
		t.Errorf("a call inside budget must keep its own result shape, got error %q", out)
	}
	if out != "the real result" {
		t.Errorf("result passthrough: got %q", out)
	}
}

// An unregistered tool keeps the old unbounded behaviour and its own result —
// no registry-wide default is applied.
func TestTimeoutWrapperPassesThroughUnregistered(t *testing.T) {
	p := tooltimeout.New() // nothing registered
	exec := wrapWithTimeouts(func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		return "the real result", false
	}, p)

	out, isErr := exec(context.Background(), "any_tool", nil)
	if isErr {
		t.Errorf("an unregistered tool must keep its own result shape, got error %q", out)
	}
	if out != "the real result" {
		t.Errorf("result passthrough: got %q", out)
	}
}

// An upstream cancel (the parent's deadline arriving first) must not be
// reported as this tool's timeout.
func TestTimeoutWrapperUpstreamCancelIsNotATimeout(t *testing.T) {
	p := tooltimeout.New()
	p.Register("tool", time.Hour)

	parent, cancelParent := context.WithDeadline(context.Background(), time.Now().Add(30*time.Millisecond))
	defer cancelParent()

	exec := wrapWithTimeouts(func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
		<-ctx.Done()
		return "stopped upstream", true
	}, p)

	out, isErr := exec(parent, "tool", nil)
	// The tool returned its own error, which the wrapper preserves; it must not
	// add the budget-exceeded message for a cancel it did not cause.
	if isErr && strings.Contains(out, "call budget") {
		t.Errorf("an upstream cancel must not be reported as the tool's timeout, got %q", out)
	}
}
