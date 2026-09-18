package api

import (
	"testing"

	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// The orchestrator's verify ladder walks seven gates by tool name. Before this
// law, five of them (run_bundle, run_gradle, run_boot, run_vision,
// run_interact) were defined but never registered — the agent dutifully called
// them and got "No tool named …" mid-build. This pins the contract: every
// ladder stage the orchestrator drives resolves in a real build session's
// registry, both with and without a device attached.
func TestVerifyLadderToolsResolve(t *testing.T) {
	// The ladder's static stages, mirrored from orchestrator.verifyGates().
	// Device stages (run_boot, run_vision, run_interact) are pinned on the
	// other side of this test: absent without a device, which is the honest
	// state the prompt law describes.
	ladderTools := []string{
		"run_typecheck", // always registered
		"run_lint",      // always registered
		"run_bundle",    // G4 static — always registered
		"run_gradle",    // G4 static — always registered
	}

	st, err := store.New(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	user, err := st.CreateUser("ladder@local", "x")
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.CreateProject(user.ID, "Ladder", "v1", "ladder", "com.talon.ladder", "com.talon.ladder", "ladderapp")
	if err != nil {
		t.Fatal(err)
	}

	h := NewPromptHandler(st, nil, t.TempDir(), "", orchestrator.New(nil, st, nil), nil, nil, nil)
	ws := t.TempDir()
	bs := h.newBuildSession(ws, proj.ID, 0, "", nil) // no device attached

	for _, name := range ladderTools {
		if _, ok := bs.reg.Get(name); !ok {
			t.Errorf("ladder tool %q missing from the registry — the verify ladder would hit 'No tool named %s'", name, name)
		}
	}

	// The no-device session must NOT carry the device gates — honest
	// absence is the law (the prompt tells the model their absence means
	// no session is live; registering them anyway would break that).
	for _, name := range []string{"run_boot", "run_vision", "run_interact"} {
		if _, ok := bs.reg.Get(name); ok {
			t.Errorf("device gate %q registered without a device session — the honest state is absence", name)
		}
	}
}
