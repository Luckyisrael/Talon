package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// Stream-liveness E2E (M2). One build turn per subtest against a stand-in
// provider that misbehaves in exactly the ways that used to kill or hang a
// turn. TEST_RULES rule 1 — the failure modes this file enumerates, each
// pinned by one subtest:
//
// F1. Provider drops the connection mid-stream after some frames (read
//     error). The turn must resend a byte-identical request instead of
//     failing.                          [resilient_turn, request 1]
//
// F2. Provider closes cleanly with no finish_reason and no [DONE] —
//     truncation with NO read error. The restart must detect it rather
//     than accept the partial answer.   [resilient_turn, request 3]
//
// F3. Partial text from a killed attempt leaks into the final message
//     (stale sink buffer). The done frame's message must equal the
//     completing attempt's text exactly. [resilient_turn]
//
// F4. Provider sends headers and frames, then stalls forever. A byte-
//     silence watchdog must kill the body within AI_STREAM_STALL and
//     restart. Pre-fix: silent hang until the 10-minute client timeout.
//                                    [stall_watchdog]
//
// F5. Provider streams deltas slowly but continuously (gaps < stall,
//     total > stall). A gap-based watchdog must NOT kill it — a total-
//     elapsed watchdog would.            [healthy_slow_stream]
//
// F6. Provider never sends headers. ResponseHeaderTimeout must bound
//     every attempt and the turn must fail cleanly. Pre-fix: hang until
//     the 10-minute client timeout, per attempt.   [header_hang]
//
// F7. Every attempt dies. The restart budget must bound the turn to
//     1 + AI_STREAM_RESTARTS requests and fail with the exact
//     "the AI stream ended early" message.   [exhaustion_is_bounded]
//
// F8. The shared 10-minute http.Client timeout kills healthy long
//     streams. The streaming client must be timeout-free, with the
//     gap watchdog as its only bound. F4/F5 exercise the replacement.
//
// TEST_RULES rule 2 — artifact: after every subtest that runs, the file
// writes e2e/artifacts/stream-liveness-e2e.json (request log with sha256
// per build request, restart accounting, SSE tail, elapsed, assertion
// results) and t.Logs the path, so a failed run leaves its evidence on
// disk.
//
// The five subtests run sequentially on purpose (no t.Parallel): each
// owns its stand-in provider, its ai.New() client (t.Setenv-scoped), and
// its own store. The async H13 title call is classified out by its
// system-prompt marker, never counted as a build request.
//
// Staged RED captures (pre-fix, from backend/):
//
//	go test ./internal/api -count=1 -v -run 'TestStreamLivenessE2E/(resilient_turn|exhaustion_is_bounded|healthy_slow_stream)'
//	go test ./internal/api -count=1 -v -timeout 60s -run 'TestStreamLivenessE2E/stall_watchdog'
//	go test ./internal/api -count=1 -v -timeout 60s -run 'TestStreamLivenessE2E/header_hang'
//
// GREEN gate: go test ./internal/api -count=1 -v -run TestStreamLivenessE2E

// livenessFinalText is the completing attempt's text in resilient_turn.
// The stale-leak assertion (F3) compares the done message against exactly
// this string; request 3 streams "STALE LEAKED TEXT " before dying, which
// must never appear in the final message.
const livenessFinalText = "Built a minimal wallet interface."

// titleMarker identifies the async H13 title request in the provider's
// request stream (title.go:43 — the system prompt is unique to it).
const titleMarker = "Title this app-development request"

