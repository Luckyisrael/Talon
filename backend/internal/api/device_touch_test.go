package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

type stubDeviceTool struct{ name string }

func (s stubDeviceTool) Name() string { return s.name }
func (s stubDeviceTool) Description() string {
	return "test stub"
}
func (s stubDeviceTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s stubDeviceTool) Execute(context.Context, json.RawMessage) (tools.ToolResult, error) {
	return tools.ToolResult{Content: "ok"}, nil
}

// A device-scoped tool call must move the live session clock so the reaper
// never pauses a container mid-build; host-side tools must not. The E2E
// proved the gap: a 15-minute gradle run is one tool call, and with no
// touches the session looked idle and died. Observed through IdleSessions
// (the reaper's own read path): touched sessions drop out of a 1s window,
// untouched ones stay in it.
func TestDeviceToolCallsTouchSession(t *testing.T) {
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("touch@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Touch", "v1", "touch", "com.talon.touch", "com.talon.touch", "touchapp")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}

	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)
	bs := h.newBuildSession(t.TempDir(), proj.ID, 0, "localhost:15555", nil)
	bs.reg.Register(stubDeviceTool{name: "build_apk"})
	bs.reg.Register(stubDeviceTool{name: "read_file"})
	exec := bs.executor()

	time.Sleep(2100 * time.Millisecond)
	exec(context.Background(), "build_apk", json.RawMessage(`{}`))
	idle, err := st.IdleSessions(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range idle {
		if s.ID == sess.ID {
			t.Fatalf("device tool call must touch the session clock (still idle): %+v", s)
		}
	}

	// Fresh idle session for the negative half: host-side calls move nothing.
	sess2, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	exec(context.Background(), "read_file", json.RawMessage(`{}`))
	idle, err = st.IdleSessions(1 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range idle {
		if s.ID == sess2.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("host-side tool call must NOT touch the session clock")
	}
}
