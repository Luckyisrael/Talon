package promptsec

import (
	"strings"
	"testing"
)

// Sizes is the per-layer half of L13's ledger (FM-TELa): the prompt-sec log
// reports each section's byte contribution, and the parts must reconcile
// with what Assemble actually produced — sections + blank-line separators.
// A table that cannot reconcile is the "? row" of prompt telemetry: it
// looks measured and cannot be summed.
func TestSizesReconcileWithAssemble(t *testing.T) {
	r := New()
	r.Register(Section{Name: "persona", Weight: WeightPersona,
		Render: func() string { return "PERSONA" }})
	r.Register(Section{Name: "map", Weight: WeightProjectMap,
		Render: func() string { return strings.Repeat("m", 300) }})
	r.Register(Section{Name: "empty", Weight: WeightMemory,
		Render: func() string { return "   " }})

	sizes := r.Sizes()
	if len(sizes) != 2 {
		t.Fatalf("empty renders must be omitted (Assemble skips them), got %d sizes", len(sizes))
	}
	sum := 0
	for _, s := range sizes {
		switch s.Name {
		case "persona":
			if s.Bytes != len("PERSONA") {
				t.Errorf("persona = %d bytes, want %d", s.Bytes, len("PERSONA"))
			}
		case "map":
			if s.Bytes != 300 {
				t.Errorf("map = %d bytes, want 300", s.Bytes)
			}
		default:
			t.Errorf("unexpected section %q", s.Name)
		}
		sum += s.Bytes
	}
	assembled := r.Assemble()
	if want := sum + 2*(len(sizes)-1); want != len(assembled) {
		t.Fatalf("sections (%d) + separators = %d, but Assemble is %d bytes — the ledger cannot reconcile",
			sum, want, len(assembled))
	}
}
