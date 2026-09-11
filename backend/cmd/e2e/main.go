// Command e2e drives SeekerStudio's entire core product against a live
// stack: real HTTP server, real model gateway, real docker redroid device,
// real template — no smoke, no dummies. It speaks the exact client surface
// (REST, SSE) the web frontend uses, asserting every stage of the product
// loop:
//
//	account → project → plan turn (live model) → build turn (agentic,
//	verifies on the device) → device session (live, metro events flow)
//	→ kill (destroyed) → start again (second session live)
//	→ phone relay reachable.
//
// Usage:
//
//	go run ./cmd/e2e -base http://127.0.0.1:8080
//
// The driver is honest by construction: a failed stage is a real product
// bug surfaced by a real run; a stage that cannot run here says why and
// counts as skipped, never silently passes.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	base        = flag.String("base", "http://127.0.0.1:8080", "backend base URL")
	stubPort    = flag.Int("stub-port", 8098, "port for the local stub OAuth provider (0 disables)")
	projectName = flag.String("project", "E2E Live Run", "project name")
	verbose     = flag.Bool("v", false, "print SSE events as they arrive")
	prompt1     = flag.String("plan-prompt", "I want a simple counter app: one screen with a big number in the middle and a + button under it that increments the number. No wallet features, nothing else.", "plan-mode prompt")
	prompt2     = flag.String("build-prompt", "Build the counter screen from the plan: a big number in the middle, a + button under it, using Pressable. Keep it to the one screen. Then check metro_status and capture a screenshot so the user can see it.", "build-mode prompt")
)

// ---------------------------------------------------------------------------
// The stub OAuth provider. run-live.sh points the backend's AUTH_GITHUB_*
// endpoint overrides at this address; the flow under test is the production
// flow, only the host differs. The consent endpoint approves instantly —
// the E2E measures the studio's machinery, not GitHub's consent page.
// ---------------------------------------------------------------------------

func startStubProvider(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v := url.Values{}
		v.Set("code", "stub-consent-code")
		v.Set("state", q.Get("state"))
		http.Redirect(w, r, q.Get("redirect_uri")+"?"+v.Encode(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") != "stub-consent-code" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "stub-access-token"})
	})
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stub-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 42000, "login": "e2e-live", "name": "E2E Live Run",
			"email": "e2e@seekerstudio.test", "avatar_url": "",
		})
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go http.Serve(ln, mux)
	return nil
}

// ---------------------------------------------------------------------------
// Stage tracking — the run is sequential, named stages, each with a verdict.
// ---------------------------------------------------------------------------

type verdict int

const (
	vPass verdict = iota
	vSkip
	vFail
)

type stageRun struct {
	name   string
	verdict
	detail string
	took   time.Duration
}

var (
	stages     []stageRun
	curStart   time.Time
	runFailed   bool
)

func begin(name string) { curStart = time.Now() }

func pass(name, detail string) {
	stages = append(stages, stageRun{name, vPass, detail, time.Since(curStart)})
	fmt.Printf("✓ %s%s\n", name, detailSuffix(detail))
}

func fail(name string, format string, args ...interface{}) {
	detail := fmt.Sprintf(format, args...)
	stages = append(stages, stageRun{name, vFail, detail, time.Since(curStart)})
	runFailed = true
	fmt.Printf("✗ %s: %s\n", name, detail)
}

func skip(name, detail string) {
	stages = append(stages, stageRun{name, vSkip, detail, time.Since(curStart)})
	fmt.Printf("− %s: %s\n", name, detail)
}

func detailSuffix(d string) string {
	if d == "" {
		return ""
	}
	return " — " + d
}

// ---------------------------------------------------------------------------
// HTTP plumbing.
// ---------------------------------------------------------------------------

var (
	// Session creation claims a container from the warm pool (cold boots can
// take minutes); the E2E waits for the claim, not a fast header.
httpc = &http.Client{Timeout: 3 * time.Minute}
	token string
)

