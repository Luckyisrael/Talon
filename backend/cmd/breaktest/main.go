// Command breaktest is the backend API break-test: a live E2E driver that
// hammers the Talon stack hunting for the seams between what the routes
// promise and what they do.
//
// The failure modes are enumerated before any code exists — e2e/BREAKTEST.md
// — and asserted here under the same BT-x.y ids. The run ends with a
// scorecard at $BREAKTEST_REPORT (default e2e/reports/breaktest-latest.md).
//
// Two local stubs keep the run deterministic and free:
//
//   - OAuth stub: run-breaktest.sh points AUTH_GITHUB_* at it. Each consent
//     round mints a distinct identity, so the cross-account cases run against
//     real second and third users — the production flow, only the host differs.
//   - Model stub: answers /chat/completions. Instant by default; slow (an
//     open-ended stream with no finish_reason) while a turn is meant to be
//     stopped mid-flight. Stop/resume timing is exact and no paid gateway is
//     ever touched.
//
// Stop semantics under test: there is no /stop route — a stop is the client
// closing the SSE connection. r.Context() cancels, the agent loop returns the
// ctx error, terminalStatus maps it to "cancelled", which is resumable.
//
// Against a running stack:
//
//	go run ./cmd/breaktest -base http://127.0.0.1:8096 -stub-port 8095 -model-port 8094
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	base      = flag.String("base", "http://127.0.0.1:8096", "backend base URL")
	stubPort  = flag.Int("stub-port", 8095, "stub OAuth provider port (0 disables, logins then fail)")
	modelPort = flag.Int("model-port", 8094, "stub model gateway port")
	verbose   = flag.Bool("v", false, "print SSE events as they arrive")
)

// ---------------------------------------------------------------------------
// Case tracking. Every assertion carries its BT id from e2e/BREAKTEST.md.
// ---------------------------------------------------------------------------

type caseRun struct {
	id, name, verdict, detail string
}

var (
	muCases  sync.Mutex
	caseList []caseRun
	started  time.Time
)

// bt records one enumerated case.
func bt(id, name string, ok bool, want, got string) {
	verdict, detail := "PASS", want
	if !ok {
		verdict, detail = "FAIL", fmt.Sprintf("want %s, got %s", want, got)
	}
	muCases.Lock()
	caseList = append(caseList, caseRun{id, name, verdict, detail})
	muCases.Unlock()
	if ok {
		fmt.Printf("✓ %s %s — %s\n", id, name, want)
	} else {
		fmt.Printf("✗ %s %s — %s\n", id, name, detail)
	}
}

// btf records an unconditional failure with a formatted detail.
func btf(id, name, format string, args ...any) {
	bt(id, name, false, "", fmt.Sprintf(format, args...))
}

// bts records an honest skip.
func bts(id, name, reason string) {
	muCases.Lock()
	caseList = append(caseList, caseRun{id, name, "SKIP", reason})
	muCases.Unlock()
	fmt.Printf("− %s %s — %s\n", id, name, reason)
}

// stIn reports whether got matches any accepted status.
func stIn(got int, opts ...int) bool {
	for _, o := range opts {
		if got == o {
			return true
		}
	}
	return false
}

// d renders a response for a failure detail.
func d(status int, body string) string {
	return fmt.Sprintf("%d %s", status, trunc(body, 220))
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// HTTP plumbing. A bounded client for regular calls; SSE opens its own
// context-scoped request so aborting it is exactly a client-side Stop.
// ---------------------------------------------------------------------------

var httpc = &http.Client{Timeout: 3 * time.Minute}

type client struct {
	token string
	name  string
}

func (c *client) doH(method, path string, body any) (int, http.Header, string) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, "marshal: " + err.Error()
		}
		rdr = bytes.NewReader(b)
	}
	return c.doRawH(method, path, "application/json", rdr)
}

func (c *client) do(method, path string, body any) (int, string) {
	st, _, b := c.doH(method, path, body)
	return st, b
}

func (c *client) doRaw(method, path, contentType string, rdr io.Reader) (int, string) {
	st, _, b := c.doRawH(method, path, contentType, rdr)
	return st, b
}

func (c *client) doRawH(method, path, contentType string, rdr io.Reader) (int, http.Header, string) {
	req, err := http.NewRequest(method, *base+path, rdr)
	if err != nil {
		return 0, nil, err.Error()
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, nil, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 48<<20))
	return resp.StatusCode, resp.Header, string(raw)
}

// ---------------------------------------------------------------------------
// SSE: open, consume until an event predicate fires, and — the whole point —
// abort by cancelling the context, which is what closing the connection does.
// ---------------------------------------------------------------------------

type sseEvent struct {
	Kind string
	Data string
}

type sseConn struct {
	resp   *http.Response
	cancel context.CancelFunc
	done   chan struct{}
	ch     chan sseEvent
	errc   chan error
	once   sync.Once
	seen   []sseEvent
}

func (c *client) sse(method, path string, body any) (*sseConn, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, method, *base+path, rdr)
	if err != nil {
		cancel()
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req) // no timeout: the context drives it
	if err != nil {
		cancel()
		return nil, err
	}
	conn := &sseConn{
		resp:   resp,
		cancel: cancel,
		done:   make(chan struct{}),
		ch:     make(chan sseEvent, 256),
		errc:   make(chan error, 1),
	}
	if resp.StatusCode == http.StatusOK {
		// Only stream responses get a reader goroutine; error responses are
		// plain JSON the caller reads directly (no two readers on one body).
		go conn.readLoop()
	}
	return conn, nil
}

func (s *sseConn) readLoop() {
	br := bufio.NewReader(s.resp.Body)
	var kind string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			select {
			case s.errc <- err:
			case <-s.done:
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if kind == "" {
				continue
			}
			ev := sseEvent{Kind: kind, Data: strings.TrimSpace(strings.TrimPrefix(line, "data:"))}
			kind = ""
			select {
			case s.ch <- ev:
			case <-s.done:
				return
			}
		}
	}
}

func (s *sseConn) status() int { return s.resp.StatusCode }

// body reads the raw response — used for non-200 replies (409 JSON etc).
func (s *sseConn) body(limit int64) string {
	b, _ := io.ReadAll(io.LimitReader(s.resp.Body, limit))
	return string(b)
}

// abort is the stop under test: cancel the request context, i.e. hang up.
func (s *sseConn) abort() {
	s.once.Do(func() { close(s.done) })
	s.cancel()
	_ = s.resp.Body.Close()
}

// events consumes until until() fires or the deadline passes. Returns every
// event seen on this connection so far (cumulative), so callers can assert
// "we saw at least one event" even when the deadline hit.
func (s *sseConn) events(deadline time.Duration, until func(sseEvent) bool) []sseEvent {
	deadlineAt := time.Now().Add(deadline)
	for {
		wait := time.Until(deadlineAt)
		if wait <= 0 {
			return s.seen
		}
		select {
		case ev := <-s.ch:
			s.seen = append(s.seen, ev)
			if *verbose {
				fmt.Printf("    ← %s: %s\n", ev.Kind, trunc(ev.Data, 140))
			}
			if until != nil && until(ev) {
				return s.seen
			}
		case err := <-s.errc:
			// Re-arm so a later call reports the same terminal error, then
			// drain: the reader pushes every frame before the terminal error,
			// and select picks randomly among ready cases — without the drain
			// a done frame that already arrived can be dropped on the floor.
			s.errc <- err
			for {
				select {
				case ev := <-s.ch:
					s.seen = append(s.seen, ev)
					if *verbose {
						fmt.Printf("    ← %s: %s\n", ev.Kind, trunc(ev.Data, 140))
					}
					if until != nil && until(ev) {
						return s.seen
					}
				default:
					return s.seen
				}
			}
		case <-time.After(wait):
			return s.seen
		}
	}
}

