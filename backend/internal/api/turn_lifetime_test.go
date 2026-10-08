package api

// FM-AI-BG / FM-IDLE-STARVE — L69: the turn belongs to the server, not to
// whoever happens to be watching it.
//
// The failure modes this file enumerates, each pinned by one assertion:
//
// F1. The app backgrounds (or its fetch dies) mid-build: the transport's
//     context is the turn's context, so RunAgentLoop dies with it and the
//     prompt row settles `cancelled` — the AI "stops whenever the app is
//     not in focus". The turn must outlive its viewer: settle `completed`
//     with nobody watching. [disconnect_sets_nothing_free]
//
// F2. The terminal push and freshness probe lived in the VIEW's post-result
//     block, so a viewer that left took "Build finished" with it. The push
//     is a claim about the turn, and the turn fired it — one build_success
//     row must exist even though no SSE reader was attached. [disconnect_keeps_the_push]
//
// F3. With the viewer gone nobody drains the 128-slot emitter while the
//     model streams 400 deltas — Emit blocks, the agent loop wedges, and
//     the prompt never settles at all. A background drainer must keep the
//     turn consuming (and finishing) with zero subscribers. [disconnect_still_drains]
//
// F4. There is no way to stop a turn: the app's stop button can only drop
//     its stream, and today dropping the stream is what kills the turn —
//     once the turn survives its viewer (F1), stopping needs its own route
//     or the button stops working entirely. [stop_route]
//
// F5. A foregrounded app cannot rejoin the turn it lost: no attach route
//     exists, so re-attach can only 404 and the client is sent to history
//     even though the turn is still running. [reattach_route]
//
// F6. The session's reaper clock (`last_active_at`) is only advanced by
//     device tool calls and viewer polls — a viewer-less turn runs for
//     minutes with a frozen clock and the reaper destroys the session out
//     from under the running work. Turn start + a ticker must keep the
//     clock warm, and the ticker must stop at finalize (a leaked ticker
//     would pin the session forever). [turn_heartbeat]
//
// TEST_RULES rule 1 — red first: F1–F3 are behavioral reds (run this file
// before the L69 fix: F1's row reads `cancelled`, F2's count reads 0) —
// observed red 2026-10-08. F4–F6 are compile-reds added after that
// observation (a compile failure would have masked it): `h.Stop undefined`,
// `h.Attach undefined`, `undefined: turnHeartbeatInterval`.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/notify"
)

