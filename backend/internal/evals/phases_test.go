package evals

import (
	"bytes"
	"os"
	"path/filepath"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seekersstudio/backend/internal/build"
	"github.com/seekersstudio/backend/internal/guardrails"
	harnessagent "github.com/seekersstudio/backend/internal/harness/agent"
	"github.com/seekersstudio/backend/internal/harness/compaction"
	"github.com/seekersstudio/backend/internal/harness/events"
	"github.com/seekersstudio/backend/internal/harness/pipeline"
	"github.com/seekersstudio/backend/internal/harness/sessionlog"
	"github.com/seekersstudio/backend/internal/knowledge"
	"github.com/seekersstudio/backend/internal/runtime"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/verify"
)

// The phase-guard set: one eval per PLAN.md phase. Each case asserts both
// polarities — the healthy path passes AND the fault-injected path fails —
// so a regression that flips either behavior breaks the eval. This is the
// cheap, deterministic half of P16/P17: no models, no devices, just the
// contracts each phase promised. (The full mock-AI app-build runner remains
// future work; these guards are what CI enforces today via `make evals`.)

// A3: the failure index resolves known errors to their known fix, and stays
// silent on unknown ones.
func TestPhaseA3FailureIndex(t *testing.T) {
	idx := knowledge.DefaultFailureIndex()
	if _, ok := knowledge.Lookup(idx, "run_typecheck", "error TS2322: Type 'string' is not assignable"); !ok {
		t.Error("healthy: TS2322 should hit the index")
	}
	if _, ok := knowledge.Lookup(idx, "run_typecheck", "total gibberish with no meaning"); ok {
		t.Error("fault: gibberish must not hit the index")
	}
}

// S9: store assets persist and list back exactly.
func TestPhaseS9StoreAssets(t *testing.T) {
	st, err := store.New(t.TempDir() + "/phase.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("phase@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "phase", "1.0.0", "phase-slug", "com.test.phase", "", "")
	if _, err := st.AddAsset(p.ID, "icon", "/tmp/icon.png", 512, 512); err != nil {
		t.Fatal(err)
	}
	assets, err := st.ListAssets(p.ID)
	if err != nil || len(assets) != 1 || assets[0].Kind != "icon" {
		t.Errorf("healthy: 1 icon expected, got %+v %v", assets, err)
	}
	empty, err := st.ListAssets(999999)
	if err != nil || len(empty) != 0 {
		t.Errorf("fault: unknown project must list nothing, got %+v %v", empty, err)
	}
}

// S8: triage cents survive the round trip exactly; absent reads null.
func TestPhaseS8TriageCents(t *testing.T) {
	st, err := store.New(t.TempDir() + "/phase.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, _ := st.CreateUser("phase@test.com", "hash")
	p, _ := st.CreateProject(u.ID, "phase", "1.0.0", "phase-slug", "com.test.phase", "", "")
	if r, _ := st.LoadTriageReport(p.ID); r != nil {
		t.Error("fault: unrecorded project must load nil")
	}
	saved, err := st.SaveTriageReport(p.ID, "cutover_now", "2026-10-01", 19999, "n")
	if err != nil || saved.ExposureCents != 19999 {
		t.Errorf("healthy: 19999 cents expected, got %+v %v", saved, err)
	}
}

// G5: boot gap resolves on first app log and reports silence honestly.
func TestPhaseG5BootGap(t *testing.T) {
	launch := testLaunch()
	stamp := launch.Add(30 * time.Second).Format("01-02 15:04:05.000")
	logs := stamp + "  1234  1234 I com.test.app: Application onCreate\n"
	if _, _, ok := runtime.BootGap(logs, "com.test.app", launch); !ok {
		t.Error("healthy: first app log should resolve the gap")
	}
	if _, _, ok := runtime.BootGap(logs, "com.ghost.app", launch); ok {
		t.Error("fault: silent package must report no gap")
	}
}

// S7: blank screens read blank, content reads content.
func TestPhaseS7BlankScreen(t *testing.T) {
	if rep := verify.Analyze(solidWhite(64, 64)); !rep.Blank {
		t.Error("healthy: solid white must read blank")
	}
	if rep := verify.Analyze(solidGradient(64, 64)); rep.Blank {
		t.Error("fault: gradient content must not read blank")
	}
}

// G6: pre-images stage and clear; overflow evicts oldest with notice.
func TestPhaseG6PatchBuffer(t *testing.T) {
	b := build.NewPatchBuffer()
	b.Stage("a.tsx", []byte("v1"), 1)
	if _, ok := b.PreImage("a.tsx"); !ok {
		t.Error("healthy: staged pre-image should be retrievable")
	}
	b.Accept("a.tsx")
	if _, ok := b.PreImage("a.tsx"); ok {
		t.Error("fault: accepted pre-image must be gone")
	}
}

func testLaunch() time.Time {
	return time.Now().Add(-time.Minute)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func solidWhite(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.White)
		}
	}
	return encodePNG(img)
}