func (s *sseConn) saw(kind string) bool {
	for _, ev := range s.seen {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}

// doneStatus pulls the status out of the terminal done frame.
func (s *sseConn) doneStatus() (string, bool) {
	for _, ev := range s.seen {
		if ev.Kind == "done" {
			var m map[string]any
			if json.Unmarshal([]byte(ev.Data), &m) == nil {
				st, _ := m["status"].(string)
				return st, true
			}
			return "", true
		}
	}
	return "", false
}

// promptIDFromEvents reads the prompt_id a phase frame carries (every phase
// frame has carried it since H2 addressing), else 0.
func (s *sseConn) promptIDFromEvents() int64 {
	for _, ev := range s.seen {
		if ev.Kind != "phase" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(ev.Data), &m) == nil {
			if v, ok := m["prompt_id"].(float64); ok {
				return int64(v)
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Metering. The server's promptLimiter is a token bucket: 5 calls per user
// per minute, window reset by the first call after it expires. reserve(n)
// mirrors it locally so the run never trips 429 — except BT-7.13, which is
// the deliberate probe and bypasses the helper.
// ---------------------------------------------------------------------------

const (
	meterRate   = 5
	meterWindow = 61 * time.Second // one second of slack past the server minute
)

type meter struct {
	mu    sync.Mutex
	start time.Time
	count int
}

func (m *meter) reserve(n int) {
	for {
		m.mu.Lock()
		if m.start.IsZero() || time.Since(m.start) > meterWindow {
			m.start = time.Now()
			m.count = 0
		}
		if m.count+n <= meterRate {
			m.count += n
			m.mu.Unlock()
			return
		}
		wait := m.start.Add(meterWindow).Sub(time.Now())
		m.mu.Unlock()
		if wait < 250*time.Millisecond {
			wait = 250 * time.Millisecond
		}
		fmt.Printf("    … prompt budget spent, waiting %s for the limiter window\n", wait.Truncate(time.Second))
		time.Sleep(wait)
	}
}

// ---------------------------------------------------------------------------
// Stub OAuth provider. One distinct identity per consent round: /authorize
// mints a fresh code, /token exchanges it for a token, /profile answers with
// a distinct numeric id (the shape cmd/e2e's proven stub uses).
// ---------------------------------------------------------------------------

func startOAuthStub(addr string) error {
	mux := http.NewServeMux()
	var mu sync.Mutex
	byCode := map[string]int{}
	byToken := map[string]int{}
	ord := 0

	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		mu.Lock()
		ord++
		code := fmt.Sprintf("bt-consent-%d", ord)
		byCode[code] = ord
		mu.Unlock()
		v := url.Values{}
		v.Set("code", code)
		v.Set("state", q.Get("state"))
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+v.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.PostFormValue("code")
		mu.Lock()
		n, ok := byCode[code]
		mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unknown code"}`, http.StatusBadRequest)
			return
		}
		tok := fmt.Sprintf("bt-token-%d", n)
		mu.Lock()
		byToken[tok] = n
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":%q}`, tok))
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		n, ok := byToken[tok]
		mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unknown token"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"id":%d,"login":"breaktest-%d","name":"Breaktest %d","email":"bt-%d@breaktest.invalid","avatar_url":""}`,
			910000+n, n, n, n))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go http.Serve(ln, mux)
	return nil
}

// ---------------------------------------------------------------------------
// Stub model gateway. Suffix-matched on /chat/completions so the client can
// be aimed at either / or /v1. Slow mode is the lever the stop tests pull.
// ---------------------------------------------------------------------------

type modelStub struct {
	slow  atomic.Bool
	count atomic.Int64
}

func startModelStub(addr string) (*modelStub, error) {
	stub := &modelStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", stub.chat)
	mux.HandleFunc("/v1/chat/completions", stub.chat)
	mux.HandleFunc("/embeddings", stub.embeddings)
	mux.HandleFunc("/v1/embeddings", stub.embeddings)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"breaktest stub: no route `+r.URL.Path+`"}}`)
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	go http.Serve(ln, mux)
	return stub, nil
}

func (m *modelStub) embeddings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
}

func (m *modelStub) chat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	m.count.Add(1)
	flusher, _ := w.(http.Flusher)

	streaming := bytes.Contains(body, []byte(`"stream":true`)) || bytes.Contains(body, []byte(`"stream": true`))
	if !streaming {
		// Buffered path (non-streaming Create, title upgrades): answer whole.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant",`+
			`"content":"stub-model reply: turn complete."},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":120,"completion_tokens":30}}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}

	if !m.slow.Load() {
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"stub-model reply: turn complete."}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":120,"completion_tokens":30}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// Slow mode: stream until the client hangs up (that hang-up is the stop
	// under test) or a five-minute cap. No finish_reason on purpose — a turn
	// that reaches the cap has already blown its own deadline.
	for i := 0; ; i++ {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		fmt.Fprintf(w, `data: {"choices":[{"index":0,"delta":{"content":"...working %d "}}]}`+"\n\n", i)
		if flusher != nil {
			flusher.Flush()
		}
		if i >= 3000 { // 5 min
			return
		}
	}
}

// ---------------------------------------------------------------------------
// The run's shared state.
// ---------------------------------------------------------------------------

type world struct {
	alice, bob, carol *client
	stub              *modelStub
	aliceM, bobM      meter

	alpha, filesP, beta, bigP int64 // created projects
	bobConv                   int64
}

var w world

// workspacePath mirrors workspace.Manager.Path: WORKSPACE_DIR/<project id>.
func workspacePath(id int64) string {
	return filepath.Join(os.Getenv("WORKSPACE_DIR"), strconv.FormatInt(id, 10))
}

// ---------------------------------------------------------------------------
// Types the driver decodes.
// ---------------------------------------------------------------------------

type project struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	PlanJSON string `json:"plan_json"`
}

type promptIdx struct {
	ID     int64  `json:"id"`
	Mode   string `json:"mode"`
	Status string `json:"status"`
}

type promptRow struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Round  int    `json:"round"`
}

type conversationResp struct {
	Turns []struct {
		PromptID int64  `json:"prompt_id"`
		Role     string `json:"role"`
	} `json:"turns"`
	Latest *struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"latest"`
}

// ---------------------------------------------------------------------------
// Login: the real OAuth flow, end to end, against the local stub. Same code
// path a browser runs — only the host differs.
// ---------------------------------------------------------------------------

func login(label string) (*client, int64, error) {
	noRedirect := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	stubAddr := fmt.Sprintf("127.0.0.1:%d", *stubPort)

	resp, err := noRedirect.Get(*base + "/v1/auth/github/start?redirect=/auth/callback")
	if err != nil {
		return nil, 0, fmt.Errorf("start: %w", err)
	}
	consent := resp.Header.Get("Location")
	resp.Body.Close()
	if !strings.Contains(consent, stubAddr) {
		return nil, 0, fmt.Errorf("start redirected to %q — backend not aimed at the stub (check AUTH_GITHUB_* endpoints)", trunc(consent, 120))
	}

	resp2, err := noRedirect.Get(consent)
	if err != nil {
		return nil, 0, fmt.Errorf("consent: %w", err)
	}
	cb := resp2.Header.Get("Location")
	resp2.Body.Close()
	if !strings.HasPrefix(cb, *base) {
		return nil, 0, fmt.Errorf("consent redirected to %q, want the backend callback", trunc(cb, 120))
	}

	resp3, err := noRedirect.Get(cb)
	if err != nil {
		return nil, 0, fmt.Errorf("callback: %w", err)
	}
	final := resp3.Header.Get("Location")
	resp3.Body.Close()
	u, err := url.Parse(final)
	if err != nil || u.Query().Get("code") == "" {
		return nil, 0, fmt.Errorf("callback landed on %q without a code", trunc(final, 160))
	}

	tok := &client{name: label}
	st, body := tok.do("POST", "/v1/auth/exchange", map[string]string{"code": u.Query().Get("code")})
	if st != 200 {
		return nil, 0, fmt.Errorf("exchange: %s", d(st, body))
	}
	var ex struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &ex); err != nil || ex.Token == "" {
		return nil, 0, fmt.Errorf("exchange body: %s", trunc(body, 200))
	}
	tok.token = ex.Token

	st, body = tok.do("GET", "/v1/me", nil)
	if st != 200 {
		return nil, 0, fmt.Errorf("/v1/me: %s", d(st, body))
	}
	var me struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &me); err != nil || me.ID == 0 {
		return nil, 0, fmt.Errorf("/v1/me body: %s", trunc(body, 200))
	}
	return tok, me.ID, nil
}

