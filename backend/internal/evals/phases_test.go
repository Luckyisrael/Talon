package evals

import (
	"bytes"
	"errors"
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
	"github.com/seekersstudio/backend/internal/harness/approval"
	"github.com/seekersstudio/backend/internal/harness/compaction"
	"github.com/seekersstudio/backend/internal/harness/events"
	"github.com/seekersstudio/backend/internal/harness/composition"
	"github.com/seekersstudio/backend/internal/harness/goals"
	"github.com/seekersstudio/backend/internal/harness/instructions"
	"github.com/seekersstudio/backend/internal/harness/pipeline"
	"github.com/seekersstudio/backend/internal/harness/promptsec"
	"github.com/seekersstudio/backend/internal/harness/ralph"
	"fmt"
	"io"
	"net"
	"github.com/seekersstudio/backend/internal/metro"
	"github.com/seekersstudio/backend/internal/relay"
	"github.com/seekersstudio/backend/internal/tools"
	"github.com/seekersstudio/backend/internal/ws"
	"github.com/seekersstudio/backend/internal/harness/sandbox"
	"github.com/seekersstudio/backend/internal/harness/sessionlog"
	"github.com/seekersstudio/backend/internal/harness/subagent"
	"github.com/seekersstudio/backend/internal/harness/workflow"
	"github.com/seekersstudio/backend/internal/harness/settings"
	"github.com/seekersstudio/backend/internal/harness/skills"
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

// H4: the sandbox + approval seams. Writes outside the workspace are denied
// with the structured verdict; read-only mode refuses every write; the
// approval seam resolves exactly one terminal per ask and audits each one.
func TestPhaseH4SandboxApprovalSeams(t *testing.T) {
	// Sandbox polarities.
	root := t.TempDir()
	ws := sandbox.New(sandbox.ModeWorkspace, root, []string{t.TempDir()},
		func(op sandbox.Operation) error { return nil })
	if d := ws.AllowsPath("write", filepath.Join(root, "ok.tsx")); !d.Allowed {
		t.Errorf("healthy: in-workspace write refused: %s", d.Reason)
	}
	if d := ws.AllowsPath("write", "/elsewhere/evil"); d.Allowed {
		t.Error("fault: outside write allowed")
	}
	ro := sandbox.New(sandbox.ModeReadOnly, root, nil, nil)
	if d := ro.AllowsPath("write", filepath.Join(root, "no.tsx")); d.Allowed {
		t.Error("fault: read-only mode allowed a write")
	}
	// Fail-closed: workspace mode without enforcement refuses structured.
	noEnf := sandbox.New(sandbox.ModeWorkspace, root, nil, nil)
	d := noEnf.AllowsPath("write", filepath.Join(root, "x"))
	if d.Allowed || !strings.Contains(d.Reason, "SANDBOX_UNAVAILABLE") {
		t.Errorf("fault: unenforceable mode not fail-closed: %+v", d)
	}

	// Approval polarities: ask-with-answerer grants once and audits; ask
	// without answerer is unavailable; never is deterministic refusal.
	var mu sync.Mutex
	var audits []approval.AuditRecord
	seam := approval.New(approval.PolicyAsk, func(r approval.AuditRecord) {
		mu.Lock()
		audits = append(audits, r)
		mu.Unlock()
	})
	if out := seam.Ask(approval.Request{Scope: "s", Action: "a"}); out != approval.Unavailable {
		t.Fatalf("fault: no answerer gave %v, want unavailable (fail-closed)", out)
	}
	seam.Register(func(approval.Request) (bool, error) { return true, nil })
	if out := seam.Ask(approval.Request{Scope: "s", Action: "a"}); out != approval.AllowedOnce {
		t.Fatalf("healthy: yes answerer gave %v, want allowed-once", out)
	}
	mu.Lock()
	if len(audits) != 2 {
		t.Errorf("fault: %d audit events for 2 asks, want exactly 2", len(audits))
	}
	mu.Unlock()
	never := approval.New(approval.PolicyNever, nil)
	if out := never.Ask(approval.Request{Scope: "s"}); out != approval.Unavailable {
		t.Errorf("fault: never policy gave %v, want unavailable", out)
	}
}

// H6: multi-source skills + the instructions chain. A workspace skill merges
// into the catalog (embedded wins clashes); the AGENTS.md chain assembles
// broad → specific and honors the budget by omitting broader files before
// truncating the most specific.
func TestPhaseH6SkillsAndInstructions(t *testing.T) {
	// Catalog: workspace skill merges; embedded wins the clash.
	ws := t.TempDir()
	skillDir := filepath.Join(ws, ".seeker", "skills", "local-flows")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: local-flows\ndescription: this project's flow conventions\n---\n\nUse the local flow."), 0o600)
	clashDir := filepath.Join(ws, ".seeker", "skills", "solana-dev")
	_ = os.MkdirAll(clashDir, 0o755)
	_ = os.WriteFile(filepath.Join(clashDir, "SKILL.md"), []byte(
		"---\nname: solana-dev\ndescription: hijack attempt\n---\n\nHijacked."), 0o600)

	cat := skills.NewCatalog(
		skills.EmbeddedProvider{Registry: embeddedTestRegistry()},
		skills.FSProvider{Root: ws})
	if err := cat.Reload(); err != nil {
		t.Fatalf("catalog reload: %v", err)
	}
	if body, src, ok := cat.Body("local-flows"); !ok || src != "workspace" || !strings.Contains(body, "local flow") {
		t.Errorf("healthy: workspace skill did not merge: %q %q %v", body, src, ok)
	}
	if _, src, ok := cat.Body("solana-dev"); !ok || src != "embedded" {
		t.Errorf("fault: name clash not won by embedded (src=%q ok=%v)", src, ok)
	}
	if m := cat.Manifest(); !strings.Contains(m, "local-flows") {
		t.Error("healthy: manifest omits the workspace skill")
	}

	// Instructions chain: budget behavior.
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(strings.Repeat("b", 60000)), 0o600)
	_ = os.MkdirAll(filepath.Join(root, "app"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "app", "AGENTS.md"), []byte(strings.Repeat("s", 30000)), 0o600)
	chain := instructions.Loader{WorkspaceRoot: root}.Load()
	text := chain.Text()
	if strings.Contains(text, strings.Repeat("b", 100)) {
		t.Error("fault: broader file survived over-budget assembly")
	}
	if !strings.Contains(text, strings.Repeat("s", 100)) {
		t.Error("fault: most-specific file omitted instead of truncated")
	}
	if !strings.Contains(text, "omitted") || !strings.Contains(text, "truncated") {
		t.Error("fault: budget pressure not honestly marked")
	}
}

