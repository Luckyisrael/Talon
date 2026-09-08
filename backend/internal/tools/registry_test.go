package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// slowTool never returns on its own; only the registry's context deadline
// ends it. Used to prove the per-tool budget actually applies.
type slowTool struct {
	name string
}

func (t *slowTool) Name() string { return t.name }
func (t *slowTool) Description() string {
	return "A tool that blocks until its context is cancelled — for timeout tests."
}
func (t *slowTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (t *slowTool) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	<-ctx.Done()
	return ToolResult{Content: "should not reach the model"}, nil
}

// build_apk carries the 30-minute budget; everything else 10. A test that
// waited minutes would defeat itself, so this asserts the *differential*:
// with the per-tool budget the slow build tool must outlive the generic one.
// Both run under a 150ms parent context: the generic tool dies on the
// registry's scaled-down budget, the build tool on its own — proving the
// two budgets are wired independently, not one constant.
func TestToolTimeoutBudgets(t *testing.T) {
	// Temporarily scale the budgets so the test runs in milliseconds.
	origTool, origBuild := toolTimeout, buildToolTimeout
	toolTimeout, buildToolTimeout = 40*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { toolTimeout, buildToolTimeout = origTool, origBuild })

	reg := NewRegistry()
	reg.Register(&slowTool{name: "plain_slow"})
	reg.Register(&slowTool{name: "build_apk"})

	start := time.Now()
	if _, err := reg.Execute(context.Background(), "plain_slow", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("registry never propagates errors: %v", err)
	}
	genericWall := time.Since(start)

	start = time.Now()
	if _, err := reg.Execute(context.Background(), "build_apk", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("registry never propagates errors: %v", err)
	}
	buildWall := time.Since(start)

	if buildWall < genericWall*2 {
		t.Errorf("build_apk should carry a larger budget: generic=%v build=%v", genericWall, buildWall)
	}
	if genericWall < 30*time.Millisecond {
		t.Errorf("generic tool should have hit its scaled budget, wall=%v", genericWall)
	}
}
