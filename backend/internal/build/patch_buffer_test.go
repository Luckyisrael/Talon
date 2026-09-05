package build

import (
	"fmt"
	"testing"
)

func TestStageAndAccept(t *testing.T) {
	b := NewPatchBuffer()
	if ev := b.Stage("a.tsx", []byte("v1"), 1); ev != "" {
		t.Errorf("no eviction expected, got %q", ev)
	}
	pre, ok := b.PreImage("a.tsx")
	if !ok || string(pre) != "v1" {
		t.Errorf("pre-image round trip failed: %q %v", pre, ok)
	}
	if b.Len() != 1 {
		t.Errorf("len should be 1, got %d", b.Len())
	}
	b.Accept("a.tsx")
	if _, ok := b.PreImage("a.tsx"); ok {
		t.Error("accepted entry should be gone")
	}
	b.Accept("missing.tsx") // benign no-op
}

func TestFIFOEvictionNotices(t *testing.T) {
	b := NewPatchBuffer()
	for i := 0; i < MaxEntries; i++ {
		b.Stage(fmt.Sprintf("f%d.tsx", i), []byte("x"), 1)
	}
	evicted := b.Stage("overflow.tsx", []byte("y"), 2)
	if evicted != "f0.tsx" {
		t.Errorf("oldest entry should evict first, got %q", evicted)
	}
	if b.Len() != MaxEntries {
		t.Errorf("len should stay capped at %d, got %d", MaxEntries, b.Len())
	}
	if _, ok := b.PreImage("f0.tsx"); ok {
		t.Error("evicted entry should be gone")
	}
}

func TestRestageRefreshes(t *testing.T) {
	b := NewPatchBuffer()
	b.Stage("a.tsx", []byte("v1"), 1)
	b.Stage("b.tsx", []byte("v1"), 1)
	b.Stage("a.tsx", []byte("v2"), 2) // re-stage moves a behind b
	for i := 0; i < MaxEntries-2; i++ {
		b.Stage(fmt.Sprintf("f%d.tsx", i), []byte("x"), 3)
	}
	// Buffer now holds b, a, f0..f47 (50). Next stage evicts b, not a.
	if ev := b.Stage("last.tsx", []byte("z"), 4); ev != "b.tsx" {
		t.Errorf("re-staged entry should survive, evicted %q", ev)
	}
	if pre, _ := b.PreImage("a.tsx"); string(pre) != "v2" {
		t.Errorf("re-stage should replace content, got %q", pre)
	}
}