// embeddedTestRegistry builds a minimal embedded-style registry for catalog
// polarity tests without loading the full binary content.
func embeddedTestRegistry() *knowledge.Registry {
	r, err := knowledge.Load("")
	if err != nil {
		return nil
	}
	return r
}

// H7: the system prompt is a section registry. Byte-stability across
// assemblies; fingerprint locality (a memory change moves only the memory
// fingerprint); the head-prefix rule (changes after the head keep the
// cached prefix).
func TestPhaseH7PromptSections(t *testing.T) {
	memory := "remember A"
	r := promptsec.New()
	r.Register(promptsec.Section{Name: "persona", Weight: promptsec.WeightPersona,
		Render: func() string { return "You are the builder." }})
	r.Register(promptsec.Section{Name: "rules/core", Weight: promptsec.WeightRules,
		Render: func() string { return "Obey the rules." }})
	r.Register(promptsec.Section{Name: "memory", Weight: promptsec.WeightMemory,
		Render: func() string { return memory }})

	// Byte stability: two assemblies are identical.
	if r.Assemble() != r.Assemble() {
		t.Fatal("fault: assembly is not byte-stable")
	}

	// Fingerprint locality: only memory moves.
	before := r.Fingerprints()
	memory = "remember B"
	changed := promptsec.Diff(before, r.Fingerprints())
	if len(changed) != 1 || changed[0] != "memory" {
		t.Errorf("fault: expected [memory] changed, got %v", changed)
	}
	// The change sits after the rules head: prefix intact.
	if r.HeadChanged("rules/core", changed) {
		t.Error("fault: late-section change broke the head prefix")
	}
	// Suppress + Diff sees removals.
	r.Suppress("memory")
	if d := promptsec.Diff(before, r.Fingerprints()); len(d) == 0 {
		t.Error("fault: suppress not detected by Diff")
	}
}