// ---------------------------------------------------------------------------
// Polling helpers.
// ---------------------------------------------------------------------------

// pollTerminal reads the prompt until its status leaves the running state.
// Returns "gone" on 404 and "<status> (timeout)" if it never settled.
func pollTerminal(c *client, id int64, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	last := "unknown"
	for time.Now().Before(deadline) {
		st, body := c.do("GET", fmt.Sprintf("/v1/prompts/%d", id), nil)
		if st == 404 {
			return "gone"
		}
		if st == 200 {
			var p promptRow
			if json.Unmarshal([]byte(body), &p) == nil {
				if p.Status != "" && p.Status != "running" {
					return p.Status
				}
				last = p.Status
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return last + " (timeout)"
}

// latestPrompt reads the turn index; the head row is the newest prompt.
func latestPrompt(c *client, projectID int64) (promptIdx, bool) {
	st, body := c.do("GET", fmt.Sprintf("/v1/projects/%d/prompts", projectID), nil)
	var list []promptIdx
	if st != 200 || json.Unmarshal([]byte(body), &list) != nil || len(list) == 0 {
		return promptIdx{}, false
	}
	return list[0], true
}

func getConversation(c *client, projectID int64, extraQuery string) (int, string, conversationResp) {
	st, body := c.do("GET", fmt.Sprintf("/v1/projects/%d/conversation%s", projectID, extraQuery), nil)
	var conv conversationResp
	_ = json.Unmarshal([]byte(body), &conv)
	return st, body, conv
}

// runFastTurn drives one build turn to completion over SSE (stub in fast
// mode) and returns the prompt id, or 0 with a failure detail.
func runFastTurn(c *client, m *meter, projectID int64, promptText string) (int64, string) {
	m.reserve(1)
	conn, err := c.sse("POST", "/v1/prompts/stream", map[string]any{
		"project_id": projectID,
		"mode":       "build",
		"prompt":     promptText,
	})
	if err != nil {
		return 0, "open: " + err.Error()
	}
	defer conn.abort()
	if conn.status() != 200 {
		return 0, fmt.Sprintf("stream status %d: %s", conn.status(), trunc(conn.body(500), 300))
	}
	conn.events(2*time.Minute, func(ev sseEvent) bool { return ev.Kind == "done" })
	if st, ok := conn.doneStatus(); !ok || st != "success" {
		st2 := "none"
		if ok {
			st2 = st
		}
		return 0, fmt.Sprintf("done status %q (%d events)", st2, len(conn.seen))
	}
	pid := conn.promptIDFromEvents()
	if pid == 0 {
		if idx, ok := latestPrompt(c, projectID); ok {
			pid = idx.ID
		}
	}
	if pid == 0 {
		return 0, "could not determine the prompt id"
	}
	if status := pollTerminal(c, pid, 30*time.Second); status != "completed" {
		return 0, "prompt status " + status
	}
	return pid, ""
}

// ---------------------------------------------------------------------------
// Stages.
// ---------------------------------------------------------------------------

// stageMonitor covers BT-1.1..1.4: liveness public, telemetry gated.
func stageMonitor() {
	pub := &client{name: "anonymous"}
	st, body := pub.do("GET", "/healthz", nil)
	bt("BT-1.1", "healthz unauthenticated", stIn(st, 200), "200", d(st, body))
	st, body = pub.do("GET", "/readyz", nil)
	bt("BT-1.2", "readyz unauthenticated", stIn(st, 200), "200", d(st, body))
	st, body = pub.do("GET", "/v1/metrics", nil)
	bt("BT-1.3", "metrics requires auth", stIn(st, 401), "401", d(st, body))
	st, body = pub.do("GET", "/v1/errors", nil)
	bt("BT-1.4", "errors requires auth", stIn(st, 401), "401", d(st, body))
}

// stageAuth covers BT-2.1..2.4: three real flows, three distinct accounts.
func stageAuth() bool {
	var idA, idB, idC int64
	var err error
	w.alice, idA, err = login("alice")
	bt("BT-2.1", "alice OAuth flow", err == nil, "JWT + /v1/me", fmt.Sprintf("err=%v id=%d", err, idA))
	if err != nil {
		return false
	}
	w.bob, idB, err = login("bob")
	bt("BT-2.2", "bob is a distinct account", err == nil && idB != 0 && idB != idA,
		"second distinct id", fmt.Sprintf("err=%v id=%d (alice %d)", err, idB, idA))
	if err != nil {
		return false
	}
	w.carol, idC, err = login("carol")
	bt("BT-2.3", "carol is a distinct account", err == nil && idC != 0 && idC != idA && idC != idB,
		"third distinct id", fmt.Sprintf("err=%v id=%d", err, idC))
	if err != nil {
		return false
	}

	pub := &client{name: "anonymous"}
	st, body := pub.do("POST", "/v1/auth/exchange", map[string]string{"code": "bt-bogus-code"})
	bt("BT-2.4", "bogus exchange code", st >= 400 && st < 500, "4xx", d(st, body))
	return true
}

// stageMonitorAuthed covers BT-1.5..1.9.
func stageMonitorAuthed() {
	A := w.alice
	st, body := A.do("GET", "/v1/metrics", nil)
	bt("BT-1.5", "metrics with JWT", st == 200 && len(body) > 0, "200 non-empty", d(st, body))
	st, body = A.do("GET", "/v1/errors", nil)
	bt("BT-1.6", "errors with JWT", stIn(st, 200), "200", d(st, body))

	probe := func(header string) (int, string) {
		req, _ := http.NewRequest("GET", *base+"/v1/projects", nil)
		req.Header.Set("Authorization", header)
		resp, err := httpc.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return resp.StatusCode, string(raw)
	}
	st, body = probe("Bearer not-a-real-jwt")
	bt("BT-1.7", "garbage bearer token", stIn(st, 401), "401", d(st, body))
	st, body = probe("Basic dXNlcjpwYXNz")
	bt("BT-1.8", "wrong auth scheme", stIn(st, 401), "401", d(st, body))

	pub := &client{name: "anonymous"}
	st, body = pub.do("GET", "/definitely/not/a/route", nil)
	noStack := !strings.Contains(body, "goroutine ") && !strings.Contains(body, ".go:")
	bt("BT-1.9", "unknown route", stIn(st, 404) && noStack, "404 without a stack trace", d(st, body))
}

// stageProject covers BT-3.1..3.11.
func stageProject() {
	A, B := w.alice, w.bob

	// BT-3.1 create + workspace on disk.
	st, body := A.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Alpha"})
	var alpha project
	_ = json.Unmarshal([]byte(body), &alpha)
	w.alpha = alpha.ID
	wsOK := false
	if alpha.ID > 0 {
		if fi, err := os.Stat(workspacePath(alpha.ID)); err == nil && fi.IsDir() {
			wsOK = true
		}
	}
	bt("BT-3.1", "create project + workspace", st == 201 && alpha.ID > 0 && wsOK,
		"201, id, workspace dir", fmt.Sprintf("%s (workspace on disk: %v)", d(st, body), wsOK))

	// BT-3.2 name boundaries.
	stEmpty, _ := A.do("POST", "/v1/projects", map[string]string{"name": "   "})
	stLong, _ := A.do("POST", "/v1/projects", map[string]string{"name": strings.Repeat("n", 121)})
	bt("BT-3.2", "empty/121-char name rejected", stEmpty == 400 && stLong == 400, "400 and 400",
		fmt.Sprintf("empty=%d long=%d", stEmpty, stLong))

	// BT-3.3 120 chars is fine.
	st, body = A.do("POST", "/v1/projects", map[string]string{"name": strings.Repeat("n", 120)})
	bt("BT-3.3", "120-char name accepted", st == 201, "201", d(st, body))

	// BT-3.4 malformed JSON.
	st, body = A.doRaw("POST", "/v1/projects", "application/json", strings.NewReader(`{"name": `))
	bt("BT-3.4", "malformed JSON", stIn(st, 400), "400", d(st, body))

	// BT-3.5 duplicate name → unique slug.
	st, body = A.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Alpha"})
	var dup project
	_ = json.Unmarshal([]byte(body), &dup)
	bt("BT-3.5", "duplicate name gets a distinct slug",
		st == 201 && dup.Slug != "" && dup.Slug != alpha.Slug, "201, slug differs",
		fmt.Sprintf("%s (alpha slug %q, dup slug %q)", d(st, body), alpha.Slug, dup.Slug))

	// BT-3.6 traversal-shaped name cannot escape the workspace root.
	st, body = A.do("POST", "/v1/projects", map[string]string{"name": "../../etc/passwd"})
	var trav project
	_ = json.Unmarshal([]byte(body), &trav)
	esc := false
	if trav.ID > 0 {
		dir, _ := filepath.Abs(workspacePath(trav.ID))
		root, _ := filepath.Abs(os.Getenv("WORKSPACE_DIR"))
		rel, err := filepath.Rel(root, dir)
		esc = err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	bt("BT-3.6", "traversal name stays inside the root",
		stIn(st, 201, 400) && !esc, "201/400, no escape",
		fmt.Sprintf("%s (escaped: %v)", d(st, body), esc))

	// BT-3.7 malformed / nonexistent ids: 400s, a 404, never a 500.
	var bad []string
	for _, id := range []string{"0", "-1", "abc", "99999999999999999999"} {
		st, body := A.do("GET", "/v1/projects/"+id, nil)
		if st != 400 {
			bad = append(bad, fmt.Sprintf("id=%s → %s", id, d(st, body)))
		}
	}
	st, body = A.do("GET", "/v1/projects/9999999999", nil)
	if st != 404 {
		bad = append(bad, fmt.Sprintf("missing id → %s", d(st, body)))
	}
	bt("BT-3.7", "malformed ids are 400/404, never 500", len(bad) == 0, "400×4 + 404",
		strings.Join(bad, "; "))

	// BT-3.8 bob probes alice's project across every verb: 404 each way.
	aid := strconv.FormatInt(w.alpha, 10)
	bad = nil
	probes := [][2]string{
		{"GET", "/v1/projects/" + aid},
		{"PUT", "/v1/projects/" + aid},
		{"DELETE", "/v1/projects/" + aid},
		{"GET", "/v1/projects/" + aid + "/files"},
		{"PUT", "/v1/projects/" + aid + "/files"},
		{"DELETE", "/v1/projects/" + aid + "/files?path=package.json"},
		{"GET", "/v1/projects/" + aid + "/prompts"},
	}
	for _, p := range probes {
		var reqBody any
		if p[0] == "PUT" {
			if strings.HasSuffix(p[1], "/files") {
				reqBody = map[string]any{"files": []any{}}
			} else {
				reqBody = map[string]string{"plan_json": "{}"}
			}
		}
		st, body := B.do(p[0], p[1], reqBody)
		if st != 404 {
			bad = append(bad, fmt.Sprintf("%s %s → %s", p[0], p[1], d(st, body)))
		}
	}
	st, body = A.do("GET", "/v1/projects/"+aid, nil)
	if st != 200 {
		bad = append(bad, fmt.Sprintf("alice can no longer read her project: %s", d(st, body)))
	}
	bt("BT-3.8", "cross-account project probes", len(bad) == 0, "404 ×7, project untouched",
		strings.Join(bad, "; "))

	// BT-3.9 plan validation and persistence.
	stBad, _ := A.do("PUT", "/v1/projects/"+aid, map[string]string{"plan_json": "{nope"})
	stSaved, _ := A.do("PUT", "/v1/projects/"+aid,
		map[string]string{"plan_json": `{"screens":[{"id":"s1","name":"Home"}]}`})
	st, body = A.do("GET", "/v1/projects/"+aid, nil)
	var readBack project
	_ = json.Unmarshal([]byte(body), &readBack)
	reflected := strings.Contains(readBack.PlanJSON, "s1")
	bt("BT-3.9", "plan put validation + persistence", stBad == 400 && stSaved == 200 && reflected,
		"400 then 200, GET reflects plan",
		fmt.Sprintf("bad=%d saved=%d reflected=%v", stBad, stSaved, reflected))

	// BT-3.10 a ~16 MB body must not wedge or 500 the route. (No global
	// MaxBytesReader exists outside the billing webhook — recorded honestly.)
	pad := strings.Repeat("x", 16<<20)
	payload := `{"name":"Breaktest Bigbody","pad":"` + pad + `"}`
	t0 := time.Now()
	st, body = A.doRaw("POST", "/v1/projects", "application/json", strings.NewReader(payload))
	elapsed := time.Since(t0)
	var big project
	_ = json.Unmarshal([]byte(body), &big)
	w.bigP = big.ID
	bt("BT-3.10", "16MB body handled", stIn(st, 200, 201) && elapsed < 20*time.Second,
		"2xx within 20s, no 500",
		fmt.Sprintf("%s in %s", d(st, body), elapsed.Truncate(10*time.Millisecond)))

	// BT-3.11 bob cannot touch alice's conversation collection.
	stGet, _ := B.do("GET", "/v1/projects/"+aid+"/conversations", nil)
	stPost, _ := B.do("POST", "/v1/projects/"+aid+"/conversations", map[string]string{"title": "stolen"})
	bt("BT-3.11", "cross-account conversations", stGet == 404 && stPost == 404,
		"404 both", fmt.Sprintf("GET=%d POST=%d", stGet, stPost))
}

// stageFiles covers BT-4.1..4.9 on a dedicated project. BT-4.8 runs last:
// if the root-delete bug is live, it wipes this workspace.
func stageFiles() {
	A := w.alice
	st, body := A.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Files"})
	var fp project
	_ = json.Unmarshal([]byte(body), &fp)
	w.filesP = fp.ID
	if st != 201 || fp.ID == 0 {
		btf("BT-4.1", "files project", "could not create the files project: %s", d(st, body))
		return
	}
	p := strconv.FormatInt(fp.ID, 10)

	// BT-4.1 happy path round trip.
	stWrite, _ := A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "breaktest/alpha.txt", "content": "hello breaktest"}},
	})
	stTree, tree := A.do("GET", "/v1/projects/"+p+"/files", nil)
	present := strings.Contains(tree, "breaktest/alpha.txt")
	stDel, _ := A.do("DELETE", "/v1/projects/"+p+"/files?path="+url.QueryEscape("breaktest/alpha.txt"), nil)
	stDel2, _ := A.do("DELETE", "/v1/projects/"+p+"/files?path="+url.QueryEscape("breaktest/alpha.txt"), nil)
	bt("BT-4.1", "file round trip", stWrite == 200 && stTree == 200 && present && stDel == 200 && stDel2 == 404,
		"200, listed, 200, then 404",
		fmt.Sprintf("write=%d tree=%d present=%v del=%d del2=%d", stWrite, stTree, present, stDel, stDel2))

	// BT-4.2 traversal write.
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "../escape.txt", "content": "escaped"}},
	})
	_, errUp := os.Stat(filepath.Join(filepath.Dir(workspacePath(fp.ID)), "escape.txt"))
	_, errRoot := os.Stat(filepath.Join(os.Getenv("WORKSPACE_DIR"), "escape.txt"))
	bt("BT-4.2", "traversal write rejected", st == 400 && os.IsNotExist(errUp) && os.IsNotExist(errRoot),
		"400, no file outside",
		fmt.Sprintf("status %d (parent has it: %v, root has it: %v)", st, !os.IsNotExist(errUp), !os.IsNotExist(errRoot)))

	// BT-4.3 absolute path write.
	_ = os.Remove("/tmp/bt-escape.txt")
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "/tmp/bt-escape.txt", "content": "escaped"}},
	})
	_, errAbs := os.Stat("/tmp/bt-escape.txt")
	bt("BT-4.3", "absolute path write rejected", st == 400 && os.IsNotExist(errAbs),
		"400, file absent", fmt.Sprintf("status %d (exists: %v)", st, !os.IsNotExist(errAbs)))

	// BT-4.4 node_modules (a symlink into the shared template).
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "node_modules/evil.js", "content": "x"}},
	})
	bt("BT-4.4", "node_modules write rejected", st == 400, "400", fmt.Sprintf("status %d", st))

	// BT-4.5 NUL byte in the path.
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "app/x\x00.js", "content": "x"}},
	})
	bt("BT-4.5", "NUL-byte path rejected", st == 400, "400", fmt.Sprintf("status %d", st))

	// BT-4.6 oversize file.
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{{"path": "breaktest/big.txt", "content": strings.Repeat("a", 3<<20)}},
	})
	_, tree = A.do("GET", "/v1/projects/"+p+"/files", nil)
	bigAbsent := !strings.Contains(tree, "breaktest/big.txt")
	bt("BT-4.6", "3MiB file rejected", st == 413 && bigAbsent, "413, nothing written",
		fmt.Sprintf("status %d (absent: %v)", st, bigAbsent))

	// BT-4.7 batch atomicity: one bad path must write nothing.
	st, _ = A.do("PUT", "/v1/projects/"+p+"/files", map[string]any{
		"files": []map[string]string{
			{"path": "breaktest/good.txt", "content": "should not survive"},
			{"path": "../bad.txt", "content": "nope"},
		},
	})
	_, tree = A.do("GET", "/v1/projects/"+p+"/files", nil)
	goodAbsent := !strings.Contains(tree, "breaktest/good.txt")
	_, errBad := os.Stat(filepath.Join(filepath.Dir(workspacePath(fp.ID)), "bad.txt"))
	bt("BT-4.7", "batch rejected atomically", st == 400 && goodAbsent && os.IsNotExist(errBad),
		"400, nothing written",
		fmt.Sprintf("status %d (good absent: %v, bad absent: %v)", st, goodAbsent, os.IsNotExist(errBad)))

	// BT-4.8 THE workspace-wipe probe: root-shaped deletes must be refused.
	var bad []string
	var delStatuses []string
	for _, rootPath := range []string{".", "./", "app/.."} {
		st, body := A.do("DELETE", "/v1/projects/"+p+"/files?path="+url.QueryEscape(rootPath), nil)
		delStatuses = append(delStatuses, fmt.Sprintf("%s→%d", rootPath, st))
		if st != 400 {
			bad = append(bad, fmt.Sprintf("path=%s → %s", rootPath, d(st, body)))
		}
	}
	wiped := false
	if _, err := os.Stat(workspacePath(fp.ID)); os.IsNotExist(err) {
		wiped = true
	}
	if wiped {
		bad = append(bad, "WORKSPACE ROOT WAS DELETED")
	}
	bt("BT-4.8", "root-shaped delete refused", len(bad) == 0, "400 ×3, workspace intact",
		fmt.Sprintf("%s (wiped: %v)", strings.Join(delStatuses, " "), wiped))

	// BT-4.9 skipDirs guard.
	st, _ = A.do("DELETE", "/v1/projects/"+p+"/files?path="+url.QueryEscape(".git"), nil)
	bt("BT-4.9", ".git delete refused", st == 400, "400", fmt.Sprintf("status %d", st))
}