type livenessAssertion struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// livenessReq is one logged build request: index (1-based arrival order),
// byte count, sha256 of the raw body (byte-identical resends compare via
// sha), and the first 400 bytes as a readability head.
type livenessReq struct {
	Index  int    `json:"index"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
	Head   string `json:"head"`
}

type livenessSubtest struct {
	Name          string              `json:"name"`
	OK            bool                `json:"ok"`
	ElapsedMS     int64               `json:"elapsed_ms"`
	BuildRequests []livenessReq       `json:"build_requests"`
	TitleRequests int                 `json:"title_requests"`
	ExtraAttempts int                 `json:"extra_attempts"` // requests beyond the first per round: stream restarts + openStream retries (-1 when rounds unknown)
	SSETail       string              `json:"sse_tail"`
	LastDone      map[string]any      `json:"last_done,omitempty"`
	Assertions    []livenessAssertion `json:"assertions"`
}

type livenessArtifact struct {
	Test        string            `json:"test"`
	GeneratedAt string            `json:"generated_at"`
	Subtests    []livenessSubtest `json:"subtests"`
}

type livenessCheck func(name string, ok bool, detail string)

// ── stand-in provider ──────────────────────────────────

// livenessProvider logs every request it sees, answers the async title
// call benignly, and hands everything else to the subtest's scripted
// behaviour keyed by 1-based build-request index.
type livenessProvider struct {
	t      *testing.T
	server *httptest.Server

	mu          sync.Mutex
	buildBodies [][]byte
	buildReqs   []livenessReq
	titleReqs   int
	script      func(idx int, w http.ResponseWriter, r *http.Request)
}

func newLivenessProvider(t *testing.T, script func(int, http.ResponseWriter, *http.Request)) *livenessProvider {
	t.Helper()
	p := &livenessProvider{t: t, script: script}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(titleMarker)) {
			p.mu.Lock()
			p.titleReqs++
			p.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Habit Tracker"}}]}`)
			return
		}
		p.mu.Lock()
		idx := len(p.buildBodies) + 1
		sum := sha256.Sum256(body)
		head := body
		if len(head) > 400 {
			head = head[:400]
		}
		p.buildBodies = append(p.buildBodies, append([]byte(nil), body...))
		p.buildReqs = append(p.buildReqs, livenessReq{
			Index: idx, Bytes: len(body), SHA256: hex.EncodeToString(sum[:]), Head: string(head),
		})
		p.mu.Unlock()
		p.script(idx, w, r)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *livenessProvider) snapshot() (reqs []livenessReq, bodies [][]byte, titles int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]livenessReq(nil), p.buildReqs...),
		append([][]byte(nil), p.buildBodies...),
		p.titleReqs
}

// ── harness ────────────────────────────────────────────

type livenessHarness struct {
	handler   *PromptHandler
	store     *store.Store
	userID    int64
	projectID int64
	provider  *livenessProvider
}

// newLivenessHarness wires a real ai.Client (pointed at the stand-in), a
// real store, and a real PromptHandler — the same shape the interactive
// stream test uses, with deterministic routing/env pinned so the failure
// classification is reproducible: router off + empty policy means a model
// request error is terminal, the fallback model equals the primary model,
// and no settings file can attach in this test binary.
func newLivenessHarness(t *testing.T, provider *livenessProvider, env map[string]string) *livenessHarness {
	t.Helper()
	base := map[string]string{
		"AI_PROTOCOL":             "openai",
		"AI_BASE_URL":             provider.server.URL,
		"AI_API_KEY":              "test-key",
		"AI_MODEL":                "test-model",
		"AI_MIN_REQUEST_INTERVAL": "0s",
		"AI_MAX_ROUNDS":           "8",
		"ROUTING_POLICY_JSON":     "",
		"TALONSTUDIO_ROUTER":      "",
		"TALONSTUDIO_HOOKS":       "",
		// The product default for the restart budget; stated explicitly so
		// the artifact records what the run was under.
		"AI_STREAM_RESTARTS": "2",
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "liveness.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("liveness@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Wallet", "v1", "wallet", "com.local.wallet", "com.local.wallet", "wallet")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, nil, nil)
	return &livenessHarness{
		handler: h, store: st, userID: user.ID, projectID: project.ID, provider: provider,
	}
}

// runBuildTurn drives POST /prompts/stream for mode=build and returns the
// full SSE body plus the wall time of the turn.
func (h *livenessHarness) runBuildTurn(t *testing.T) (string, time.Duration) {
	t.Helper()
	body := fmt.Sprintf(
		`{"project_id":%d,"prompt":"Build a habit-tracking wallet app shell","mode":"build","permission_mode":"auto"}`,
		h.projectID)
	rec := httptest.NewRecorder()
	start := time.Now()
	h.handler.Stream(rec, authedRequest(h.userID, body))
	elapsed := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Errorf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String(), elapsed
}

// ── SSE frame helpers (provider scripts) ───────────────

func livenessFrame(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

func livenessContentFrame(text string) string {
	return livenessFrame(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{"content": text}}},
	})
}

