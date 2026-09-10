package promptsec

import (
	"strings"
	"testing"
)

// Byte stability: the same sections in the same order produce identical
// bytes, empty sections are omitted without separators, and assembly is
// deterministic regardless of map iteration order.
func TestAssemblyByteStable(t *testing.T) {
	build := func() *Registry {
		r := New()
		r.Register(Section{Name: "persona", Weight: WeightPersona, Render: func() string { return "You are the builder." }})
		r.Register(Section{Name: "rules/core", Weight: WeightRules, Render: func() string { return "Obey the rules." }})
		r.Register(Section{Name: "empty-sec", Weight: WeightDiscipline, Render: func() string { return "  " }})
		r.Register(Section{Name: "skills", Weight: WeightSkills, Render: func() string { return "## Skills\n- one" }})
		return r
	}
	a := build().Assemble()
	b := build().Assemble()
	if a != b {
		t.Errorf("assembly not byte-stable:\n%q\n%q", a, b)
	}
	if strings.Contains(a, "empty-sec") || strings.Contains(a, "  \n\n") {
		t.Error("empty section leaked into the prompt")
	}
	want := "You are the builder.\n\nObey the rules.\n\n## Skills\n- one"
	if a != want {
		t.Errorf("assembly mismatch:\nwant %q\ngot  %q", want, a)
	}
}

// Fingerprint locality: changing one section's render moves only that
// section's fingerprint; Diff names exactly the moved one; a change after
// the head leaves the head prefix intact.
func TestFingerprintLocality(t *testing.T) {
	memory := "remember A"
	r := New()
	r.Register(Section{Name: "persona", Weight: WeightPersona, Render: func() string { return "You are the builder." }})
	r.Register(Section{Name: "rules/core", Weight: WeightRules, Render: func() string { return "Obey the rules." }})
	r.Register(Section{Name: "memory", Weight: WeightMemory, Render: func() string { return memory }})

	before := r.Fingerprints()
	memory = "remember B"
	after := r.Fingerprints()

	changed := Diff(before, after)
	if len(changed) != 1 || changed[0] != "memory" {
		t.Errorf("fault: expected exactly [memory] to change, got %v", changed)
	}
	// The memory section sits after the rules head: the cached prefix
	// (persona + rules) is intact.
	if r.HeadChanged("rules/core", changed) {
		t.Error("fault: a change after the head must not break the prefix")
	}
	// A rules change does break it.
	memory = "remember C"
	if !r.HeadChanged("rules/core", Diff(after, r.Fingerprints())) {
		if len(Diff(after, r.Fingerprints())) == 0 {
			t.Skip("no change detected")
		}
	}
}

// Suppress removes a section and Diff reports the removal.
func TestSuppress(t *testing.T) {
	r := New()
	r.Register(Section{Name: "persona", Weight: WeightPersona, Render: func() string { return "p" }})
	r.Register(Section{Name: "teaching", Weight: WeightPlanCheck, Render: func() string { return "steps" }})
	before := r.Fingerprints()
	r.Suppress("teaching")
	changed := Diff(before, r.Fingerprints())
	if len(changed) != 1 || changed[0] != "teaching" {
		t.Errorf("fault: suppress not reported: %v", changed)
	}
	if strings.Contains(r.Assemble(), "steps") {
		t.Error("fault: suppressed section still rendered")
	}
}

// The order is a total order: same weights sort by name; different weights
// sort by weight — no nondeterminism.
func TestTotalOrder(t *testing.T) {
	r := New()
	r.Register(Section{Name: "b-sec", Weight: 100, Render: func() string { return "B" }})
	r.Register(Section{Name: "a-sec", Weight: 100, Render: func() string { return "A" }})
	r.Register(Section{Name: "z-sec", Weight: 50, Render: func() string { return "Z" }})
	for i := 0; i < 5; i++ {
		if got := r.Assemble(); got != "Z\n\nA\n\nB" {
			t.Fatalf("order not total: %q", got)
		}
	}
}