// stageSetup covers BT-5.1..5.4: bob gets a used project (thread captured
// for the isolation cases), alice gets a completed turn so her history is
// non-empty when BT-6.10 checks it survives.
func stageSetup() {
	w.stub.slow.Store(false)

	// BT-5.1
	st, body := w.bob.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Beta"})
	var beta project
	_ = json.Unmarshal([]byte(body), &beta)
	w.beta = beta.ID
	bt("BT-5.1", "bob creates his project", st == 201 && beta.ID > 0, "201", d(st, body))
	if beta.ID == 0 {
		bts("BT-5.2", "bob's fast turn", "no bob project")
		bts("BT-5.3", "bob's conversation id", "no bob project")
		bts("BT-5.4", "alice's marker turn", "no bob project")
		return
	}

	// BT-5.2
	pid, detail := runFastTurn(w.bob, &w.bobM, beta.ID, "bob-marker-7c3f: make a tiny greeting screen")
	ok := pid != 0
	got := detail
	if ok {
		got = fmt.Sprintf("prompt %d", pid)
	}
	bt("BT-5.2", "bob's build turn completes", ok, "done success + completed", got)
	if !ok {
		bts("BT-5.3", "bob's conversation id", "turn failed: "+detail)
		bts("BT-5.4", "alice's marker turn", "bob's turn failed")
		return
	}

	// BT-5.3
	var convs []struct {
		ID int64 `json:"id"`
	}
	st, body = w.bob.do("GET", fmt.Sprintf("/v1/projects/%d/conversations", beta.ID), nil)
	_ = json.Unmarshal([]byte(body), &convs)
	if st == 200 && len(convs) > 0 {
		w.bobConv = convs[0].ID
	}
	bt("BT-5.3", "bob's conversation captured", w.bobConv != 0, "conversation id ≥ 1",
		fmt.Sprintf("%s (id %d)", d(st, body), w.bobConv))

	// BT-5.4 alice's marker turn on alpha.
	pidA, detailA := runFastTurn(w.alice, &w.aliceM, w.alpha, "alice-marker-a91d: a one-liner note")
	okA := pidA != 0
	gotA := detailA
	if okA {
		gotA = fmt.Sprintf("prompt %d", pidA)
	}
	bt("BT-5.4", "alice's marker turn completes", okA, "completed", gotA)
}

