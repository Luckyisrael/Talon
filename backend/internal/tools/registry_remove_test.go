package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type removeProbe struct{ ran bool }

func (p *removeProbe) Name() string        { return "remove_probe" }
func (p *removeProbe) Description() string { return "probe" }
func (p *removeProbe) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (p *removeProbe) Execute(context.Context, json.RawMessage) (ToolResult, error) {
	p.ran = true
	return ToolResult{Content: "ran"}, nil
}

// Remove takes the tool out of both surfaces: the advertised catalog and
// execution. A name that is gone must be GONE — depth-1's second wall in a
// child registry depends on delegate being unexecutable, not merely
// unadvertised (law 12: comments must not lie about behavior).
func TestRegistryRemoveTakesToolOutOfCatalogAndDispatch(t *testing.T) {
	r := NewRegistry()
	p := &removeProbe{}
	r.Register(p)

	if _, ok := r.Get("remove_probe"); !ok {
		t.Fatal("probe must register")
	}
	r.Remove("remove_probe")

	if _, ok := r.Get("remove_probe"); ok {
		t.Error("removed tool still resolves through Get")
	}
	for _, d := range r.List() {
		if d.Name == "remove_probe" {
			t.Error("removed tool still advertised")
		}
	}
	// Unknown names are a model-visible failed call (is_error), never a
	// Go error — assert the tool did not run and the name is gone.
	res, err := r.Execute(context.Background(), "remove_probe", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("execute of a removed name: %v", err)
	}
	if !res.IsError {
		t.Errorf("removed tool executed: %q", res.Content)
	}
	if p.ran {
		t.Error("removed tool ran")
	}

	// Removing an unknown name is a no-op, not a panic.
	r.Remove("never_registered")

	// Re-registering after removal works (the name is reusable).
	r.Register(p)
	if _, ok := r.Get("remove_probe"); !ok {
		t.Error("re-register after remove must work")
	}
}
