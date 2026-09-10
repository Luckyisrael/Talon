package composition

import (
	"errors"
	"testing"
)

// Overlay precedence: base < mode < preset, per key; unmentioned keys pass
// through; each layer replaces whole values (no merge).
func TestOverlayPrecedence(t *testing.T) {
	ov := Overlay{
		Base:   map[string]string{"model": "base-model", "pool": "4", "log": "info"},
		Mode:   map[string]string{"model": "mode-model", "http": "on"},
		Preset: map[string]string{"model": "preset-model"},
	}
	got := ov.Apply()
	want := map[string]string{
		"model": "preset-model", // preset wins
		"pool":  "4",            // base untouched by higher layers
		"log":   "info",
		"http":  "on", // mode introduced
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra keys leaked: %v", got)
	}
}

// Preset resolution + the switch guard: unknown presets refuse; a project
// with output cannot switch (mid-turn composition change breaks cache
// stability); a fresh project can.
func TestPresetSwitchGuard(t *testing.T) {
	p, err := Resolve(" Expert ")
	if err != nil || p.Name != "expert" || p.Discipline != "compressed" {
		t.Fatalf("healthy: resolve failed: %+v %v", p, err)
	}
	if _, err := Resolve("nope"); !errors.Is(err, ErrUnknownPreset) {
		t.Fatalf("fault: unknown preset resolved: %v", err)
	}

	pol := SwitchPolicy{}
	if err := pol.CanSwitch(false); err != nil {
		t.Errorf("healthy: fresh project cannot switch: %v", err)
	}
	if err := pol.CanSwitch(true); !errors.Is(err, ErrNoOutputYet) {
		t.Errorf("fault: mid-output switch allowed: %v", err)
	}
	// The remedy is in the error text.
	if err := pol.CanSwitch(true); !contains(err.Error(), "finish or revert") {
		t.Errorf("fault: refusal lacks the remedy: %s", err)
	}

	// Starter/expert/fast differ in the knobs that matter.
	starter, _ := Resolve("starter")
	fast, _ := Resolve("fast")
	if !starter.StartGuide || fast.StartGuide {
		t.Error("preset knobs drifted: starter should carry the guide, fast should not")
	}
	if !fast.RoutingTightPrune {
		t.Error("fast preset should tighten the pruner")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