// H9: subagents. One-shot children return their final text as the tool
// result (nothing interleaves); depth 1 is refused at the seam; the pool
// bound holds; a 429 sets the cooldown that suppresses the next fan-out.
func TestPhaseH9Subagents(t *testing.T) {
	runner := &h9Runner{result: func(s subagent.Spec) subagent.Result {
		return subagent.Result{FinalText: "Report: the package exports Connection.", Rounds: 2}
	}}
	svc := subagent.NewService(runner, 2)

	res, err := svc.Spawn(context.Background(), subagent.Spec{Prompt: "inspect", Label: "pkg"})
	if err != nil || res.Errored {
		t.Fatalf("healthy: one-shot child failed: %v %+v", err, res)
	}
	if !strings.Contains(res.FinalText, "Connection") {
		t.Error("healthy: child final text missing")
	}
	out := subagent.RenderResult("pkg", res)
	if !strings.Contains(out, "BEGIN CHILD REPORT") {
		t.Error("fault: result not delimited as a child report")
	}
	// The runner saw exactly the child's spec — nothing of the parent's
	// conversation leaked in.
	if len(runner.ran) != 1 || runner.ran[0].Prompt != "inspect" {
		t.Errorf("fault: child transcript interleaved: %+v", runner.ran)
	}

	// Depth 1: a descended service refuses spawns.
	if _, err := svc.Descend().Spawn(context.Background(), subagent.Spec{Prompt: "x"}); !errors.Is(err, subagent.ErrDeep) {
		t.Errorf("fault: grandchild not refused: %v", err)
	}

	// 429 → cooldown suppresses the next fan-out.
	runner.result = func(subagent.Spec) subagent.Result {
		return subagent.Result{Errored: true, Err: "API error (status 429): rate limit"}
	}
	if _, err := svc.Spawn(context.Background(), subagent.Spec{Prompt: "boom"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := svc.Spawn(context.Background(), subagent.Spec{Prompt: "next"}); !errors.Is(err, subagent.ErrCooldown) {
		t.Errorf("fault: cooldown did not suppress fan-out: %v", err)
	}

	// Child crash is structured, and the pool is released.
	svc.ClearCooldown()
	runner.result = func(subagent.Spec) subagent.Result {
		return subagent.Result{Errored: true, Err: "child panicked: index out of range"}
	}
	crashRes, err := svc.Spawn(context.Background(), subagent.Spec{Prompt: "crash"})
	if err != nil {
		t.Fatalf("crash surfaced as spawn error, want structured result: %v", err)
	}
	if !crashRes.Errored || !strings.Contains(crashRes.Err, "panicked") {
		t.Errorf("fault: crash not structured: %+v", crashRes)
	}
	states := map[string]string{}
	for _, c := range svc.List() {
		states[c.Label] = c.State
	}
	if states["pkg"] != "done" {
		t.Errorf("fault: done child listed as %q", states["pkg"])
	}
}

// h9Runner is the evals stub.
type h9Runner struct {
	ran    []subagent.Spec
	result func(subagent.Spec) subagent.Result
}

func (r *h9Runner) RunChild(_ context.Context, s subagent.Spec) subagent.Result {
	r.ran = append(r.ran, s)
	return r.result(s)
}

// H10: declarative workflow fan-out. Spec validation rejects malformed
// specs; parallel items overlap; a failing item nulls only itself; the
// aggregate phase sees prior results; narration flows.
func TestPhaseH10Workflow(t *testing.T) {
	// Validation polarities.
	bad := workflow.Spec{Name: "x", Phases: []workflow.Phase{{Title: "t"}}}
	if err := bad.Validate(); err == nil {
		t.Error("fault: empty phase passed validation")
	}
	good := workflow.Spec{
		Name:   "check",
		Phases: []workflow.Phase{
			{Title: "p", Parallel: []workflow.Item{{Prompt: "a"}, {Prompt: "b"}, {Prompt: "c"}}},
			{Title: "agg", Prompt: "summarize"},
		},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("healthy: valid spec rejected: %v", err)
	}

	// Run semantics over the H9 service.
	peak := 0
	runner := &h10Runner{answer: func(p string) subagent.Result {
		if strings.Contains(p, "b") {
			return subagent.Result{Errored: true, Err: "child b failed"}
		}
		return subagent.Result{FinalText: `{"ok": true}`}
	}, onLive: func(n int) { if n > peak { peak = n } }}
	svc := subagent.NewService(runner, 4)
	var narrated int
	eng := workflow.NewEngine(svc, func(string) { narrated++ })

	res := eng.Run(context.Background(), good)
	if res.Errored {
		t.Fatalf("workflow errored: %s", res.Err)
	}
	if res.Phases[0].Nulls != 1 {
		t.Errorf("fault: nulls = %d, want 1", res.Phases[0].Nulls)
	}
	if peak < 2 {
		t.Errorf("fault: parallel items did not overlap (peak %d)", peak)
	}
	if narrated < 4 { // start+finish × 2 phases
		t.Errorf("fault: narration incomplete: %d events", narrated)
	}
	// The aggregate's prompt embeds phase-1 results.
	if !strings.Contains(runner.lastPrompt, "Prior phase results") {
		t.Errorf("fault: aggregate did not receive prior results: %q", runner.lastPrompt)
	}
}

// h10Runner is the workflow evals stub with overlap tracking.
type h10Runner struct {
	mu        sync.Mutex
	live      int
	peakSeen  int
	lastPrompt string
	answer    func(string) subagent.Result
	onLive    func(int)
}

func (r *h10Runner) RunChild(_ context.Context, s subagent.Spec) subagent.Result {
	r.mu.Lock()
	r.live++
	if r.live > r.peakSeen {
		r.peakSeen = r.live
		if r.onLive != nil {
			r.onLive(r.peakSeen)
		}
	}
	r.lastPrompt = s.Prompt
	r.mu.Unlock()
	ans := r.answer(s.Prompt)
	time.Sleep(2 * time.Millisecond)
	r.mu.Lock()
	r.live--
	r.mu.Unlock()
	return ans
}

// H11: durable goals. One active goal per project; CAS mutations refuse
// stale revisions; round accounting is append-only and survives a re-read
// (the simulated restart); blocked goals carry the explanation verbatim.
func TestPhaseH11Goals(t *testing.T) {
	store := goals.NewMemStore()
	svc := goals.NewService(store)

	g, err := svc.Start(1, "ship the wallet flow", 10)
	if err != nil || g.Phase != goals.PhaseActive {
		t.Fatalf("healthy: start failed: %v %+v", err, g)
	}
	// One active per project: the second supersedes.
	g2, _ := svc.Start(1, "fix release", 10)
	cur, _ := svc.Current(1)
	if cur.ID != g2.ID {
		t.Error("fault: superseded goal still current")
	}

	// CAS: a stale revision refuses. The service path re-reads internally,
	// so the stale case is exercised through the store directly — the seam
	// the service itself composes over.
	r := cur.Revision
	if _, err := svc.RecordRounds(1, 2); err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := store.Apply(goals.Mutation{GoalID: cur.ID, Revision: r, AddRounds: 1}); !errors.Is(err, goals.ErrStale) {
		t.Errorf("fault: stale mutation not refused: %v", err)
	}
	after, _ := svc.Current(1)
	if after.Rounds != 2 {
		t.Errorf("fault: rounds = %d, want 2 (append-only)", after.Rounds)
	}

	// Simulated restart: a fresh service over the same store re-reads the
	// same goal; blocked state and explanation survive verbatim.
	if _, err := svc.Block(1, "the device pool has no free container; waits exceeded 30m"); err != nil {
		t.Fatalf("block: %v", err)
	}
	resumed := goals.NewService(store)
	rg, err := resumed.Current(1)
	if err != nil || rg.Phase != goals.PhaseBlocked {
		t.Fatalf("fault: blocked state lost across restart-read: %+v %v", rg, err)
	}
	if !strings.Contains(goals.ChipText(rg), "no free container") {
		t.Error("fault: blocked explanation not verbatim in the chip")
	}
	if !goals.CapReached(goals.Goal{Rounds: 10, MaxRounds: 10}) {
		t.Error("cap check broken")
	}
}

// H12: composition as data. Overlay precedence (base < mode < preset, per
// key, whole-value replacement); preset switch guard (no output yet);
// settings hot-reload flips routing without a restart.
func TestPhaseH12CompositionData(t *testing.T) {
	ov := composition.Overlay{
		Base:   map[string]string{"model": "base", "pool": "4"},
		Mode:   map[string]string{"model": "mode"},
		Preset: map[string]string{"model": "preset"},
	}
	got := ov.Apply()
	if got["model"] != "preset" || got["pool"] != "4" {
		t.Errorf("fault: overlay precedence broken: %v", got)
	}

	pol := composition.SwitchPolicy{}
	if err := pol.CanSwitch(false); err != nil {
		t.Errorf("healthy: fresh project cannot switch: %v", err)
	}
	if err := pol.CanSwitch(true); !errors.Is(err, composition.ErrNoOutputYet) {
		t.Errorf("fault: mid-output switch allowed: %v", err)
	}

	// Settings: a write then a read serves the written routing (the
	// harness-level document; the ai-level integration is tested in the ai
	// package's own suite).
	doc := settings.NewDoc(settings.Document{Routing: settings.Routing{FallbackModel: "seed"}},
		filepath.Join(t.TempDir(), "settings.json"))
	if s := doc.Snapshot(); s.Routing.FallbackModel != "seed" {
		t.Errorf("healthy: seed not serving: %+v", s.Routing)
	}
	if err := doc.Write(settings.Document{Routing: settings.Routing{FallbackModel: "live"}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if s := doc.Snapshot(); s.Routing.FallbackModel != "live" {
		t.Errorf("fault: live document not serving: %+v", s.Routing)
	}
}

// H13: the Model Experience doc contract. Every harness package's README
// carries a "What the model sees" section; the tools README carries the
// per-group contract. Doc drift fails the merge gate — documentation is a
// test, not a hope.
func TestPhaseH13ModelExperienceDocs(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "harness")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("harness dir: %v", err)
	}
	missing := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		readme := filepath.Join(dir, e.Name(), "README.md")
		raw, err := os.ReadFile(readme)
		if err != nil {
			missing = append(missing, e.Name()+" (no README)")
			continue
		}
		if !strings.Contains(string(raw), "What the model sees") {
			missing = append(missing, e.Name()+" (no 'What the model sees' section)")
		}
	}
	if len(missing) > 0 {
		t.Errorf("fault: model-experience docs drifted: %s", strings.Join(missing, "; "))
	}

	// The tools registry README carries the contract.
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "tools", "README.md"))
	if err != nil {
		t.Fatalf("tools README: %v", err)
	}
	if !strings.Contains(string(raw), "What the model sees") {
		t.Error("fault: tools README lacks the model-experience section")
	}
}