// stageIsolation covers BT-6.1..6.10.
func stageIsolation() {
	A, B := w.alice, w.bob
	if w.alpha == 0 || w.beta == 0 || w.bobConv == 0 {
		bts("BT-6.1", "foreign conversation read", "missing project/conversation setup")
		return
	}

	// BT-6.1 alice reads bob's thread through her own project id.
	st, body, _ := getConversation(A, w.alpha, fmt.Sprintf("?conversation_id=%d", w.bobConv))
	leak := strings.Contains(body, "bob-marker-7c3f")
	bt("BT-6.1", "foreign conversation not readable", !leak, "no foreign turns",
		fmt.Sprintf("%s (foreign marker visible: %v)", d(st, body), leak))

	// BT-6.2 nonexistent thread.
	st, body, _ = getConversation(A, w.alpha, "?conversation_id=99999999999")
	bt("BT-6.2", "nonexistent conversation id", stIn(st, 200, 400, 404), "200/4xx", d(st, body))

	// BT-6.3 alice posts a turn naming bob's thread. Metered.
	w.aliceM.reserve(1)
	st, body = A.do("POST", "/v1/prompts", map[string]any{
		"project_id":      w.alpha,
		"mode":            "build",
		"prompt":          "cross-tenant-write-probe: reply with hi",
		"conversation_id": w.bobConv,
	})
	accepted := stIn(st, 200, 201)
	bt("BT-6.3", "foreign conversation write rejected", !accepted, "4xx rejected", d(st, body))

	// BT-6.4 bob re-reads his own thread: no probe text may be there.
	stB, bodyB, _ := getConversation(B, w.beta, fmt.Sprintf("?conversation_id=%d", w.bobConv))
	polluted := strings.Contains(bodyB, "cross-tenant-write-probe")
	bt("BT-6.4", "victim thread unpolluted", !polluted, "no probe text",
		fmt.Sprintf("%s (polluted: %v)", d(stB, bodyB), polluted))

	// BT-6.5 limit edges: 200 and bounded, never a 500.
	var bad []string
	for _, q := range []string{"-1", "0", "abc"} {
		st, body, conv := getConversation(A, w.alpha, "?limit="+q)
		if st != 200 || len(conv.Turns) > 500 {
			bad = append(bad, fmt.Sprintf("limit=%s → %s (%d turns)", q, d(st, body), len(conv.Turns)))
		}
	}
	bt("BT-6.5", "conversation limit edges", len(bad) == 0, "200, bounded", strings.Join(bad, "; "))

	// BT-6.6 before_seq edges.
	bad = nil
	for _, q := range []string{"0", "-3", "abc"} {
		st, body := A.do("GET", fmt.Sprintf("/v1/projects/%d/conversation?before_seq=%s", w.alpha, q), nil)
		if st != 400 {
			bad = append(bad, fmt.Sprintf("before_seq=%s → %s", q, d(st, body)))
		}
	}
	st, body = A.do("GET", fmt.Sprintf("/v1/projects/%d/conversation?before_seq=1", w.alpha), nil)
	if st != 200 {
		bad = append(bad, fmt.Sprintf("before_seq=1 → %s", d(st, body)))
	}
	bt("BT-6.6", "before_seq validation", len(bad) == 0, "400 ×3, valid → 200", strings.Join(bad, "; "))

	// BT-6.7 search edges.
	bad = nil
	if st, body := A.do("GET", fmt.Sprintf("/v1/projects/%d/conversation/search?q=a", w.alpha), nil); st != 400 {
		bad = append(bad, "q=1char → "+d(st, body))
	}
	if st, body := A.do("GET", fmt.Sprintf("/v1/projects/%d/conversation/search?q=%s", w.alpha, strings.Repeat("z", 201)), nil); st != 400 {
		bad = append(bad, "q=201char → "+d(st, body))
	}
	for _, q := range []string{"100%", `" OR 1=1 --`} {
		st, body := A.do("GET", fmt.Sprintf("/v1/projects/%d/conversation/search?q=%s", w.alpha, url.QueryEscape(q)), nil)
		if st != 200 {
			bad = append(bad, fmt.Sprintf("q=%q → %s", q, d(st, body)))
		}
	}
	bt("BT-6.7", "search validation + metachars", len(bad) == 0, "400, 400, 200, 200",
		strings.Join(bad, "; "))

	// BT-6.8 prompt index paging edges.
	bad = nil
	st, body = A.do("GET", fmt.Sprintf("/v1/projects/%d/prompts?limit=100000", w.alpha), nil)
	var list []promptIdx
	_ = json.Unmarshal([]byte(body), &list)
	if st != 200 || len(list) > 100 {
		bad = append(bad, fmt.Sprintf("limit=100000 → %s (%d rows)", d(st, body), len(list)))
	}
	st, body = A.do("GET", fmt.Sprintf("/v1/projects/%d/prompts?limit=0", w.alpha), nil)
	if st != 200 {
		bad = append(bad, "limit=0 → "+d(st, body))
	}
	for _, q := range []string{"abc", "-1"} {
		st, body := A.do("GET", fmt.Sprintf("/v1/projects/%d/prompts?before=%s", w.alpha, q), nil)
		if st != 400 {
			bad = append(bad, fmt.Sprintf("before=%s → %s", q, d(st, body)))
		}
	}
	bt("BT-6.8", "prompt index edges", len(bad) == 0, "clamped rows, 400 cursors", strings.Join(bad, "; "))

	// BT-6.9 bob reads alice's subresources: 404 everywhere.
	bad = nil
	aid := strconv.FormatInt(w.alpha, 10)
	for _, path := range []string{
		"/conversation", "/prompts", "/memory", "/audit", "/checkpoints", "/feedback", "/files",
	} {
		st, body := B.do("GET", "/v1/projects/"+aid+path, nil)
		if st != 404 {
			bad = append(bad, fmt.Sprintf("GET %s → %s", path, d(st, body)))
		}
	}
	bt("BT-6.9", "cross-account subresource reads", len(bad) == 0, "404 ×7", strings.Join(bad, "; "))

	// BT-6.10 bob cannot clear alice's thread; her turns survive.
	_, bodyBefore, _ := getConversation(A, w.alpha, "")
	st, body = B.do("DELETE", "/v1/projects/"+aid+"/conversation", nil)
	stAfter, bodyAfter, _ := getConversation(A, w.alpha, "")
	stillHasMarker := strings.Contains(bodyAfter, "alice-marker-a91d")
	bt("BT-6.10", "cross-account clear rejected", st == 404 && stAfter == 200 && stillHasMarker,
		"404, turns survive",
		fmt.Sprintf("delete=%s (turns %d→%d, marker kept=%v)", d(st, body),
			len(bodyBefore), len(bodyAfter), stillHasMarker))
}

