package redroid

import "testing"

// The allocator's claim-side rebooking: a warm container's port moves to
// the claiming session without a free/allocate race window where another
// boot could steal it.
func TestPortAllocatorRebook(t *testing.T) {
	pa := NewPortAllocator(15555, 15560)
	// The pool boots under a negative id...
	warmID := int64(-7)
	port, err := pa.Allocate(warmID)
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	// ...then a real session claims it.
	pa.rebookTo(42, port)
	// The port is booked to the session now...
	if got, err := pa.Allocate(43); err != nil || got == port {
		t.Errorf("fault: rebooked port %d was allocatable again (%v)", port, err)
	}
	// ...and releasing the session frees exactly that port.
	pa.Release(42)
	if got, err := pa.Allocate(44); err != nil || got != port {
		t.Errorf("fault: port %d not freed by session release (got %d)", port, got)
	}
}

// ReleaseByPort frees by value — the pool's Stop path knows ports, not ids.
func TestPortAllocatorReleaseByPort(t *testing.T) {
	pa := NewPortAllocator(15555, 15560)
	port, _ := pa.Allocate(-1)
	pa.ReleaseByPort(port)
	if got, err := pa.Allocate(-2); err != nil || got != port {
		t.Errorf("fault: ReleaseByPort left %d taken (got %d)", port, got)
	}
}

// Warm ids are negative and unique — they can never collide with real
// session ids (store ids start at 1) or with each other.
func TestPoolSessionIDsNegativeAndUnique(t *testing.T) {
	seen := map[int64]bool{}
	for i := 0; i < 10; i++ {
		id := poolSessionID()
		if id >= 0 {
			t.Fatalf("fault: warm id %d is not negative", id)
		}
		if seen[id] {
			t.Fatalf("fault: warm id %d reused", id)
		}
		seen[id] = true
	}
}

// A zero-size pool is a no-op, not a panic: Claim returns nil and Stop is
// safe — WARM_POOL_SIZE=0 must mean the exact pre-DV4 flow.
func TestZeroPoolIsNoop(t *testing.T) {
	p := NewPool(nil, 0)
	if c := p.Claim(1); c != nil {
		t.Errorf("fault: empty pool claimed a container: %+v", c)
	}
	if n := len(p.Warm()); n != 0 {
		t.Errorf("fault: empty pool reports %d warm", n)
	}
	p.Stop() // must not panic with a nil manager
}
