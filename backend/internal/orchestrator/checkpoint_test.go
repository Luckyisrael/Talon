package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/events"
	harnessevents "github.com/luckyisrael/talon/backend/internal/harness/events"
)

// failAuditLog is an AuditLog whose Sync always fails — the crash-fidelity
// condition the checkpoint policy must refuse to cross.
type failAuditLog struct {
	appends []harnessevents.Kind
	syncs   int
}

func (f *failAuditLog) Append(kind harnessevents.Kind, payload any) (int64, error) {
	f.appends = append(f.appends, kind)
	return int64(len(f.appends)), nil
}
func (f *failAuditLog) Sync() error {
	f.syncs++
	return errors.New("the disk is gone")
}

// The barrier is fail-closed: a tool whose record cannot be made durable must
// not run. The round comes back as a failed tool_result naming the refusal, and
// the executor is never called.
func TestCheckpointRefusesWhenUndurable(t *testing.T) {
	executed := []string{}
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	calls := []ai.ToolUseBlock{{ID: "t1", Name: "write_file", Input: json.RawMessage(`{}`)}}

	results := o.executeTools(context.Background(), sess, calls, RunOptions{
		Execute: func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			executed = append(executed, name)
			return "ran", false
		},
		EventLog: &failAuditLog{},
	})

	if len(executed) != 0 {
		t.Fatalf("a tool must not run when the barrier fails; %v did", executed)
	}
	if len(results) != 1 {
		t.Fatalf("want one refused tool_result, got %d", len(results))
	}
	if results[0].ToolUseID != "t1" {
		t.Errorf("the refusal must answer the tool_use id, got %q", results[0].ToolUseID)
	}
	if !results[0].IsError {
		t.Error("the refusal must be an error result")
	}
	if results[0].Content == "" {
		t.Error("the refusal must tell the model why it did not run")
	}
}

// With no audit log the loop must behave exactly as before — the barrier is an
// opt-in durability contract, not a new hard dependency for tests and evals.
func TestCheckpointAbsentRunsTools(t *testing.T) {
	executed := []string{}
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	calls := []ai.ToolUseBlock{{ID: "t1", Name: "read_file", Input: json.RawMessage(`{}`)}}

	results := o.executeTools(context.Background(), sess, calls, RunOptions{
		Execute: func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			executed = append(executed, name)
			return "ok", false
		},
	})

	if len(executed) != 1 {
		t.Fatalf("the tool must run with no audit log, got %d", len(executed))
	}
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("the call must come back as a normal result, got %+v", results)
	}
}

// The audit log's Sync must be reachable through the seam the loop holds — a
// log that cannot be Synced cannot implement the policy at all.
func TestAuditLogSeamRequiresSync(t *testing.T) {
	var _ AuditLog = (*failAuditLog)(nil)
	var _ AuditLog = (*memEventLog)(nil)
}

// evn is a live emitter: the loop emits from the first tool call, so a nil
// emitter panics.
func evn() *events.SessionEmitter {
	return events.NewSessionEmitter(1, 128)
}