// stageTurns covers BT-7.1..7.14: stop a live turn over SSE, resume it,
// race the resume, and probe the metered guards.
func stageTurns() {
	A := w.alice
	if w.alpha == 0 {
		bts("BT-7.1", "slow turn opens", "no alice project")
		return
	}

	// BT-7.1 open a slow turn: first event, running prompt id, model live.
	w.stub.slow.Store(true)
	w.aliceM.reserve(1)
	conn, err := A.sse("POST", "/v1/prompts/stream", map[string]any{
		"project_id": w.alpha,
		"mode":       "build",
		"prompt":     "breaktest slow turn one: keep working for a while",
	})
	if err != nil {
		btf("BT-7.1", "slow turn opens", "open stream: %v", err)
		return
	}
	defer conn.abort()
	if conn.status() != 200 {
		btf("BT-7.1", "slow turn opens", "stream status %d: %s", conn.status(), trunc(conn.body(500), 300))
		return
	}
	conn.events(30*time.Second, func(ev sseEvent) bool { return ev.Kind == "text.delta" })
	bt("BT-7.1", "slow turn streams", len(conn.seen) > 0, "≥1 SSE event within 30s",
		fmt.Sprintf("%d events (text.delta: %v)", len(conn.seen), conn.saw("text.delta")))

	pid := conn.promptIDFromEvents()
	if pid == 0 {
		if idx, ok := latestPrompt(A, w.alpha); ok {
			pid = idx.ID
		}
	}
	running := false
	if pid > 0 {
		if idx, ok := latestPrompt(A, w.alpha); ok && idx.ID == pid && idx.Status == "running" {
			running = true
		}
	}
	bt("BT-7.1b", "running prompt id captured", pid > 0 && running, "prompt id + status running",
		fmt.Sprintf("pid=%d running=%v", pid, running))
	if pid == 0 {
		return
	}

	// Make sure the agent loop is streaming (its handle exists) before steering.
	conn.events(20*time.Second, func(ev sseEvent) bool { return ev.Kind == "text.delta" })

	// BT-7.2 steer mid-turn.
	st, body := A.do("POST", fmt.Sprintf("/v1/prompts/%d/steer", pid), map[string]string{"message": "keep going"})
	bt("BT-7.2", "steer while running", st == 202, "202", d(st, body))

	// BT-7.3 discard while running: refused.
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/discard", pid), nil)
	bt("BT-7.3", "discard while running", st == 409, "409", d(st, body))

	// BT-7.4 resume while running: refused (metered).
	w.aliceM.reserve(1)
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/resume", pid), nil)
	bt("BT-7.4", "resume while running", st == 409, "409", d(st, body))

	// BT-7.5 THE stop: hang up mid-turn; the turn must land on cancelled.
	conn.abort()
	status := pollTerminal(A, pid, 45*time.Second)
	bt("BT-7.5", "SSE abort lands on cancelled", status == "cancelled", "cancelled", status)

	// BT-7.6 history records the stop.
	_, body, conv := getConversation(A, w.alpha, "")
	latestStatus := ""
	if conv.Latest != nil {
		latestStatus = conv.Latest.Status
	}
	inHistory := false
	for _, t := range conv.Turns {
		if t.PromptID == pid {
			inHistory = true
		}
	}
	bt("BT-7.6", "stopped turn recorded", latestStatus == "cancelled" && inHistory,
		"latest cancelled, turn in history",
		fmt.Sprintf("latest=%q inHistory=%v", latestStatus, inHistory))

	// Session teardown lands microseconds after the status write.
	time.Sleep(1500 * time.Millisecond)

	// Fast stub from here: the resumes below must run to completion, not
	// stall against the slow stream (slow mode belongs to BT-7.1/7.10 only).
	w.stub.slow.Store(false)

	// BT-7.7 concurrent double-resume: exactly one claim wins.
	type resumeRes struct {
		code   int
		errMsg string
	}
	w.aliceM.reserve(2)
	ch := make(chan resumeRes, 2)
	barrier := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-barrier
			c, err := A.sse("POST", fmt.Sprintf("/v1/prompts/%d/resume", pid), nil)
			if err != nil {
				ch <- resumeRes{errMsg: err.Error()}
				return
			}
			defer c.abort()
			if c.status() != 200 {
				ch <- resumeRes{code: c.status(), errMsg: trunc(c.body(400), 200)}
				return
			}
			c.events(90*time.Second, func(ev sseEvent) bool { return ev.Kind == "done" })
			ch <- resumeRes{code: 200}
		}()
	}
	close(barrier)
	r1, r2 := <-ch, <-ch
	oneStream := (r1.code == 200 && r2.code == 409) || (r1.code == 409 && r2.code == 200)
	final := pollTerminal(A, pid, 60*time.Second)
	bt("BT-7.7", "concurrent resume claims once", oneStream && final == "completed",
		"one 200 stream + one 409, turn completed",
		fmt.Sprintf("codes=%d/%d (err %q / %q), final=%s", r1.code, r2.code, r1.errMsg, r2.errMsg, final))

	// BT-7.8 resume after completion.
	w.aliceM.reserve(1)
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/resume", pid), nil)
	bt("BT-7.8", "resume after completion", st == 409, "409", d(st, body))

	// BT-7.9 discard after completion: refused, status untouched.
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/discard", pid), nil)
	_, pbody := A.do("GET", fmt.Sprintf("/v1/prompts/%d", pid), nil)
	var pr promptRow
	_ = json.Unmarshal([]byte(pbody), &pr)
	bt("BT-7.9", "discard after completion", st == 409 && pr.Status == "completed",
		"409 and status stays completed",
		fmt.Sprintf("discard=%s status=%q", d(st, body), pr.Status))

	// BT-7.10 second full cycle: slow → abort → resume → completed, with the
	// transcript carrying exactly one user row for the prompt.
	w.stub.slow.Store(true)
	w.aliceM.reserve(1)
	conn2, err := A.sse("POST", "/v1/prompts/stream", map[string]any{
		"project_id": w.alpha,
		"mode":       "build",
		"prompt":     "breaktest slow turn two: another long job",
	})
	if err != nil {
		btf("BT-7.10", "second stop → resume cycle", "open: %v", err)
	} else if conn2.status() != 200 {
		btf("BT-7.10", "second stop → resume cycle", "stream status %d: %s",
			conn2.status(), trunc(conn2.body(300), 200))
		conn2.abort()
	} else {
		conn2.events(30*time.Second, func(ev sseEvent) bool { return len(conn2.seen) >= 1 })
		pid2 := conn2.promptIDFromEvents()
		if pid2 == 0 {
			if idx, ok := latestPrompt(A, w.alpha); ok {
				pid2 = idx.ID
			}
		}
		conn2.abort()
		stopped := pollTerminal(A, pid2, 45*time.Second)

		w.stub.slow.Store(false)
		w.aliceM.reserve(1)
		conn3, err3 := A.sse("POST", fmt.Sprintf("/v1/prompts/%d/resume", pid2), nil)
		resumed, doneSeen, doneSt := false, false, ""
		if err3 != nil {
			doneSt = "open: " + err3.Error()
		} else {
			if conn3.status() == 200 {
				conn3.events(90*time.Second, func(ev sseEvent) bool { return ev.Kind == "done" })
				doneSt, doneSeen = conn3.doneStatus()
				resumed = true
			} else {
				doneSt = fmt.Sprintf("resume status %d: %s", conn3.status(), trunc(conn3.body(300), 200))
			}
			conn3.abort()
		}
		final2 := pollTerminal(A, pid2, 60*time.Second)
		_, _, conv2 := getConversation(A, w.alpha, "")
		userRows := 0
		for _, t := range conv2.Turns {
			if t.PromptID == pid2 && t.Role == "user" {
				userRows++
			}
		}
		okCycle := stopped == "cancelled" && resumed && final2 == "completed" && userRows == 1
		detail := fmt.Sprintf("stop=%s resumed=%v done=%q final=%s userRows=%d",
			stopped, resumed, doneSt, final2, userRows)
		if resumed && !doneSeen {
			detail += " (no done frame)"
		}
		bt("BT-7.10", "stop → resume cycle", okCycle,
			"cancelled → completed, exactly one user row", detail)
	}

	// BT-7.11 answer on a finished turn.
	w.aliceM.reserve(1)
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/answer", pid), map[string]string{"answer": "nope"})
	bt("BT-7.11", "answer on completed", st == 409, "409", d(st, body))

	// BT-7.12 steer a finished turn.
	w.aliceM.reserve(1)
	st, body = A.do("POST", fmt.Sprintf("/v1/prompts/%d/steer", pid), map[string]string{"message": "too late"})
	bt("BT-7.12", "steer on completed", st == 409, "409", d(st, body))

	// BT-7.13 the limiter itself: carol's sixth metered call in one window.
	// Deliberately raw — no reserve — because 429 is the expected outcome.
	var codes []int
	var retryAfter string
	for i := 0; i < 6; i++ {
		st, hdr, _ := w.carol.doH("POST", "/v1/prompts/987654321/resume", nil)
		codes = append(codes, st)
		if st == 429 {
			retryAfter = hdr.Get("Retry-After")
		}
	}
	firstFiveClean := true
	for _, c := range codes[:5] {
		if c == 429 {
			firstFiveClean = false
		}
	}
	bt("BT-7.13", "prompt limiter 429s the sixth call",
		firstFiveClean && codes[5] == 429 && retryAfter != "",
		"5 allowed, then 429 with Retry-After",
		fmt.Sprintf("codes=%v Retry-After=%q", codes, retryAfter))

	// BT-7.14 usage accounting endpoint.
	st, body = A.do("GET", "/v1/usage", nil)
	bt("BT-7.14", "usage endpoint", st == 200, "200", d(st, body))
}