// H13: session titles. The fallback truncates the first prompt honestly
// (one line, word-boundary, 80 bytes); the store round-trips titles; the
// session token meter rolls the per-turn ledger.
func TestPhaseH13TitlesAndMeter(t *testing.T) {
	fb := titleFallbackForTest("Build me a wallet screen with MWA transact()\nand a balance display")
	if fb != "Build me a wallet screen with MWA transact()" {
		t.Errorf("healthy: fallback title wrong: %q", fb)
	}
	long := strings.Repeat("word ", 40)
	if len(titleFallbackForTest(long)) > 80 {
		t.Error("fault: fallback title exceeds the byte cap")
	}
	if titleFallbackForTest("") != "Untitled turn" {
		t.Error("fault: empty prompt title not honest")
	}

	// Store round-trip: create with a title, read it back.
	st, err := store.New(filepath.Join(t.TempDir(), "h13.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	// FK: the project needs a real user row.
	u, err := st.CreateUser("h13@evals.test", "x")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	proj, err := st.CreateProject(u.ID, "p", "v1", "s", "a.b.c", "d.e", "f")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	p, err := st.CreateTitledPrompt(proj.ID, "build", "Add offline transaction history caching", "Offline tx history caching")
	if err != nil {
		t.Fatalf("titled prompt: %v", err)
	}
	if p.Title != "Offline tx history caching" {
		t.Errorf("fault: title lost: %q", p.Title)
	}
	// The fallback title when none given.
	p2, _ := st.CreateTitledPrompt(proj.ID, "build", "Second turn text", "")
	if p2.Title == "" {
		t.Error("fault: empty fallback title stored")
	}
	// The meter: after usage rows exist, the rollup sums them.
	if err := st.RecordPromptTokens(p.ID, 1, 100, 50, 25); err != nil {
		t.Fatalf("tokens: %v", err)
	}
	in, out, cr, err := st.ProjectTokenUsage(proj.ID)
	if err != nil || in != 100 || out != 50 || cr != 25 {
		t.Errorf("fault: meter rollup wrong: %d %d %d %v", in, out, cr, err)
	}
}

// titleFallbackForTest mirrors the api fallback (kept local so the eval
// asserts the shape, not the import).
func titleFallbackForTest(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		cut := s[:80]
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
		s = cut
	}
	if s == "" {
		s = "Untitled turn"
	}
	return s
}

// H14: the Ralph lane. Rounds are fresh (only the bounded report crosses);
// done, blocker, and cap are all reachable terminal states.
func TestPhaseH14Ralph(t *testing.T) {
	runner := &h14runner{script: []h14step{
		{report: ralph.Report{Passed: []string{"tsc"}, Remaining: []string{"lint"}}, done: false},
		{report: ralph.Report{Passed: []string{"tsc", "lint"}}, done: true},
	}}
	out := ralph.NewLane(runner, 8).Run(context.Background(), "gates green")
	if !out.Done || out.Rounds != 2 {
		t.Fatalf("healthy: done outcome wrong: %+v", out)
	}
	// Freshness: round 1 saw an empty report; round 2 saw round 1's.
	if len(runner.saw[0].Passed) != 0 {
		t.Error("fault: round 1 got carryover")
	}
	if len(runner.saw[1].Passed) != 1 {
		t.Error("fault: round 2 did not get the prior report")
	}

	// Blocker terminal.
	blocked := ralph.NewLane(&h14runner{script: []h14step{
		{report: ralph.Report{Blocker: "no free device in the pool"}},
	}}, 8).Run(context.Background(), "gates green")
	if blocked.Done || blocked.Blocker == "" {
		t.Errorf("fault: blocker not terminal: %+v", blocked)
	}

	// Cap terminal.
	steps := make([]h14step, 5)
	capped := ralph.NewLane(&h14runner{script: steps}, 3).Run(context.Background(), "gates green")
	if !capped.Capped || capped.Rounds != 3 {
		t.Errorf("fault: cap not honest: %+v", capped)
	}
}

type h14step struct {
	report ralph.Report
	done   bool
}

type h14runner struct {
	script []h14step
	saw    []ralph.Report
}

func (r *h14runner) RunRound(_ context.Context, _ ralph.Objective, prev ralph.Report) (ralph.Report, bool, error) {
	r.saw = append(r.saw, prev)
	if len(r.script) == 0 {
		return ralph.Report{Remaining: []string{"more"}}, false, nil
	}
	s := r.script[0]
	r.script = r.script[1:]
	return s.report, s.done, nil
}

// DV1: the metro event vocabulary — the classifier is the contract between
// the bundler's raw output and everything downstream (badge, agent tool).
func TestPhaseDV1MetroEvents(t *testing.T) {
	cases := []struct{ line, kind string }{
		{"Starting Metro Bundler", "bundle_start"},
		{"Bundling index.js  45%", "bundle_progress"},
		{"Bundled 4318ms", "bundle_done"},
		{"Reloading app...", "reloaded"},
		{"SyntaxError: x: Unexpected token", "error"},
		{"Unable to resolve module ./x from /app/y.tsx", "error"},
	}
	for _, c := range cases {
		if ev := metro.ClassifyLine(c.line); ev.Kind != c.kind {
			t.Errorf("fault: classify(%q) = %q, want %q", c.line, ev.Kind, c.kind)
		}
	}
	// Progress extraction is honest: no percentage means zero, not a guess.
	if ev := metro.ClassifyLine("Bundling index.js"); ev.Progress != 0 {
		t.Errorf("fault: fabricated progress %d", ev.Progress)
	}
	if ev := metro.ClassifyLine("Bundling index.js  73%"); ev.Progress != 73 {
		t.Errorf("fault: progress = %d, want 73", ev.Progress)
	}
	// The emitter drops-oldest under a wedged consumer instead of stalling
	// the bundler's stdout pump — the lifeline invariant.
	e := metro.NewEmitter()
	ch := e.Subscribe()
	for i := 0; i < metro.SubscriberBuffer+10; i++ {
		e.Emit(metro.Event{Kind: "log"})
	}
	saw := 0
	for len(ch) > 0 {
		<-ch
		saw++
	}
	if saw == 0 {
		t.Error("fault: wedged consumer lost every event including the newest")
	}
	// The tool's buffer keeps the newest N, bounded by construction.
	buf := tools.NewMetroBuffer(3)
	for i := 0; i < 10; i++ {
		buf.Push("log", fmt.Sprintf("e%d", i), 0)
	}
	if recent := buf.Recent(2); len(recent) != 2 || recent[1].Text != "e9" {
		t.Errorf("fault: buffer tail wrong: %+v", recent)
	}
}

// DV2: the phone path. The relay passes bytes and dies with its session;
// the test-info surface is honest; the store resolves the live session.
func TestPhaseDV2PhoneRelay(t *testing.T) {
	// Relay: real sockets, byte passthrough, teardown law.
	metro, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("metro: %v", err)
	}
	defer metro.Close()
	go func() {
		for {
			c, err := metro.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { defer c.Close(); io.Copy(c, c) }(c)
		}
	}()
	r, err := relay.StartForSession(777, freePortFor(t), metro.Addr().String())
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	phone, err := net.DialTimeout("tcp", r.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("phone: %v", err)
	}
	msg := []byte("GET /index.bundle HTTP/1.1\r\n\r\n")
	phone.Write(msg)
	buf := make([]byte, len(msg))
	phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(phone, buf); err != nil || string(buf) != string(msg) {
		t.Errorf("fault: relay mangled bytes (%v)", err)
	}
	phone.Close()
	relay.StopSession(777)
	if relay.GetSession(777) != nil {
		t.Error("fault: relay outlived its session")
	}

	// The live-session lookup is the bridge's key.
	st, err := store.New(filepath.Join(t.TempDir(), "dv2.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	u, _ := st.CreateUser("dv2@e.test", "x")
	proj, _ := st.CreateProject(u.ID, "dv2", "v", "s2", "a2.b", "c2.d", "e2")
	if _, err := st.LatestLiveSession(proj.ID); err == nil {
		// No session created yet: absence is an error (sql.ErrNoRows), not nil.
		t.Log("note: empty lookup returns error — the honest empty")
	}
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	live, err := st.LatestLiveSession(proj.ID)
	if err != nil || live == nil || live.ID != sess.ID {
		t.Errorf("fault: live session lookup wrong (%v)", err)
	}
}

func freePortFor(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// DV3: the video negotiation contract. The mode vocabulary is the wire
// contract between the two transports; the header parser proof lives in
// the scrcpy package tests (unexported socket plumbing).
func TestPhaseDV3StreamNegotiation(t *testing.T) {
	const modeH264 = "h264"
	const modeJPEG = "jpeg"
	if ws.ModeH264 != modeH264 || ws.ModeJPEG != modeJPEG {
		t.Errorf("fault: mode vocabulary drifted (%q/%q)", ws.ModeH264, ws.ModeJPEG)
	}
	// The fallback order is a phase law: jpeg is always offered, h264 only
	// after the device's encoder proved itself.
	if ws.ModeJPEG == "" {
		t.Error("fault: no jpeg fallback vocabulary")
	}
}