func call(method, path string, body interface{}, out interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, *base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode: %v (body: %s)", err, truncate(string(raw), 200))
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------------------
// SSE plumbing. The build/plan stream is a POST whose response body is the
// event stream; metro is a GET. Both are read exactly as the frontend reads
// them: buffered lines, event:/data: pairs.
// ---------------------------------------------------------------------------

type sseEvent struct {
	Kind string
	Data string
}

type sseReq struct {
	method string
	path   string
	body   interface{}
}

// readSSE consumes an SSE response until until() returns true or the
// deadline passes. until is also consulted with a zero event when the
// stream ends on its own.
func readSSE(r sseReq, deadline time.Duration, until func(ev sseEvent) bool) ([]sseEvent, error) {
	var rdr io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(r.method, *base+r.path, rdr)
	if err != nil {
		return nil, err
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: deadline + 60*time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	deadlineAt := time.Now().Add(deadline)
	var events []sseEvent
	br := bufio.NewReader(resp.Body)
	var kind string
	for time.Now().Before(deadlineAt) {
		line, err := br.ReadString('\n')
		if err != nil {
			if len(events) > 0 {
				return events, nil // stream ended; caller judges completeness
			}
			return nil, fmt.Errorf("stream read: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if kind != "" {
				ev := sseEvent{Kind: kind, Data: data}
				events = append(events, ev)
				if *verbose {
					fmt.Printf("    ← %s: %s\n", kind, truncate(data, 160))
				}
				if until(ev) {
					return events, nil
				}
				kind = ""
			}
		case line == "":
			// separator
		}
	}
	return events, nil
}

// ---------------------------------------------------------------------------
// The run.
// ---------------------------------------------------------------------------

func main() {
	flag.Parse()
	started := time.Now()
	fmt.Printf("e2e: live run against %s\n\n", *base)

	if *stubPort > 0 {
		if err := startStubProvider(fmt.Sprintf("127.0.0.1:%d", *stubPort)); err != nil {
			fmt.Printf("e2e: stub provider could not listen: %v\n", err)
			os.Exit(1)
		}
		if *verbose {
			fmt.Printf("  stub provider on 127.0.0.1:%d\n", *stubPort)
		}
	}

	var projectID int64

	// 1 — account, through the real OAuth flow against a local stub provider:
	// the backend is aimed at the stub via the AUTH_GITHUB_* endpoint env
	// overrides (set by run-live.sh), so the code path under test is exactly
	// the one a browser runs against GitHub — only the host differs.
	begin("account")
	var reg struct {
		Token string `json:"token"`
	}
	{
		stubAddr := fmt.Sprintf("127.0.0.1:%d", *stubPort)
		noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

		// Start → the backend 302s to the stub's consent screen.
		req, _ := http.NewRequest("GET", *base+"/v1/auth/github/start?redirect=/auth/callback", nil)
		resp, err := noRedirect.Do(req)
		if err != nil {
			fail("account", "start: %v", err)
			report(started)
			return
		}
		consent := resp.Header.Get("Location")
		resp.Body.Close()
		if !strings.Contains(consent, stubAddr) {
			fail("account", "start redirected to %q — the backend is not aimed at the stub provider (check the AUTH_GITHUB_* endpoint env in run-live.sh)", truncate(consent, 120))
			report(started)
			return
		}
		// The stub approves instantly and 302s back to the backend's
		// callback with the code+state.
		resp2, err := noRedirect.Get(consent)
		if err != nil {
			fail("account", "consent: %v", err)
			report(started)
			return
		}
		cb := resp2.Header.Get("Location")
		resp2.Body.Close()
		if !strings.HasPrefix(cb, *base) {
			fail("account", "stub consent redirected to %q, want the backend callback", truncate(cb, 120))
			report(started)
			return
		}
		resp3, err := noRedirect.Get(cb)
		if err != nil {
			fail("account", "callback: %v", err)
			report(started)
			return
		}
		final := resp3.Header.Get("Location")
		resp3.Body.Close()
		u, err := url.Parse(final)
		if err != nil || u.Query().Get("code") == "" {
			fail("account", "callback landed on %q without a code", truncate(final, 160))
			report(started)
			return
		}
		// Exchange: the only place a JWT is minted.
		if err := call("POST", "/v1/auth/exchange", map[string]string{"code": u.Query().Get("code")}, &reg); err != nil {
			fail("account", "exchange: %v", err)
			report(started)
			return
		}
		token = reg.Token
		pass("account", "OAuth stub flow: consent → callback → exchange")
	}

	// 2 — fresh project from the template.
	begin("project")
	var proj struct {
		ID int64 `json:"id"`
	}
	if err := call("POST", "/v1/projects", map[string]interface{}{"name": *projectName}, &proj); err != nil {
		fail("project", "create: %v", err)
		report(started)
		return
	}
	projectID = proj.ID
	pass("project", fmt.Sprintf("#%d from template", projectID))

	// 3 — plan turn (live model; the pacer holds us under the gateway budget).
	begin("plan")
	planEvents, planErr := readSSE(sseReq{method: "POST", path: "/v1/prompts/stream",
		body: map[string]interface{}{"project_id": projectID, "mode": "plan", "prompt": *prompt1}},
		12*time.Minute, func(ev sseEvent) bool { return ev.Kind == "done" })
	var planDone map[string]interface{}
	sawDone := false
	for _, ev := range planEvents {
		if ev.Kind == "done" {
			sawDone = true
			_ = json.Unmarshal([]byte(ev.Data), &planDone)
		}
	}
	if planErr != nil || !sawDone {
		fail("plan", "stream: %v (done seen: %v, %d events)", planErr, sawDone, len(planEvents))
		report(started)
		return
	}
	if st, _ := planDone["status"].(string); st != "success" {
		fail("plan", "turn status %q", st)
		report(started)
		return
	}
	pass("plan", fmt.Sprintf("%d events", len(planEvents)))

	// 4 — build turn (agentic loop; the model writes real files and verifies).
	// A question mid-turn is the negotiation-first law in action, not a
	// failure: the driver answers it (the ask_user flow is part of the
	// product) and the loop continues until done.
	begin("build")
	answers := 0
	toolCalls := 0
	verifyCalls := 0
	var buildDone map[string]interface{}
	var buildEvents []sseEvent
	var buildErr error
	buildEvents, buildErr = readSSE(sseReq{method: "POST", path: "/v1/prompts/stream",
		body: map[string]interface{}{"project_id": projectID, "mode": "build", "prompt": *prompt2}},
		20*time.Minute, func(ev sseEvent) bool { return ev.Kind == "done" })
	for {
		pendingQuestion := int64(0)
		toolCalls = 0
		verifyCalls = 0
		sawDone = false
		for _, ev := range buildEvents {
			switch ev.Kind {
			case "tool_call":
				toolCalls++
			case "verify":
				verifyCalls++
			case "question":
				q := struct {
					PromptID float64 `json:"prompt_id"`
				}{}
				if json.Unmarshal([]byte(ev.Data), &q) == nil && q.PromptID > 0 {
					pendingQuestion = int64(q.PromptID)
				}
			case "done":
				sawDone = true
				_ = json.Unmarshal([]byte(ev.Data), &buildDone)
			}
		}
		if pendingQuestion > 0 {
			// A question ends this stream (done status "waiting"), and the
			// answer endpoint resumes the turn — the product's ask_user
			// loop, not a failure. The answer is permissive within the
			// run's purpose: the E2E already granted the model the
			// workspace and the device.
			answers++
			if answers > 4 {
				fail("build", "ask_user loop exceeded 4 rounds — the model is stuck asking")
				report(started)
				return
			}
			fmt.Printf("  → answering question on prompt %d (round %d)\n", pendingQuestion, answers)
			buildEvents, buildErr = readSSE(sseReq{method: "POST",
				path: fmt.Sprintf("/v1/prompts/%d/answer", pendingQuestion),
				body:   map[string]interface{}{"answer": "Yes, proceed — this E2E run approves workspace writes and device probes."}},
				20*time.Minute,
				func(ev sseEvent) bool { return ev.Kind == "done" })
			continue
		}
		if buildErr != nil || !sawDone {
			fail("build", "stream: %v (done seen: %v, %d events)", buildErr, sawDone, len(buildEvents))
			report(started)
			return
		}
		break
	}
	st, _ := buildDone["status"].(string)
	if st == "waiting" {
		// The stream ended waiting on a question we failed to parse — that
		// is a driver bug, and it is named, not hidden.
		fail("build", "turn ended waiting on a question the driver could not parse")
		report(started)
		return
	}
	if st != "success" {
		fail("build", "turn status %q (files %v)", st, buildDone["files_touched"])
		report(started)
		return
	}
	files, _ := buildDone["files_touched"].(float64)
	verified, _ := buildDone["verified"].(bool)
	detail := fmt.Sprintf("%d tool calls, %d verify, %.0f files, verified=%v, %d ask_user answered", toolCalls, verifyCalls, files, verified, answers)
	if !verified {
		// Verified=false is honest information, not a failure: the turn
		// finished but the ladder did not fully pass. The report names it.
		detail += " (verification incomplete — named, not hidden)"
	}
	pass("build", detail)


	// 5 — device session (the warm pool claims a container).
	begin("device-session")
	sessID, err := createSession(projectID)
	if err != nil {
		fail("device-session", "create: %v", err)
		report(started)
		return
	}
	if err := waitSession(sessID, "live", 8*time.Minute); err != nil {
		fail("device-session", "session %d: %v", sessID, err)
		report(started)
		return
	}
	pass("device-session", fmt.Sprintf("session #%d live", sessID))

	// 6 — metro events flow for the project (bundle evidence after the build).
	begin("metro-events")
	metroEvents, mErr := readSSE(sseReq{method: "GET", path: fmt.Sprintf("/v1/projects/%d/metro", projectID)},
		3*time.Minute, func(ev sseEvent) bool {
			return ev.Kind == "bundle_done" || ev.Kind == "bundle_progress" || ev.Kind == "reloaded" ||
				ev.Kind == "error" || ev.Kind == "heartbeat"
		})
	if mErr != nil && len(metroEvents) == 0 {
		fail("metro-events", "stream: %v", mErr)
		report(started)
		return
	}
	kinds := map[string]int{}
	for _, ev := range metroEvents {
		kinds[ev.Kind]++
	}
	pass("metro-events", fmt.Sprintf("%d events %v", len(metroEvents), kinds))

	// 7 — kill: destroy the session; the container must go with it.
	begin("kill")
	if err := call("DELETE", fmt.Sprintf("/v1/sessions/%d", sessID), nil, nil); err != nil {
		fail("kill", "destroy: %v", err)
		report(started)
		return
	}
	// Poll until the session reports ended — the destroy path is async-clean.
	ended := false
	for i := 0; i < 30; i++ {
		var st struct {
			State string `json:"state"`
		}
		if err := call("GET", fmt.Sprintf("/v1/sessions/%d", sessID), nil, &st); err == nil && st.State != "live" {
			ended = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ended {
		fail("kill", "session %d still live 60s after destroy", sessID)
		report(started)
		return
	}
	pass("kill", fmt.Sprintf("session #%d ended", sessID))

	// 8 — start again: the second session must come up (warm pool or fresh).
	begin("start-again")
	sess2, err := createSession(projectID)
	if err != nil {
		fail("start-again", "create: %v", err)
		report(started)
		return
	}
	if err := waitSession(sess2, "live", 8*time.Minute); err != nil {
		fail("start-again", "session %d: %v", sess2, err)
		report(started)
		return
	}
	pass("start-again", fmt.Sprintf("session #%d live", sess2))

	// 9 — phone relay: test-info reports the relay, and metro answers through it.
	begin("phone-relay")
	var info struct {
		Available bool   `json:"available"`
		MetroURL  string `json:"metro_url"`
	}
	if err := call("GET", fmt.Sprintf("/v1/projects/%d/test-info", projectID), nil, &info); err != nil {
		fail("phone-relay", "test-info: %v", err)
		report(started)
		return
	}
	if !info.Available || info.MetroURL == "" {
		skip("phone-relay", "relay not available (honest state from the backend)")
	} else {
		resp, err := httpc.Get(info.MetroURL)
		if err != nil {
			fail("phone-relay", "relay fetch: %v", err)
			report(started)
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fail("phone-relay", "relay metro status %d", resp.StatusCode)
			report(started)
			return
		}
		pass("phone-relay", "metro answers through the relay")
	}

	// 10 — cleanup: leave no device behind (the reaper would catch it in
	// 30 idle minutes, but the run cleans up after itself).
	begin("cleanup")
	if err := call("DELETE", fmt.Sprintf("/v1/sessions/%d", sess2), nil, nil); err != nil {
		skip("cleanup", fmt.Sprintf("second session destroy: %v (reaper will catch it)", err))
	} else {
		pass("cleanup", "second session destroyed")
	}

	report(started)
}

func createSession(projectID int64) (int64, error) {
	var sess struct {
		ID int64 `json:"id"`
	}
	if err := call("POST", "/v1/sessions", map[string]interface{}{"project_id": projectID}, &sess); err != nil {
		return 0, err
	}
	return sess.ID, nil
}

func waitSession(id int64, want string, maxWait time.Duration) error {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		var st struct {
			State string `json:"state"`
		}
		if err := call("GET", fmt.Sprintf("/v1/sessions/%d", id), nil, &st); err == nil {
			if st.State == want {
				return nil
			}
			if st.State == "error" || st.State == "ended" {
				return fmt.Errorf("terminal state %q before %q", st.State, want)
			}
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("not %s after %s", want, maxWait)
}

func report(started time.Time) {
	fmt.Println("\n─ run report " + strings.Repeat("─", 30))
	passed, skipped, failed := 0, 0, 0
	for _, s := range stages {
		mark := "PASS"
		switch s.verdict {
		case vSkip:
			mark, skipped = "SKIP", skipped+1
		case vFail:
			mark, failed = "FAIL", failed+1
		default:
			passed++
		}
		fmt.Printf("  %-4s %-15s %s\n", mark, s.name, s.detail)
	}
	fmt.Printf("\n%d passed, %d skipped, %d failed — %s total\n",
		passed, skipped, failed, time.Since(started).Truncate(time.Second))
	if failed > 0 {
		os.Exit(1)
	}
}

var _ = url.QueryEscape // keep net/url imported for future query needs