// stageDelete covers BT-8.1..8.3.
func stageDelete() {
	A, B := w.alice, w.bob

	// BT-8.1 fresh, childless project.
	st, body := A.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Fresh"})
	var fresh project
	_ = json.Unmarshal([]byte(body), &fresh)
	if st != 201 || fresh.ID == 0 {
		btf("BT-8.1", "delete fresh project", "create failed: %s", d(st, body))
	} else {
		fid := strconv.FormatInt(fresh.ID, 10)
		stDel, bodyDel := A.do("DELETE", "/v1/projects/"+fid, nil)
		stGet, _ := A.do("GET", "/v1/projects/"+fid, nil)
		_, err := os.Stat(workspacePath(fresh.ID))
		dirGone := os.IsNotExist(err)
		bt("BT-8.1", "delete fresh project", stDel == 200 && stGet == 404 && dirGone,
			"200, then 404, workspace gone",
			fmt.Sprintf("delete=%s get=%d dirGone=%v", d(stDel, bodyDel), stGet, dirGone))
	}

	// BT-8.2 used project: the FK sweep (child rows exist).
	st, body = A.do("POST", "/v1/projects", map[string]string{"name": "Breaktest Used"})
	var used project
	_ = json.Unmarshal([]byte(body), &used)
	if st != 201 || used.ID == 0 {
		btf("BT-8.2", "delete used project", "create failed: %s", d(st, body))
	} else {
		w.stub.slow.Store(false)
		pid, detail := runFastTurn(A, &w.aliceM, used.ID, "breaktest used-turn: one line")
		if pid == 0 {
			bts("BT-8.2", "delete used project", "setup turn failed: "+detail)
		} else {
			uid := strconv.FormatInt(used.ID, 10)
			st, body = A.do("DELETE", "/v1/projects/"+uid, nil)
			ok := st == 200
			detail2 := d(st, body)
			if ok {
				stGet, _ := A.do("GET", "/v1/projects/"+uid, nil)
				_, err := os.Stat(workspacePath(used.ID))
				if stGet != 404 || !os.IsNotExist(err) {
					ok = false
					detail2 += fmt.Sprintf(" (aftermath get=%d dirGone=%v)", stGet, os.IsNotExist(err))
				}
			}
			bt("BT-8.2", "delete used project", ok, "200 + gone", detail2)
		}
	}

	// BT-8.3 delete while a turn is running: must be refused, not raced.
	if w.beta == 0 {
		bts("BT-8.3", "delete while running", "no bob project")
		return
	}
	w.stub.slow.Store(true)
	w.bobM.reserve(1)
	conn, err := B.sse("POST", "/v1/prompts/stream", map[string]any{
		"project_id": w.beta,
		"mode":       "build",
		"prompt":     "breaktest slow turn on beta: hold the loop open",
	})
	if err != nil {
		bts("BT-8.3", "delete while running", "stream open failed: "+err.Error())
		return
	}
	defer conn.abort()
	if conn.status() != 200 {
		bts("BT-8.3", "delete while running", fmt.Sprintf("stream status %d", conn.status()))
		return
	}
	conn.events(30*time.Second, func(ev sseEvent) bool { return ev.Kind == "text.delta" })
	pidB := conn.promptIDFromEvents()
	if pidB == 0 {
		if idx, ok := latestPrompt(B, w.beta); ok {
			pidB = idx.ID
		}
	}

	bid := strconv.FormatInt(w.beta, 10)
	st, body = B.do("DELETE", "/v1/projects/"+bid, nil)
	ok := st == 409
	detail := d(st, body)

	// Whatever happened, the turn must settle and the project stay coherent.
	conn.abort()
	settle := "unknown"
	if pidB > 0 {
		settle = pollTerminal(B, pidB, 45*time.Second)
	}
	stGet, _ := B.do("GET", "/v1/projects/"+bid, nil)
	if stGet != 200 {
		ok = false
		detail += fmt.Sprintf(" (project not readable after delete attempt: GET %d)", stGet)
	}
	w.stub.slow.Store(false)
	bt("BT-8.3", "delete while running refused", ok, "409, project intact",
		fmt.Sprintf("%s (turn settled: %s)", detail, settle))
}