func livenessFinishFrame(reason string) string {
	return livenessFrame(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": reason}},
	})
}

func livenessDoneFrame() string { return "data: [DONE]\n\n" }

func livenessToolCallStart(id, name string) string {
	return livenessFrame(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": id,
				"function": map[string]any{"name": name, "arguments": ""},
			}},
		}}},
	})
}

func livenessToolCallArgs(args string) string {
	return livenessFrame(map[string]any{
		"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index":    0,
				"function": map[string]any{"arguments": args},
			}},
		}}},
	})
}

// livenessOpenStream writes SSE headers and returns the flusher.
func livenessOpenStream(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	return f
}

// ── SSE parsing (client side) ──────────────────────────

type livenessSSEFrame struct {
	Event string
	Data  string
}

func livenessParseSSE(out string) []livenessSSEFrame {
	var frames []livenessSSEFrame
	for _, block := range strings.Split(out, "\n\n") {
		if block == "" {
			continue
		}
		var ev, data string
		for _, line := range strings.Split(block, "\n") {
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				ev = v
			} else if v, ok := strings.CutPrefix(line, "data: "); ok {
				data = v
			}
		}
		if ev != "" {
			frames = append(frames, livenessSSEFrame{Event: ev, Data: data})
		}
	}
	return frames
}

func livenessErrorMessages(frames []livenessSSEFrame) []string {
	var msgs []string
	for _, f := range frames {
		if f.Event == "error" {
			msgs = append(msgs, f.Data)
		}
	}
	return msgs
}

func livenessLastDone(frames []livenessSSEFrame) map[string]any {
	for i := len(frames) - 1; i >= 0; i-- {
		if frames[i].Event != "done" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(frames[i].Data), &m); err == nil {
			return m
		}
	}
	return nil
}

func livenessTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func livenessReqIndexes(reqs []livenessReq) string {
	parts := make([]string, len(reqs))
	for i, r := range reqs {
		parts[i] = fmt.Sprintf("#%d(%dB)", r.Index, r.Bytes)
	}
	return strings.Join(parts, " ")
}

// livenessExtra derives the restart/retry accounting for the artifact:
// requests beyond the first attempt of each round. -1 when the done
// frame carries no rounds count (failed before the loop recorded one).
func livenessExtra(reqCount int, done map[string]any) int {
	if done == nil {
		return -1
	}
	r, ok := done["rounds"].(float64)
	if !ok {
		return -1
	}
	return reqCount - int(r)
}

func livenessDoneString(done map[string]any, key string) string {
	if done == nil {
		return "<no done frame>"
	}
	v, _ := done[key].(string)
	return v
}

func livenessDoneRounds(done map[string]any) (int, bool) {
	if done == nil {
		return 0, false
	}
	r, ok := done["rounds"].(float64)
	if !ok {
		return 0, false
	}
	return int(r), true
}

// ── runner ─────────────────────────────────────────────

func runLivenessSubtest(
	t *testing.T,
	art *livenessArtifact,
	save func(),
	name string,
	fn func(t *testing.T, res *livenessSubtest, check livenessCheck),
) {
	t.Helper()
	res := livenessSubtest{Name: name}
	ran := false
	t.Run(name, func(st *testing.T) {
		ran = true
		check := func(n string, ok bool, detail string) {
			res.Assertions = append(res.Assertions, livenessAssertion{Name: n, OK: ok, Detail: detail})
			if !ok {
				st.Errorf("%s: %s", n, detail)
			}
		}
		fn(st, &res, check)
		res.OK = !st.Failed()
	})
	if ran {
		art.Subtests = append(art.Subtests, res)
		save()
	}
}

// ── the five failure modes ─────────────────────────────