// TestBuildTurnSurvivesViewerDisconnect drops the stream's request context
// while the model call is in flight — the exact shape of an app going to
// background — then lets the model answer into the void. The turn must
// finish on its own: status `completed`, one build_success push, no hung
// viewer goroutine.
func TestBuildTurnSurvivesViewerDisconnect(t *testing.T) {
	hit := make(chan struct{})
	release := make(chan struct{})

	script := func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx > 1 {
			// A restart would answer benignly; the disconnect, not a retry
			// storm, decides this test's outcome.
			_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			return
		}
		close(hit)
		<-release
		// 400 deltas blow past the emitter's 128-slot buffer: with the
		// viewer gone, only a background drainer stops this from wedging
		// the agent loop (F3).
		for i := 0; i < 400; i++ {
			_, _ = io.WriteString(w, livenessContentFrame(fmt.Sprintf("delta-%d ", i)))
		}
		_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
		_, _ = io.WriteString(w, livenessFinishFrame("stop"))
		_, _ = io.WriteString(w, livenessDoneFrame())
	}
	provider := newLivenessProvider(t, script)
	h := newLivenessHarness(t, provider, nil)
	// The harness wires no notifier and dispatchNotification no-ops on nil,
	// but the product always has one (main.go passes notify.Get) — F2 counts
	// the in-app history rows Dispatch records before Configured(), exactly
	// as production records them. Without this the push assertion could only
	// ever read 0.
	h.handler.notifier = notify.Noop{}

	// Once-guarded release: a t.Fatal between hit and close would otherwise
	// leave the stand-in's handler blocked and hang httptest.Server.Close.
	var releaseOnce sync.Once
	releaseRound := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRound()

	// The stream's request context is cancelable: cancelling it is the
	// transport dying, exactly what the phone does when it backgrounds.
	base := authedRequest(h.userID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"build a streak app","mode":"build"}`, h.projectID))
	ctx, cancel := context.WithCancel(base.Context())
	defer cancel()
	req := base.WithContext(ctx)

	rec := httptest.NewRecorder()
	viewDone := make(chan struct{})
	go func() {
		defer close(viewDone)
		h.handler.Stream(rec, req)
	}()

	select {
	case <-hit:
		// The model call is in flight; the viewer is attached.
	case <-time.After(15 * time.Second):
		t.Fatal("the model was never called — the turn never started")
	}

	cancel()
	select {
	case <-viewDone:
		// The view detaches promptly — it must not block the turn either.
	case <-time.After(5 * time.Second):
		t.Fatal("the stream view never returned after its request context died")
	}

	// Nobody is watching now. Let the model answer.
	releaseRound()

	// F1+F3: the prompt must settle on its own — and settle completed.
	deadline := time.Now().Add(20 * time.Second)
	status := "running"
	for time.Now().Before(deadline) {
		p, err := h.store.GetLatestPrompt(h.projectID)
		if err != nil {
			t.Fatalf("GetLatestPrompt: %v", err)
		}
		if p != nil && p.Status != "running" {
			status = p.Status
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "completed" {
		t.Errorf("the turn died with its viewer:\n"+
			"prompt status = %q, want %q (FM-AI-BG: the turn runs on a server-owned context, "+
			"not the request's)", status, "completed")
	}

	// F2: the terminal push belongs to the turn, not the view.
	pushDeadline := time.Now().Add(5 * time.Second)
	pushes := 0
	for time.Now().Before(pushDeadline) {
		pushes = len(countBuildSuccess(t, h))
		if pushes > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pushes != 1 {
		t.Errorf("terminal push with no viewer attached:\n"+
			"build_success notifications = %d, want exactly 1 (FM-AI-BG: push dispatched "+
			"from the turn, not from whoever held the SSE connection)", pushes)
	}
}

// promptParamRequest builds an authed request carrying a hand-injected chi
// {id} param — the established pattern for exercising param-reading handlers
// directly (the route/param contract test keeps these honest against main.go).
func promptParamRequest(userID, promptID int64) *http.Request {
	req := authedRequest(userID, "{}")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(promptID, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// syncRecorder is an http.ResponseWriter the test can safely observe while
// the handler runs: `wrote` closes on the first WriteHeader, so waiting on
// it means "the handler has passed validation and committed to streaming".
// The attach contract this pins: Attach joins the emitter BEFORE writing
// headers — once `wrote` fires, releasing the model cannot race the join.
type syncRecorder struct {
	mu      sync.Mutex
	header  http.Header
	code    int
	codeSet bool
	body    bytes.Buffer
	wrote   chan struct{}
	once    sync.Once
}

func newSyncRecorder() *syncRecorder {
	return &syncRecorder{header: make(http.Header), wrote: make(chan struct{})}
}

func (s *syncRecorder) Header() http.Header { return s.header }

func (s *syncRecorder) WriteHeader(code int) {
	s.mu.Lock()
	if !s.codeSet {
		s.code, s.codeSet = code, true
	}
	s.mu.Unlock()
	s.once.Do(func() { close(s.wrote) })
}

func (s *syncRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.codeSet {
		s.code, s.codeSet = http.StatusOK, true
		s.once.Do(func() { close(s.wrote) })
	}
	return s.body.Write(b)
}

func (s *syncRecorder) Flush() {}

func (s *syncRecorder) Code() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

func (s *syncRecorder) Body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.String()
}

// TestStopRouteCancelsTheTurn pins F4: the app's stop button needs a route,
// because once the turn survives its viewer (the test above) there is no
// connection left to drop. Stop cancels the turn's own context: the turn
// settles `cancelled` (deliberate stop is a cancel, not a failure), the
// attached view ends, and a second Stop finds no running turn — 409.
//
// Compile-red before the L69 fix: `h.Stop undefined`.
func TestStopRouteCancelsTheTurn(t *testing.T) {
	hit := make(chan struct{})
	release := make(chan struct{})
	script := func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx == 1 {
			close(hit)
			<-release // Stop fires first; this round never gets to answer
			return
		}
		_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
		_, _ = io.WriteString(w, livenessFinishFrame("stop"))
		_, _ = io.WriteString(w, livenessDoneFrame())
	}
	provider := newLivenessProvider(t, script)
	h := newLivenessHarness(t, provider, nil)
	var releaseOnce sync.Once
	releaseRound := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRound()

	rec := httptest.NewRecorder()
	viewDone := make(chan struct{})
	go func() {
		defer close(viewDone)
		h.handler.Stream(rec, authedRequest(h.userID,
			fmt.Sprintf(`{"project_id":%d,"prompt":"build a streak app","mode":"build"}`, h.projectID)))
	}()

	select {
	case <-hit:
	case <-time.After(15 * time.Second):
		t.Fatal("the model was never called — the turn never started")
	}
	p, err := h.store.GetLatestPrompt(h.projectID)
	if err != nil || p == nil {
		t.Fatalf("GetLatestPrompt: prompt=%v err=%v", p, err)
	}

	stopRec := httptest.NewRecorder()
	h.handler.Stop(stopRec, promptParamRequest(h.userID, p.ID)) // compile-red: undefined
	if stopRec.Code != http.StatusOK {
		t.Errorf("Stop on a running turn: status %d, want %d (FM-AI-BG: the stop route the "+
			"app's stop button needs)", stopRec.Code, http.StatusOK)
	}

	// The attached view must end when the turn it watches ends.
	select {
	case <-viewDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the attached view never ended after Stop cancelled the turn")
	}

	if got := promptStatus(t, h, p.ID); got != "cancelled" {
		t.Errorf("deliberate stop:\nprompt status = %q, want %q (FM-AI-BG: Stop cancels the "+
			"turn context; terminalStatus still records the cancel)", got, "cancelled")
	}

	// Nothing is running now — a second Stop must refuse, not no-op.
	stopRec2 := httptest.NewRecorder()
	h.handler.Stop(stopRec2, promptParamRequest(h.userID, p.ID))
	if stopRec2.Code != http.StatusConflict {
		t.Errorf("Stop with no running turn: status %d, want %d (409 — there is no turn "+
			"to cancel)", stopRec2.Code, http.StatusConflict)
	}
}

// TestReattachStreamsARunningTurn pins F5: the foregrounded app must be able
// to rejoin the turn it lost. View 1 detaches (its request context dies
// mid-turn — the turn must keep running), then Attach joins the same turn:
// live frames from now on, then the terminal done frame, then completion.
// Attaching again after the turn ends refuses — 409 sends the client to the
// persisted history instead.
//
// Compile-red before the L69 fix: `h.Attach undefined`.
func TestReattachStreamsARunningTurn(t *testing.T) {
	hit := make(chan struct{})
	release := make(chan struct{})
	script := func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx == 1 {
			close(hit)
			<-release
			_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			return
		}
		_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
		_, _ = io.WriteString(w, livenessFinishFrame("stop"))
		_, _ = io.WriteString(w, livenessDoneFrame())
	}
	provider := newLivenessProvider(t, script)
	h := newLivenessHarness(t, provider, nil)
	var releaseOnce sync.Once
	releaseRound := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRound()

	// View 1: attached, then its transport dies mid-turn.
	base := authedRequest(h.userID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"build a streak app","mode":"build"}`, h.projectID))
	ctx, cancel := context.WithCancel(base.Context())
	rec1 := httptest.NewRecorder()
	view1Done := make(chan struct{})
	go func() {
		defer close(view1Done)
		h.handler.Stream(rec1, base.WithContext(ctx))
	}()
	select {
	case <-hit:
	case <-time.After(15 * time.Second):
		t.Fatal("the model was never called — the turn never started")
	}
	cancel()
	select {
	case <-view1Done:
	case <-time.After(10 * time.Second):
		t.Fatal("the detached view never ended")
	}
	p, err := h.store.GetLatestPrompt(h.projectID)
	if err != nil || p == nil {
		t.Fatalf("GetLatestPrompt: prompt=%v err=%v", p, err)
	}

	// Re-attach: join the still-running turn.
	rec2 := newSyncRecorder()
	attachDone := make(chan struct{})
	go func() {
		defer close(attachDone)
		h.handler.Attach(rec2, promptParamRequest(h.userID, p.ID)) // compile-red: undefined
	}()
	select {
	case <-rec2.wrote: // Attach committed to streaming ⇒ it has joined the emitter
	case <-time.After(10 * time.Second):
		t.Fatalf("Attach never started streaming (code %d)", rec2.Code())
	}

	// Only now let the model answer: every frame lands after the join.
	releaseRound()

	select {
	case <-attachDone:
	case <-time.After(20 * time.Second):
		t.Fatal("the attached view never ended after the turn finished")
	}
	out := rec2.Body()
	if !strings.Contains(out, "event: done") || !strings.Contains(out, `"status":"success"`) {
		t.Errorf("re-attached view missed the terminal frame:\n%s", out)
	}
	if !strings.Contains(out, livenessFinalText) {
		t.Errorf("re-attached view missed live frames emitted after the join:\n%s", out)
	}
	if got := promptStatus(t, h, p.ID); got != "completed" {
		t.Errorf("turn after re-attach:\nprompt status = %q, want %q", got, "completed")
	}

	// The turn is over: attach refuses and the client falls back to history.
	stopRec := httptest.NewRecorder()
	h.handler.Attach(stopRec, promptParamRequest(h.userID, p.ID))
	if stopRec.Code != http.StatusConflict {
		t.Errorf("Attach after the turn ended: status %d, want %d (409 — history refetch "+
			"is the fallback)", stopRec.Code, http.StatusConflict)
	}
}

