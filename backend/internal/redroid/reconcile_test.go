package redroid

import (
	"path/filepath"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/store"
)

// A session whose container is gone must be marked destroyed and its port
// released — otherwise a crash leaves phantom sessions holding ports
// forever. Uses the real docker daemon for listing, but only the
// DB→docker half of the sweep (sweepOrphans=false): the destructive orphan
// arm takes the production path, and the census below proves this run
// touched nothing (FM-TESTa).
func TestReconcileDestroysGoneContainers(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon unavailable")
	}
	st, err := store.New(filepath.Join(t.TempDir(), "reconcile.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err := st.CreateUser("rec@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A container id that cannot exist: 64 hex chars, zero match in docker.
	if err := st.UpdateSessionContainer(sess.ID, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "gone", 15555); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(NewPortAllocator(15555, 15560), 2)
	if err != nil {
		t.Fatal(err)
	}
	// FM-TESTa: "nothing is created or removed" is an assertion, not a
	// comment. Snapshot the daemon census across the call — if the sweep
	// touches any container the test does not own, this goes RED instead
	// of quietly killing a live server's fleet.
	beforeIDs := dockerCensus(t, mgr)
	// sweepOrphans=false: the test drives only the DB→docker half. The
	// census still runs as the guard — if a later change re-arms the
	// destructive arm here, this test goes RED instead of quietly killing
	// a live server's fleet (FM-TESTa).
	if err := mgr.reconcile(st, false); err != nil {
		t.Fatalf("reconcile must not fail on a missing container: %v", err)
	}
	afterIDs := dockerCensus(t, mgr)
	for id := range beforeIDs {
		if !afterIDs[id] {
			t.Errorf("FM-TESTa: census lost %s — the test's Reconcile removed a container it does not own; read-only was false", shortID(id))
		}
	}
	for id := range afterIDs {
		if !beforeIDs[id] {
			t.Errorf("FM-TESTa: census gained %s — the test's Reconcile created a container", shortID(id))
		}
	}

	got, err := st.GetSession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "destroyed" {
		t.Errorf("expected destroyed, got %q", got.State)
	}
	if got.ContainerID != "" || got.ADBPort != 0 {
		t.Errorf("expected container refs cleared, got %+v", got)
	}
}

func dockerAvailable() bool {
	mgr, err := NewManager(NewPortAllocator(15555, 15560), 1)
	if err != nil {
		return false
	}
	_ = mgr
	return true
}

// dockerCensus is the set of every container the product's name filter can
// see — the before/after pair FM-TESTa compares.
func dockerCensus(t *testing.T, mgr *Manager) map[string]bool {
	t.Helper()
	list, err := mgr.ListContainers()
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	out := make(map[string]bool, len(list))
	for _, c := range list {
		out[c.ContainerID] = true
	}
	return out
}

// FM-POOLa: the sweep's decision as a pure function — a container is
// removed only when no active session claims it AND the live warm pool has
// not prepaid it (i). A warm boot belongs to no session by design (negative
// id), so without the isWarm arm the 5-minute cleanup loop collects the
// pool's own prepaid container every tick. (iii) The mirror: nil isWarm —
// no pool wired, the startup sweep — must still plan foreign leftovers, or
// a crashed process's warm container leaks 1GB/1CPU forever.
//
// RED pre-fix: orphanPlan does not exist — the decision is welded into the
// docker-touching loop where a unit test cannot reach it.
func TestOrphanPlanSparesClaimedAndWarm(t *testing.T) {
	warm := ContainerInfo{ContainerID: "warm1", SessionID: -1}
	stray := ContainerInfo{ContainerID: "stray1", SessionID: 7}
	live := ContainerInfo{ContainerID: "live1", SessionID: 3}
	claimed := map[string]*store.Session{"live1": {}}
	isWarm := func(id string) bool { return id == "warm1" }

	plan := orphanPlan([]ContainerInfo{warm, stray, live}, claimed, isWarm)
	if len(plan) != 1 || plan[0].ContainerID != "stray1" {
		t.Errorf("FM-POOLa(i): plan = %v, want exactly [stray1] — the unclaimed stray is swept, the live session and the prepaid warm entry are spared", plan)
	}

	// (iii) startup, no pool: the warm-shaped leftover is foreign now and
	// must still be collected.
	if plan := orphanPlan([]ContainerInfo{warm}, claimed, nil); len(plan) != 1 {
		t.Errorf("FM-POOLa(iii): with no pool wired the startup sweep must still plan foreign leftovers, got %v", plan)
	}
}

// decideAction is the pause-vs-destroy judgement, unit-tested without
// Docker: paused sleeps (resume), halted states die (destroy), running
// recovers. The E2E proved destroying paused containers kills mid-turn
// device flows; this pins the corrected behavior.
func TestDecideAction(t *testing.T) {
	cases := []struct {
		exists bool
		state  string
		want   containerAction
	}{
		{false, "", actionDestroy},
		{false, "running", actionDestroy},
		{true, "running", actionRecover},
		{true, "paused", actionResume},
		{true, "exited", actionDestroy},
		{true, "dead", actionDestroy},
		{true, "removing", actionDestroy},
		{true, "created", actionDestroy},
		{true, "weird-state", actionDestroy},
	}
	for _, c := range cases {
		if got := decideAction(c.exists, c.state); got != c.want {
			t.Errorf("decideAction(%v, %q) = %v, want %v", c.exists, c.state, got, c.want)
		}
	}
}
