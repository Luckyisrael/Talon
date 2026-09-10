package agent

import (
	"sync"
	"testing"
	"time"
)

// memSession is a Steerable that records deliveries.
type memSession struct {
	mu    sync.Mutex
	lane  []Inbox
	seq   int64
	block bool
}

func (m *memSession) Deliver(class Class, text string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	m.lane = append(m.lane, Inbox{Class: class, Text: text, At: m.seq})
	return m.seq
}

func (m *memSession) laneSnapshot() []Inbox {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Inbox(nil), m.lane...)
}

// A live session receives the steer immediately — the whole point of the
// handle — and the handle's own inbox stays empty for it.
func TestSteerReachesLiveSession(t *testing.T) {
	sess := &memSession{}
	a := Handle(1, 1, sess)
	if got := a.Steer(Steer, "redirect"); got != 1 {
		t.Errorf("first deliver seq = %d, want 1", got)
	}
	if got := a.Steer(Inject, "context"); got != 2 {
		t.Errorf("second deliver seq = %d, want 2", got)
	}
	lane := sess.laneSnapshot()
	if len(lane) != 2 || lane[0].Class != Steer || lane[0].Text != "redirect" {
		t.Errorf("lane wrong: %+v", lane)
	}
	if a.Pending() != 0 {
		t.Errorf("live handle kept %d in its own inbox", a.Pending())
	}
}

// A detached handle (no session) queues in its inbox; Drain hands the queue
// over exactly once, preserving arrival order.
func TestDetachedHandleQueuesAndDrains(t *testing.T) {
	a := Handle(2, 2, nil)
	if a.Pending() != 0 {
		t.Fatal("fresh handle not empty")
	}
	a.Steer(Steer, "first")
	a.Steer(Inject, "second")
	if a.Pending() != 2 {
		t.Fatalf("queued %d, want 2", a.Pending())
	}
	got := a.Drain()
	if len(got) != 2 || got[0].Text != "first" || got[1].Text != "second" {
		t.Errorf("drain wrong: %+v", got)
	}
	if a.Pending() != 0 || len(a.Drain()) != 0 {
		t.Error("drain did not clear the inbox")
	}
}

// Deliveries from many goroutines (the SSE route under load) all land, in
// globally unique sequence order — a lost or duplicated steer is a lost
// user redirect.
func TestConcurrentDeliverAllLand(t *testing.T) {
	sess := &memSession{}
	a := Handle(3, 3, sess)
	const n = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			a.Steer(Steer, "x")
		}()
	}
	close(start)
	wg.Wait()
	lane := sess.laneSnapshot()
	if len(lane) != n {
		t.Fatalf("delivered %d of %d", len(lane), n)
	}
	seen := map[int64]bool{}
	for _, in := range lane {
		if seen[in.At] {
			t.Errorf("duplicated sequence %d", in.At)
		}
		seen[in.At] = true
	}
}

// Publish is rollback-honest: an unpublished id is invisible, a published one
// resolves, unpublish is idempotent, and Len tracks.
func TestRegistryPublishUnpublish(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 || r.Get(42) != nil {
		t.Fatal("fresh registry not empty")
	}
	a := Handle(42, 42, nil)
	r.Publish(a)
	if r.Len() != 1 || r.Get(42) != a {
		t.Fatal("publish failed")
	}
	r.Unpublish(42)
	r.Unpublish(42)
	if r.Len() != 0 || r.Get(42) != nil {
		t.Fatal("unpublish left residue")
	}
}

// A nil-session handle never blocks: Steer on a detached agent is a queue
// append, not a delivery attempt that could panic.
func TestNilSessionNeverPanics(t *testing.T) {
	a := Handle(9, 9, nil)
	done := make(chan int64, 1)
	go func() { done <- a.Steer(Steer, "late") }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Steer on detached handle blocked")
	}
}