// ---------------------------------------------------------------------------
// Report: the run's verifiable artifact.
// ---------------------------------------------------------------------------

func report() {
	fmt.Println("\n─ breaktest report " + strings.Repeat("─", 30))
	passed, failed, skipped := 0, 0, 0
	var body strings.Builder
	fmt.Fprintf(&body, "# breaktest scorecard — %s\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&body, "base: %s\n\n", *base)
	muCases.Lock()
	for _, c := range caseList {
		switch c.verdict {
		case "PASS":
			passed++
		case "SKIP":
			skipped++
		default:
			failed++
		}
		fmt.Printf("  %-4s %-9s %-38s %s\n", c.verdict, c.id, c.name, c.detail)
		fmt.Fprintf(&body, "- %s **%s** %s — %s\n", c.verdict, c.id, c.name, c.detail)
	}
	muCases.Unlock()
	summary := fmt.Sprintf("%d passed, %d failed, %d skipped — %s total",
		passed, failed, skipped, time.Since(started).Truncate(time.Second))
	fmt.Println("\n" + summary)
	fmt.Fprintf(&body, "\n%s\n", summary)
	if path := os.Getenv("BREAKTEST_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(body.String()), 0o644); err != nil {
			fmt.Printf("scorecard publish failed: %v\n", err)
		} else {
			fmt.Printf("scorecard published: %s\n", path)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func main() {
	flag.Parse()
	started = time.Now()

	wsRoot := os.Getenv("WORKSPACE_DIR")
	fmt.Printf("breaktest: %s (oauth stub :%d, model stub :%d, workspaces %s)\n",
		*base, *stubPort, *modelPort, wsRoot)
	if wsRoot == "" {
		fmt.Println("fatal: WORKSPACE_DIR is not set — run via e2e/run-breaktest.sh")
		os.Exit(2)
	}

	if *stubPort > 0 {
		if err := startOAuthStub(fmt.Sprintf("127.0.0.1:%d", *stubPort)); err != nil {
			fmt.Printf("fatal: oauth stub: %v\n", err)
			os.Exit(1)
		}
	}
	stub, err := startModelStub(fmt.Sprintf("127.0.0.1:%d", *modelPort))
	if err != nil {
		fmt.Printf("fatal: model stub: %v\n", err)
		os.Exit(1)
	}
	w.stub = stub

	stageMonitor()
	if !stageAuth() {
		report()
		return
	}
	stageMonitorAuthed()
	stageProject()
	stageFiles()
	stageSetup()
	stageIsolation()
	stageTurns()
	stageDelete()

	// BT-1.10 the server survives the whole run.
	st, body := (&client{name: "anonymous"}).do("GET", "/healthz", nil)
	bt("BT-1.10", "healthz after the run", stIn(st, 200), "200", d(st, body))

	report()
}
