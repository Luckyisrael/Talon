package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// W1.4: run_gate was defined, unit-tested, and documented since the G4
// ladder — but never registered, so the model asking "what did the verify
// chain report?" always got "No tool named run_gate". This guards the
// wiring in both handler shapes the suite builds: with an orchestrator
// (provider reads the loop's accumulator) and with a nil one (provider
// nil-guards). The def must reach the model-visible catalog, and the tool
// must classify read-only so the loop may run it concurrently with reads.
func TestRunGateRegisteredAndWired(t *testing.T) {
	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("gate@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Gate", "v1", "gate", "com.talon.gate", "com.talon.gate", "gateapp")
	if err != nil {
		t.Fatal(err)
	}

	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)
	bs := h.newBuildSession(t.TempDir(), proj.ID, 0, "", nil)

	tool, ok := bs.reg.Get("run_gate")
	if !ok {
		t.Fatal("run_gate is not registered — the model cannot ask what the verify chain reports")
	}
	found := false
	for _, d := range bs.defs {
		if d.Name == "run_gate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("run_gate missing from the model-visible tool definitions")
	}
	if !bs.reg.IsReadOnly("run_gate") {
		t.Fatal("run_gate must be read-only — it only renders the loop's accumulator")
	}

	// Fresh session: the accumulator holds no gates, so the honest answer is
	// "no chain run yet" — not a pass claim for gates that never ran.
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, "no chain run yet") {
		t.Fatalf("fresh session report: isError=%v content=%q", res.IsError, res.Content)
	}

	// Nil-orchestrator handler (the testinfo shape): provider nil-guards.
	h2 := NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil)
	bs2 := h2.newBuildSession(t.TempDir(), proj.ID, 0, "", nil)
	tool2, ok := bs2.reg.Get("run_gate")
	if !ok {
		t.Fatal("run_gate missing with a nil orchestrator")
	}
	res2, err := tool2.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || res2.IsError || !strings.Contains(res2.Content, "no chain run yet") {
		t.Fatalf("nil-orch execute: err=%v isError=%v content=%q", err, res2.IsError, res2.Content)
	}
}