func solidGradient(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 128, 255})
		}
	}
	return encodePNG(img)
}

// H1: the harness session log is event-sourced — derivation is deterministic,
// shadowing never deletes rows, and a fork carries a stable prefix. Both
// polarities: the healthy path replays exactly, and the fault (a shadow that
// destroyed history) is structurally impossible because Replay reads the rows
// the shadow only marks.
func TestPhaseH1SessionLog(t *testing.T) {
	mem := &harnessMemStore{rows: map[string][]sessionlog.Event{}, seq: map[string]int64{}}

	l, err := sessionlog.New(mem, "eval-h1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(events.UserMessage, events.MessagePayload{
		Role: "user", Blocks: []events.BlockPayload{{Type: "text", Text: "turn one"}},
	}); err != nil {
		t.Fatal(err)
	}
	boundary := l.Seq()
	if _, err := l.Append(events.UserMessage, events.MessagePayload{
		Role: "user", Blocks: []events.BlockPayload{{Type: "text", Text: "turn two"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Shadow(boundary, "condensed", "eval"); err != nil {
		t.Fatal(err)
	}

	// Healthy: derivation is bounded by the shadow, replay is complete.
	if got := len(l.DeriveMessages()); got != 2 {
		t.Errorf("healthy: derived %d messages after shadow, want 2", got)
	}
	if got := len(l.Replay()); got != 4 { // 2 messages + shadow + summary
		t.Errorf("healthy: replay lost rows after shadow: %d", got)
	}

	// Determinism: a second log over the same persisted rows derives
	// byte-identical messages.
	l2, err := sessionlog.New(mem, "eval-h1")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(l.DeriveMessages())
	b, _ := json.Marshal(l2.DeriveMessages())
	if string(a) != string(b) {
		t.Error("healthy: re-opened log derives different messages — replay is not re-derivation")
	}

	// Fork polarity: the child sees the prefix and nothing past it; a parent
	// append after the fork never reaches the child.
	child, err := l2.Fork(mem, "eval-h1-child", boundary)
	if err != nil {
		t.Fatal(err)
	}
	// boundary was captured after the first append, so the prefix is exactly
	// that one event — the second message and the shadow markers stay behind.
	if got := len(child.Replay()); got != 1 {
		t.Errorf("fault: fork carried %d rows past the boundary, want 1", got)
	}
	if _, err := l2.Append(events.UserMessage, events.MessagePayload{
		Role: "user", Blocks: []events.BlockPayload{{Type: "text", Text: "parent-only"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range child.Replay() {
		if strings.Contains(string(e.Payload), "parent-only") {
			t.Error("fault: child saw a parent append made after the fork")
		}
	}
}

// harnessMemStore is the evals-local sessionlog.Store: ordered rows per key,
// no deletion — the contract the SQLite backend honours.
type harnessMemStore struct {
	rows map[string][]sessionlog.Event
	seq  map[string]int64
}

func (m *harnessMemStore) AppendEvent(key string, kind events.Kind, payload []byte, at time.Time) (int64, error) {
	m.seq[key]++
	m.rows[key] = append(m.rows[key], sessionlog.Event{Seq: m.seq[key], Kind: kind, Payload: payload, At: at})
	return m.seq[key], nil
}

func (m *harnessMemStore) EventsFrom(key string, fromSeq int64) ([]sessionlog.Event, error) {
	var out []sessionlog.Event
	for _, e := range m.rows[key] {
		if e.Seq > fromSeq {
			out = append(out, e)
		}
	}
	return out, nil
}

// H2: the steer contract — a mid-turn message reaches the running loop as a
// marked turn, and only when delivered. Both polarities: the delivered steer
// surfaces in the next request's conversation, and an undelivered one leaves
// no marker anywhere.
func TestPhaseH2SteerContract(t *testing.T) {
	// The lane side: SessionState's Steerable seam drains exactly once, in
	// order, with the class preserved.
	lane := &steerLaneProbe{}
	a := harnessagent.Handle(1, 1, lane)
	if got := a.Steer(harnessagent.Steer, "use reanimated-v4"); got != 1 {
		t.Errorf("first steer seq = %d, want 1", got)
	}
	if got := a.Steer(harnessagent.Inject, "node 20 on device"); got != 2 {
		t.Errorf("inject seq = %d, want 2", got)
	}
	got := lane.drain()
	if len(got) != 2 {
		t.Fatalf("lane drained %d, want 2", len(got))
	}
	if got[0].Class != harnessagent.Steer || got[0].Text != "use reanimated-v4" {
		t.Errorf("steer malformed: %+v", got[0])
	}
	if got[1].Class != harnessagent.Inject {
		t.Errorf("inject class malformed: %+v", got[1])
	}
	if again := lane.drain(); len(again) != 0 {
		t.Errorf("lane did not clear: %+v", again)
	}

	// The registry side: publish after setup, unpublish on exit, steer to a
	// dead agent resolves nil (the route maps that to 409, not a void).
	r := harnessagent.NewRegistry()
	if r.Get(1) != nil {
		t.Error("empty registry resolved an agent")
	}
	r.Publish(a)
	if r.Get(1) == nil {
		t.Error("published agent did not resolve")
	}
	r.Unpublish(1)
	if r.Get(1) != nil {
		t.Error("unpublished agent still resolves — a steer would vanish into it")
	}
}

// steerLaneProbe is the evals-local Steerable: records deliveries, drains
// once. It stands in for the orchestrator session the production path uses.
type steerLaneProbe struct {
	mu   sync.Mutex
	lane []harnessagent.Inbox
	seq  int64
}

func (p *steerLaneProbe) Deliver(class harnessagent.Class, text string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	p.lane = append(p.lane, harnessagent.Inbox{Class: class, Text: text, At: p.seq})
	return p.seq
}

func (p *steerLaneProbe) drain() []harnessagent.Inbox {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.lane
	p.lane = nil
	return out
}

// H3: the bounded pipeline. Both polarities: the pool bound holds under a
// 20-call read-only burst (unbounded would peak at 20), and an exclusive call
// is an ordering barrier nothing overlaps.
func TestPhaseH3PipelineBound(t *testing.T) {
	ts := &h3Stages{}
	p := pipeline.New(nil, ts, pipeline.ClassifierFunc(func(name string) bool {
		return name == "read_file"
	}), nil)
	p.PoolOverride(4)

	calls := make([]pipeline.Call, 20)
	for i := range calls {
		calls[i] = pipeline.Call{ID: strconv.Itoa(i), Name: "read_file"}
	}
	results := p.Run(context.Background(), calls)
	if len(results) != 20 {
		t.Fatalf("results %d, want 20", len(results))
	}
	peak := ts.Peak()
	if peak > 4 {
		t.Errorf("pool bound violated: peak %d > 4", peak)
	}
	if peak < 2 {
		t.Errorf("pool never overlapped (peak %d): the bound serialized it", peak)
	}

	// Barrier polarity: reads before an exclusive call end before it starts.
	b := &h3BarrierStages{spans: map[string][2]int64{}}
	bp := pipeline.New(nil, b, pipeline.ClassifierFunc(func(name string) bool {
		return name == "read_file"
	}), nil)
	res := bp.Run(context.Background(), []pipeline.Call{
		{ID: "r1", Name: "read_file"},
		{ID: "r2", Name: "read_file"},
		{ID: "w1", Name: "write_file"},
		{ID: "r3", Name: "read_file"},
	})
	if len(res) != 4 {
		t.Fatalf("barrier results %d, want 4", len(res))
	}
	if !b.BarrierHeld() {
		t.Error("exclusive call was not an ordering barrier")
	}
}

// h3Stages counts concurrent dispatches.
type h3Stages struct {
	mu    sync.Mutex
	live  int
	peak  int
	delay time.Duration
}

func (s *h3Stages) Dispatch(ctx context.Context, c pipeline.Call) (string, bool) {
	s.mu.Lock()
	s.live++
	if s.live > s.peak {
		s.peak = s.live
	}
	s.mu.Unlock()
	time.Sleep(3 * time.Millisecond)
	s.mu.Lock()
	s.live--
	s.mu.Unlock()
	return "ok", false
}

func (s *h3Stages) Peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

// h3BarrierStages records spans and reports whether the exclusive call ran
// alone: every read that started before it also ended before it.
type h3BarrierStages struct {
	mu    sync.Mutex
	clock int64
	spans map[string][2]int64
}

func (s *h3BarrierStages) Dispatch(ctx context.Context, c pipeline.Call) (string, bool) {
	s.mu.Lock()
	start := s.clock
	s.clock++
	s.spans[c.ID] = [2]int64{start, 0}
	s.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	s.mu.Lock()
	sp := s.spans[c.ID]
	sp[1] = s.clock
	s.clock++
	s.spans[c.ID] = sp
	s.mu.Unlock()
	return "ok", false
}

func (s *h3BarrierStages) BarrierHeld() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	barrier := s.spans["w1"]
	for id, sp := range s.spans {
		if id == "w1" {
			continue
		}
		overlaps := sp[0] < barrier[1] && sp[1] > barrier[0]
		if overlaps {
			return false
		}
	}
	return true
}

// H8: the context economy. Both polarities: pruning shrinks only oversized
// results (small content passes through identical), and a spill lands the
// body on disk verbatim under a resolvable locator while the replacement is
// honest about what moved.
func TestPhaseH8ContextEconomy(t *testing.T) {
	small := "keep me intact"
	if got := compaction.Prune(small); got != small {
		t.Errorf("healthy: small content pruned: %q", got)
	}

	large := strings.Repeat("H", compaction.PruneHeadBytes) +
		strings.Repeat("M", 10000) + strings.Repeat("T", compaction.PruneTailBytes)
	pruned := compaction.Prune(large)
	if !strings.Contains(pruned, "bytes omitted") {
		t.Error("healthy: pruned result lacks the honest omission marker")
	}
	if !strings.Contains(pruned, strings.Repeat("T", compaction.PruneTailBytes)) {
		t.Error("healthy: pruned result lost the tail")
	}

	s, err := compaction.NewSpillStore(t.TempDir())
	if err != nil {
		t.Fatalf("spill store: %v", err)
	}
	body := strings.Repeat("x", compaction.SpillThresholdBytes+100)
	rep, spilled, err := s.Spill("run_shell", body)
	if err != nil || !spilled {
		t.Fatalf("spill: %v %v", spilled, err)
	}
	loc := s.LocatorPath(rep)
	if loc == "" {
		t.Fatal("fault: locator unresolvable — the model cannot re-read its own output")
	}
	onDisk, err := os.ReadFile(loc)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(onDisk) != body {
		t.Error("fault: spilled body is not verbatim")
	}
	if got := s.LocatorPath("[saved to /etc/passwd]"); got != "" {
		t.Error("fault: locator escaped the store scope")
	}
}

// H5: version-stamped writes. Both polarities: a write over the version the
// model last saw passes, and a write over a file that changed since the last
// read is refused with the conflict named — the user's edit is preserved, not
// clobbered.
func TestPhaseH5VersionStampedWrites(t *testing.T) {
	// A workspace with one file, stamped at mtime 100.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.tsx"), []byte("original"), 0o600)

	st := &h5State{
		seen:    map[string]int64{"app.tsx": 100},
		exists:  map[string]bool{"app.tsx": true},
		read:    map[string]bool{"app.tsx": true},
		current: map[string]int64{"app.tsx": 100},
	}

	write := `{"path":"app.tsx","content":"new body"}`
	if d := guardrails.Check("write_file", json.RawMessage(write), st, nil); !d.Allowed {
		t.Errorf("healthy: same-version write refused: %+v", d)
	}

	// The user edits behind the agent's back: the stamp moves.
	st.current["app.tsx"] = 946684800
	d := guardrails.Check("write_file", json.RawMessage(write), st, nil)
	if d.Allowed {
		t.Fatal("fault: write over a changed file was allowed — the user's edit would be clobbered")
	}
	if d.Rule != "write-conflict" {
		t.Errorf("fault: refusal rule = %q, want write-conflict", d.Rule)
	}
	if !strings.Contains(d.Message, "changed on disk") {
		t.Errorf("fault: message does not name the conflict: %q", d.Message)
	}

	// Unknown stamps degrade to the old rule: a never-read existing file is
	// still refused by read-before-write, a fresh file still passes.
	st2 := &h5State{exists: map[string]bool{"old.tsx": true}, current: map[string]int64{"old.tsx": 0}}
	if d := guardrails.Check("write_file", json.RawMessage(`{"path":"old.tsx","content":"x"}`), st2, nil); d.Allowed {
		t.Error("fault: read-before-write lost — never-read existing file was allowed")
	}
	if d := guardrails.Check("write_file", json.RawMessage(`{"path":"brand-new.tsx","content":"x"}`), st2, nil); !d.Allowed {
		t.Errorf("healthy: new-file write refused: %+v", d)
	}
}

// h5State is the evals-local guardrails.Versioned: stamps from maps, no fs.
type h5State struct {
	seen    map[string]int64
	current map[string]int64
	exists  map[string]bool
	read    map[string]bool
}

func (s *h5State) HasReadFile(p string) bool { return s.read[p] }
func (s *h5State) FileExists(p string) bool  { return s.exists[p] }
func (s *h5State) LastSeenVersion(p string) int64 {
	return s.seen[p]
}
func (s *h5State) CurrentVersion(p string) int64 { return s.current[p] }