// idleHasSession reports whether the reaper's clock sees this session as
// idle for the given window — the only exported lens on last_active_at.
func idleHasSession(t *testing.T, h *livenessHarness, sessionID int64) bool {
	t.Helper()
	idle, err := h.store.IdleSessions(time.Second)
	if err != nil {
		t.Fatalf("IdleSessions: %v", err)
	}
	for _, s := range idle {
		if s.ID == sessionID {
			return true
		}
	}
	return false
}

// TestTurnHeartbeatKeepsSessionWarm pins F6 / FM-IDLE-STARVE: last_active_at
// was only ever advanced by device tool calls and viewer polls, so a turn
// nobody polls ran with a frozen reaper clock — backgrounding the app
// freezes the viewer's poll, and the reaper would destroy the session under
// the running work. Three states, one observation lens (IdleSessions over a
// 1 s window; every "old enough" assertion sleeps past 2 s so SQLite's
// second-granular timestamps can't blur the boundary):
//
//  1. Baseline: a session nobody touches ages into the window.
//  2. During/after a turn: turn start + the turnHeartbeatInterval ticker
//     keep the clock fresh — the session must NOT read idle, even though
//     the turn outlasts the window.
//  3. After finalize: the ticker must stop — the clock freezes again (a
//     leaked ticker would pin the session warm forever).
//
// Compile-red before the L69 fix: `undefined: turnHeartbeatInterval`.
func TestTurnHeartbeatKeepsSessionWarm(t *testing.T) {
	prev := turnHeartbeatInterval
	turnHeartbeatInterval = 100 * time.Millisecond
	defer func() { turnHeartbeatInterval = prev }()

	script := func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx == 1 {
			// A turn longer than the idle window: turn-start touch alone
			// would read idle by the time it finishes — only the ticker
			// keeps the clock fresh across it.
			time.Sleep(2500 * time.Millisecond)
		}
		_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
		_, _ = io.WriteString(w, livenessFinishFrame("stop"))
		_, _ = io.WriteString(w, livenessDoneFrame())
	}
	provider := newLivenessProvider(t, script)
	h := newLivenessHarness(t, provider, nil)

	sess, err := h.store.CreateSession(h.projectID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 1. Baseline: untouched, the session ages into the reaper's window.
	time.Sleep(2200 * time.Millisecond)
	if !idleHasSession(t, h, sess.ID) {
		t.Fatal("baseline: an untouched session should read idle after 2s — the " +
			"observation lens itself is broken")
	}

	// 2. The turn runs past the window with nobody polling the session.
	rec := httptest.NewRecorder()
	h.handler.Stream(rec, authedRequest(h.userID,
		fmt.Sprintf(`{"project_id":%d,"prompt":"build a streak app","mode":"build"}`, h.projectID)))
	if idleHasSession(t, h, sess.ID) {
		t.Errorf("a running turn let its session's reaper clock freeze:\n" +
			"session reads idle immediately after a >2s turn (FM-IDLE-STARVE: turn start " +
			"+ turnHeartbeatInterval ticker must keep last_active_at warm)")
	}

	// 3. The ticker stops at finalize: without work, the clock ages again.
	time.Sleep(2200 * time.Millisecond)
	if !idleHasSession(t, h, sess.ID) {
		t.Errorf("the turn's heartbeat outlived the turn:\n" +
			"session still warm 2s after finalize (the ticker must stop when the " +
			"turn does — a leaked ticker pins the session against the reaper forever)")
	}
}

// promptStatus reads one prompt's current status, waiting briefly for a
// terminal write that may still be in flight behind the view's return.
func promptStatus(t *testing.T, h *livenessHarness, promptID int64) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := h.store.GetPrompt(promptID)
		if err != nil {
			t.Fatalf("GetPrompt: %v", err)
		}
		if p.Status != "running" || time.Now().After(deadline) {
			return p.Status
		}
		time.Sleep(50 * time.Millisecond)
	}
}