func TestStreamLivenessE2E(t *testing.T) {
	art := &livenessArtifact{Test: "TestStreamLivenessE2E"}
	save := func() {
		art.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.MarshalIndent(art, "", "  ")
		if err != nil {
			t.Logf("artifact marshal: %v", err)
			return
		}
		path := filepath.Join("..", "..", "..", "e2e", "artifacts", "stream-liveness-e2e.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Logf("artifact dir: %v", err)
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Logf("artifact write: %v", err)
			return
		}
		t.Logf("stream-liveness artifact: %s", path)
	}

	// F1/F2/F3: two rounds, each of which loses its first attempt — the
	// first to a dropped connection, the second to a clean-but-truncated
	// close. Exactly four provider requests must result, resent
	// byte-identically per round, and the final message must be exactly
	// the completing attempt's text.
	runLivenessSubtest(t, art, save, "resilient_turn", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			switch idx {
			case 1:
				// F1: a fragment lands, then the connection dies.
				_, _ = io.WriteString(w, livenessContentFrame("partial fragment "))
				flusher.Flush()
				time.Sleep(20 * time.Millisecond)
				panic(http.ErrAbortHandler)
			case 2:
				// The byte-identical resend completes round 1 with a
				// read-only tool call: empty workspace, real tool result.
				_, _ = io.WriteString(w, livenessToolCallStart("call_1", "list_files"))
				_, _ = io.WriteString(w, livenessToolCallArgs("{}"))
				_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			case 3:
				// F2: text flows, then a CLEAN close — no finish_reason,
				// no [DONE], no read error. Truncation, not a drop.
				_, _ = io.WriteString(w, livenessContentFrame("STALE LEAKED TEXT "))
				flusher.Flush()
			case 4:
				// The restart of round 2 completes. Its text must be the
				// done frame's message verbatim (F3: sink reset).
				_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
				_, _ = io.WriteString(w, livenessFinishFrame("stop"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			default:
				t.Errorf("unexpected build request #%d", idx)
				panic(http.ErrAbortHandler)
			}
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, nil)

		out, elapsed := h.runBuildTurn(t)
		res.ElapsedMS = elapsed.Milliseconds()
		res.SSETail = livenessTail(out, 1500)

		frames := livenessParseSSE(out)
		errMsgs := livenessErrorMessages(frames)
		check("no error events on the resilient turn", len(errMsgs) == 0,
			fmt.Sprintf("got %d: %s", len(errMsgs), strings.Join(errMsgs, " | ")))

		done := livenessLastDone(frames)
		res.LastDone = done
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status success", livenessDoneString(done, "status") == "success",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
		rounds, roundsOK := livenessDoneRounds(done)
		check("done rounds == 2", roundsOK && rounds == 2, fmt.Sprintf("rounds=%d present=%v", rounds, roundsOK))
		msg := livenessDoneString(done, "message")
		check("final message equals completing attempt text (sink reset)",
			msg == livenessFinalText, fmt.Sprintf("message=%q want=%q", msg, livenessFinalText))

		reqs, bodies, titles := provider.snapshot()
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.ExtraAttempts = livenessExtra(len(reqs), done)
		check("exactly 4 build requests (2 rounds x attempt+restart)", len(reqs) == 4,
			fmt.Sprintf("got %d: %s", len(reqs), livenessReqIndexes(reqs)))
		switch {
		case len(bodies) >= 2:
			check("round 1 resent byte-identically", bytes.Equal(bodies[0], bodies[1]),
				fmt.Sprintf("sha %s vs %s", reqs[0].SHA256, reqs[1].SHA256))
		default:
			check("round 1 resent byte-identically", false, "fewer than 2 requests recorded")
		}
		switch {
		case len(bodies) >= 4:
			check("round 2 resent byte-identically", bytes.Equal(bodies[2], bodies[3]),
				fmt.Sprintf("sha %s vs %s", reqs[2].SHA256, reqs[3].SHA256))
		case len(bodies) >= 3:
			check("round 2 resent byte-identically", false,
				fmt.Sprintf("round 2 made %d request(s), want 2", len(bodies)-2))
		default:
			check("round 2 resent byte-identically", false, "round 2 never reached the provider")
		}
		if len(bodies) >= 3 {
			check("round 2 request carries round 1's tool result",
				bytes.Contains(bodies[2], []byte("No source files yet")),
				livenessTail(string(bodies[2]), 300))
		} else {
			check("round 2 request carries round 1's tool result", false, "round 2 never reached the provider")
		}
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))

		// finalizeTurn persists before Stream returns, so the terminal
		// status is durable the moment the stream ends.
		p, err := h.store.GetLatestPrompt(h.projectID)
		check("prompt persisted", err == nil, fmt.Sprintf("err=%v", err))
		if err == nil {
			check("prompt status completed", p.Status == "completed", fmt.Sprintf("status=%q", p.Status))
		}
		t.Logf("resilient_turn: %d build requests, %d title(s), elapsed %v", len(reqs), titles, elapsed)
	})

	// F4: headers and a frame arrive, then permanent silence. The stall
	// watchdog must kill the body within AI_STREAM_STALL and restart.
	runLivenessSubtest(t, art, save, "stall_watchdog", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		final := "Recovered after the stall."
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			switch idx {
			case 1:
				flusher := livenessOpenStream(w)
				_, _ = io.WriteString(w, livenessContentFrame("starting the turn "))
				flusher.Flush()
				<-r.Context().Done() // F4: never send another byte
			case 2:
				flusher := livenessOpenStream(w)
				_, _ = io.WriteString(w, livenessContentFrame(final))
				_, _ = io.WriteString(w, livenessFinishFrame("stop"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			default:
				t.Errorf("unexpected build request #%d", idx)
				panic(http.ErrAbortHandler)
			}
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, map[string]string{"AI_STREAM_STALL": "400ms"})

		out, elapsed := h.runBuildTurn(t)
		res.ElapsedMS = elapsed.Milliseconds()
		res.SSETail = livenessTail(out, 1500)
		check("stalled turn completes in under 10s", elapsed < 10*time.Second, fmt.Sprintf("elapsed=%v", elapsed))

		frames := livenessParseSSE(out)
		errMsgs := livenessErrorMessages(frames)
		check("no error events (restart is silent)", len(errMsgs) == 0,
			fmt.Sprintf("got %d: %s", len(errMsgs), strings.Join(errMsgs, " | ")))

		done := livenessLastDone(frames)
		res.LastDone = done
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status success", livenessDoneString(done, "status") == "success",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
		rounds, roundsOK := livenessDoneRounds(done)
		check("done rounds == 1 (restart stays in-round)", roundsOK && rounds == 1,
			fmt.Sprintf("rounds=%d present=%v", rounds, roundsOK))
		msg := livenessDoneString(done, "message")
		check("message is the restarted attempt's text", msg == final,
			fmt.Sprintf("message=%q want=%q", msg, final))

		reqs, _, titles := provider.snapshot()
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.ExtraAttempts = livenessExtra(len(reqs), done)
		check("exactly 2 build requests (stall + restart)", len(reqs) == 2,
			fmt.Sprintf("got %d: %s", len(reqs), livenessReqIndexes(reqs)))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))
		t.Logf("stall_watchdog: %d build requests, elapsed %v", len(reqs), elapsed)
	})

	// F7: every attempt dies. The budget (1 + AI_STREAM_RESTARTS = 3)
	// must bound the turn, and the terminal error must keep the exact
	// "the AI stream ended early" wording all the way to the wire.
	runLivenessSubtest(t, art, save, "exhaustion_is_bounded", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			_, _ = io.WriteString(w, livenessContentFrame("fragment "))
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
			panic(http.ErrAbortHandler) // F7: every single attempt dies
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, nil)

		out, elapsed := h.runBuildTurn(t)
		res.ElapsedMS = elapsed.Milliseconds()
		res.SSETail = livenessTail(out, 1500)
		check("exhausted turn fails in under 30s", elapsed < 30*time.Second, fmt.Sprintf("elapsed=%v", elapsed))

		frames := livenessParseSSE(out)
		errMsgs := livenessErrorMessages(frames)
		found := false
		for _, m := range errMsgs {
			if strings.Contains(m, "the AI stream ended early") {
				found = true
				break
			}
		}
		check("an error event carries the exact terminal string", found,
			fmt.Sprintf("error events: %s", strings.Join(errMsgs, " | ")))

		done := livenessLastDone(frames)
		res.LastDone = done
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status failed", livenessDoneString(done, "status") == "failed",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))

		reqs, _, titles := provider.snapshot()
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.ExtraAttempts = livenessExtra(len(reqs), done)
		check("exactly 3 build requests (1 + 2 restarts)", len(reqs) == 3,
			fmt.Sprintf("got %d: %s", len(reqs), livenessReqIndexes(reqs)))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))
		t.Logf("exhaustion_is_bounded: %d build requests, elapsed %v", len(reqs), elapsed)
	})

	// F5: slow but flowing. Total elapsed exceeds the stall knob, every
	// individual gap stays under it — a gap-based watchdog must let the
	// single request finish untouched.
	runLivenessSubtest(t, art, save, "healthy_slow_stream", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		var expected strings.Builder
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			if idx != 1 {
				t.Errorf("unexpected build request #%d (slow stream should need no restart)", idx)
				panic(http.ErrAbortHandler)
			}
			flusher := livenessOpenStream(w)
			for i := 0; i < 5; i++ {
				chunk := fmt.Sprintf("chunk-%d ", i)
				expected.WriteString(chunk)
				_, _ = io.WriteString(w, livenessContentFrame(chunk))
				flusher.Flush()
				time.Sleep(300 * time.Millisecond)
			}
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, map[string]string{"AI_STREAM_STALL": "1s"})

		out, elapsed := h.runBuildTurn(t)
		res.ElapsedMS = elapsed.Milliseconds()
		res.SSETail = livenessTail(out, 1500)
		// 5 frames x 300ms of guaranteed sleep: the stream genuinely took
		// longer than the 1s stall knob (otherwise this test is vacuous).
		check("the stream really was slower than the stall knob",
			elapsed >= 1400*time.Millisecond, fmt.Sprintf("elapsed=%v", elapsed))
		check("slow turn completes in under 15s", elapsed < 15*time.Second, fmt.Sprintf("elapsed=%v", elapsed))

		frames := livenessParseSSE(out)
		errMsgs := livenessErrorMessages(frames)
		check("no error events (gap watchdog stayed quiet)", len(errMsgs) == 0,
			fmt.Sprintf("got %d: %s", len(errMsgs), strings.Join(errMsgs, " | ")))

		done := livenessLastDone(frames)
		res.LastDone = done
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status success", livenessDoneString(done, "status") == "success",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
		rounds, roundsOK := livenessDoneRounds(done)
		check("done rounds == 1", roundsOK && rounds == 1, fmt.Sprintf("rounds=%d present=%v", rounds, roundsOK))
		want := expected.String()
		msg := livenessDoneString(done, "message")
		check("message is the full concatenated text", msg == want,
			fmt.Sprintf("message=%q want=%q", msg, want))

		reqs, _, titles := provider.snapshot()
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.ExtraAttempts = livenessExtra(len(reqs), done)
		check("exactly 1 build request (no restart for slow-but-flowing)", len(reqs) == 1,
			fmt.Sprintf("got %d: %s", len(reqs), livenessReqIndexes(reqs)))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))
		t.Logf("healthy_slow_stream: %d build request(s), elapsed %v", len(reqs), elapsed)
	})

	// F6: headers never arrive. openStream's bounded attempts (the
	// stand-in sees exactly three requests) must each be cut short by
	// the header timeout, and the turn must end as a clean failure —
	// pre-fix this hangs until the 10-minute client timeout, per attempt.
	runLivenessSubtest(t, art, save, "header_hang", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done() // F6: never write a single header byte
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, map[string]string{"AI_STREAM_HEADER_TIMEOUT": "400ms"})

		out, elapsed := h.runBuildTurn(t)
		res.ElapsedMS = elapsed.Milliseconds()
		res.SSETail = livenessTail(out, 1500)
		check("header-hang turn fails in under 30s", elapsed < 30*time.Second,
			fmt.Sprintf("elapsed=%v", elapsed))

		frames := livenessParseSSE(out)
		errMsgs := livenessErrorMessages(frames)
		check("an error event was reported", len(errMsgs) > 0, "no error event in the SSE stream")

		done := livenessLastDone(frames)
		res.LastDone = done
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status failed", livenessDoneString(done, "status") == "failed",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))

		reqs, _, titles := provider.snapshot()
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.ExtraAttempts = livenessExtra(len(reqs), done)
		check("exactly 3 header attempts reached the stand-in", len(reqs) == 3,
			fmt.Sprintf("got %d: %s", len(reqs), livenessReqIndexes(reqs)))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))
		t.Logf("header_hang: %d build request(s), elapsed %v", len(reqs), elapsed)
	})
}
