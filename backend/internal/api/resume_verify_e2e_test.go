package api

// Resume/Answer verify E2E: a build turn's verify law spans legs.
//
// The defect (fix under test): touched files were run-scoped — tools.NewFS
// gives each run a fresh journal — so a resumed (or answered) leg that wrote
// nothing new saw files_touched == [] and took the done-no-changes path,
// skipping the verify ladder over files earlier legs wrote, while export
// fails closed on the absent project_verify_state row. The loop's own law
// ("code changed → verify before claiming done") was violated for every
// resume. Fix: the prompt row persists the union of touched files, and
// NewAgentTurn closes over unionTouched(prior, bs.fs.Touched()) for both the
// loop's FilesTouched view and the crash-durable Persist/finalize writes.
//
// TEST_RULES rule 1 — the failure modes this file enumerates, each pinned by
// one subtest:
//
// F1. Leg 1 writes features/ledger.json then spends its round budget
//     (paused). The resume leg returns text and no tools: pre-fix the fresh
//     journal is empty → done-no-changes, verify_policy empty, zero verify
//     events, no project_verify_state row, no files_touched on the stored
//     prompt row.            [resume_verifies_prior_legs]
//
// F3. A question turn (no tools, no writes) must STILL skip verify — the
//     union must not manufacture touched files. Polarity guard: passes both
//     pre- and post-fix.        [question_turn_still_skips_verify]
//
// F4. Leg 1 writes features/launch.json then pauses on ask_user; the Answer
//     leg returns text and no tools — the same law through the answer door.
//     Leg 1's round flush persists the union mid-turn; the pause's finalize
//     re-reads the row so it can never clobber it.
//                                    [answer_verifies_prior_legs]
//
// The stored row is asserted through GET /prompts/{id} parsed into a generic
// map — never the store struct — so the test compiles pre-fix (RED is an
// assertion failure, not a build error).
//
// TEST_RULES rule 2 — artifact: every subtest writes
// e2e/artifacts/resume-verify-e2e.json (build requests with sha256, SSE tail,
// last done frame, assertions) and t.Logs the path.
//
// Staged RED capture (pre-fix, from backend/):
//
//	go test ./internal/api -count=1 -v -timeout 120s -run TestResumeVerifyE2E
//
// GREEN gate: the same command passes post-fix; F1 and F4 fail pre-fix on
// exactly the verify assertions, F3 passes both ways.
//
// Determinism basis: only .json files are written (non-lintable), so the
// bare-workspace ladder is all fast-passes (typecheck no-tsconfig, lint
// nothing-to-lint, bundle no-package.json, gradle no-gradlew) plus
// unavailable device gates (unregistered without adbTarget → ok). No repair
// loop, no extra model calls — exactly 3 build requests per multi-leg test.
// Gates need no approval in the ask posture Resume/Answer send (only
// write/edit/delete are sandbox-guarded); leg 1 sends permission_mode auto.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// jsonStringSlice reads a JSON array-of-strings key from a wire map,
// tolerating absence (the pre-fix prompt row has no files_touched key).
func jsonStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key]
	if !ok || raw == nil {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// hasPath reports whether files contains rel (exact, forward slashes).
func hasPath(files []string, rel string) bool {
	for _, f := range files {
		if f == rel {
			return true
		}
	}
	return false
}

// countEvent counts SSE frames of one event name.
func countEvent(frames []livenessSSEFrame, ev string) int {
	n := 0
	for _, f := range frames {
		if f.Event == ev {
			n++
		}
	}
	return n
}

// promptRowWire GETs /prompts/{id} through the real handler and returns the
// decoded JSON object.
func promptRowWire(t *testing.T, h *livenessHarness, promptID int64) map[string]any {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", promptID))
	req := authedRequest(h.userID, "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handler.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET prompt %d: status %d, body %s", promptID, rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("GET prompt %d: decode: %v", promptID, err)
	}
	return m
}

// resumeBuildTurn drives POST /prompts/{id}/resume with no body — the
// ask-posture continuation Resume issues (no permission_mode).
func resumeBuildTurn(t *testing.T, h *livenessHarness, promptID int64) string {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", promptID))
	req := authedRequest(h.userID, "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handler.Resume(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume prompt %d: status %d, body %s", promptID, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// answerBuildTurn drives POST /prompts/{id}/answer — the Answer door onto
// the same resume law. The reply avoids affirmative/negated/device-build
// wording so no approval side effect taints the turn.
func answerBuildTurn(t *testing.T, h *livenessHarness, promptID int64) string {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", promptID))
	req := authedRequest(h.userID, `{"answer":"Use append-only entries."}`)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handler.Answer(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer prompt %d: status %d, body %s", promptID, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// checkResumeDone asserts the terminal done frame of a resumed/answered leg
// drove the verify ladder over a file a prior leg wrote.
func checkResumeDone(check livenessCheck, out string, frames []livenessSSEFrame, rel string) map[string]any {
	done := livenessLastDone(frames)
	check("resume leg has no error events", len(livenessErrorMessages(frames)) == 0,
		strings.Join(livenessErrorMessages(frames), " | "))
	check("resume leg done frame present", done != nil, livenessTail(out, 400))
	check("resume leg done status success", livenessDoneString(done, "status") == "success",
		fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
	vp := livenessDoneString(done, "verify_policy")
	check("resume leg verify_policy strict", vp == "strict", fmt.Sprintf("verify_policy=%q", vp))
	files := jsonStringSlice(done, "files_touched")
	check(fmt.Sprintf("resume leg files_touched has %s", rel), hasPath(files, rel),
		fmt.Sprintf("files_touched=%v", files))
	nVerify := countEvent(frames, "verify")
	check("resume leg ran the verify ladder (>=1 verify event)", nVerify >= 1,
		fmt.Sprintf("verify events=%d", nVerify))
	return done
}

func TestResumeVerifyE2E(t *testing.T) {
	art := &livenessArtifact{Test: "TestResumeVerifyE2E"}
	save := func() {
		art.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.MarshalIndent(art, "", "  ")
		if err != nil {
			t.Logf("artifact marshal: %v", err)
			return
		}
		path := filepath.Join("..", "..", "..", "e2e", "artifacts", "resume-verify-e2e.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Logf("artifact dir: %v", err)
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Logf("artifact write: %v", err)
			return
		}
		t.Logf("resume-verify artifact: %s", path)
	}

	// ── F1: budget-paused leg, then Resume with no new writes ──
	runLivenessSubtest(t, art, save, "resume_verifies_prior_legs", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			switch idx {
			case 1:
				// Round 1: write a non-lintable file — every bare-workspace
				// gate then resolves fast and deterministically.
				_, _ = io.WriteString(w, livenessToolCallStart("call_1", "write_file"))
				_, _ = io.WriteString(w, livenessToolCallArgs(`{"path":"features/ledger.json","content":"{\"entries\": []}"}`))
				_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			case 2:
				// Round 2: a tool call spends the 2-round budget → Exhausted.
				_, _ = io.WriteString(w, livenessToolCallStart("call_2", "list_files"))
				_, _ = io.WriteString(w, livenessToolCallArgs("{}"))
				_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			case 3:
				// The resume leg: the model stops with no tools. Pre-fix the
				// touched set is the fresh run journal (empty) → verify is
				// skipped over leg 1's file; post-fix the union drives the
				// ladder.
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
		h := newLivenessHarness(t, provider, map[string]string{
			"AI_MAX_ROUNDS":             "2",
			"TALONSTUDIO_VERIFY_POLICY": "",
			"TALONSTUDIO_LSP":           "",
		})

		// ── leg 1: write, then spend the round budget ──
		out1, elapsed1 := h.runBuildTurn(t)
		frames1 := livenessParseSSE(out1)
		check("leg 1 has no error events", len(livenessErrorMessages(frames1)) == 0,
			strings.Join(livenessErrorMessages(frames1), " | "))
		done1 := livenessLastDone(frames1)
		check("leg 1 done frame present", done1 != nil, livenessTail(out1, 400))
		check("leg 1 done status paused", livenessDoneString(done1, "status") == "paused",
			fmt.Sprintf("status=%q", livenessDoneString(done1, "status")))
		rounds1, rounds1OK := livenessDoneRounds(done1)
		check("leg 1 ran exactly 2 rounds", rounds1OK && rounds1 == 2,
			fmt.Sprintf("rounds=%d ok=%v", rounds1, rounds1OK))

		p, err := h.store.GetLatestPrompt(h.projectID)
		if err != nil {
			t.Fatalf("GetLatestPrompt after leg 1: %v", err)
		}
		check("leg 1 row status paused", p.Status == "paused", fmt.Sprintf("status=%q", p.Status))
		check("leg 1 row round 2", p.Round == 2, fmt.Sprintf("round=%d", p.Round))

		ws, werr := h.handler.workspacePath(h.projectID)
		check("workspace resolves", werr == nil, fmt.Sprintf("err=%v", werr))
		if werr == nil {
			_, statErr := os.Stat(filepath.Join(ws, "features", "ledger.json"))
			check("features/ledger.json written on disk", statErr == nil, fmt.Sprintf("stat=%v", statErr))
		}
		reqs1, _, _ := provider.snapshot()
		check("leg 1 made exactly 2 build requests", len(reqs1) == 2, livenessReqIndexes(reqs1))

		// ── leg 2: Resume — the model stops without writing ──
		start2 := time.Now()
		out2 := resumeBuildTurn(t, h, p.ID)
		elapsed2 := time.Since(start2)
		frames2 := livenessParseSSE(out2)
		done2 := checkResumeDone(check, out2, frames2, "features/ledger.json")

		vFound, _, vErr := h.store.GetProjectVerifyState(h.projectID)
		check("resume recorded project verify state", vErr == nil && vFound,
			fmt.Sprintf("found=%v err=%v", vFound, vErr))

		// The durable row must carry the union across legs too (crash
		// durability): parsed from wire JSON, absent key → empty slice.
		wire := promptRowWire(t, h, p.ID)
		wireFiles := jsonStringSlice(wire, "files_touched")
		check("stored prompt row carries features/ledger.json", hasPath(wireFiles, "features/ledger.json"),
			fmt.Sprintf("files_touched=%v", wireFiles))

		p2, err2 := h.store.GetPrompt(p.ID)
		status2 := "missing"
		if err2 == nil && p2 != nil {
			status2 = p2.Status
		}
		check("resume row status completed", status2 == "completed",
			fmt.Sprintf("status=%q err=%v", status2, err2))

		reqs, _, titles := provider.snapshot()
		check("exactly 3 build requests across both legs", len(reqs) == 3, livenessReqIndexes(reqs))
		allErrs := append(livenessErrorMessages(frames1), livenessErrorMessages(frames2)...)
		check("no error events across the turn", len(allErrs) == 0, strings.Join(allErrs, " | "))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))

		e1 := livenessExtra(len(reqs1), done1)
		res.ExtraAttempts = e1
		if e1 >= 0 && done2 != nil {
			if e2 := livenessExtra(len(reqs)-len(reqs1), done2); e2 >= 0 {
				res.ExtraAttempts = e1 + e2
			} else {
				res.ExtraAttempts = -1
			}
		} else {
			res.ExtraAttempts = -1
		}
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.SSETail = livenessTail(out2, 1500)
		res.LastDone = done2
		res.ElapsedMS = (elapsed1 + elapsed2).Milliseconds()
	})

	// ── F3: a question turn must STILL skip verify (polarity guard) ──
	runLivenessSubtest(t, art, save, "question_turn_still_skips_verify", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			switch idx {
			case 1:
				// A pure question: no tools, no writes.
				_, _ = io.WriteString(w, livenessContentFrame(
					"The wallet scaffold already persists keys in the OS keystore; no changes are needed."))
				_, _ = io.WriteString(w, livenessFinishFrame("stop"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			default:
				t.Errorf("unexpected build request #%d", idx)
				panic(http.ErrAbortHandler)
			}
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, map[string]string{
			"TALONSTUDIO_VERIFY_POLICY": "",
			"TALONSTUDIO_LSP":           "",
		})

		out, elapsed := h.runBuildTurn(t)
		frames := livenessParseSSE(out)
		check("no error events", len(livenessErrorMessages(frames)) == 0,
			strings.Join(livenessErrorMessages(frames), " | "))
		done := livenessLastDone(frames)
		check("done frame present", done != nil, livenessTail(out, 400))
		check("done status success", livenessDoneString(done, "status") == "success",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
		check("verify_policy empty", livenessDoneString(done, "verify_policy") == "",
			fmt.Sprintf("verify_policy=%q", livenessDoneString(done, "verify_policy")))
		files := jsonStringSlice(done, "files_touched")
		check("files_touched empty", len(files) == 0, fmt.Sprintf("files_touched=%v", files))
		check("no verify events", countEvent(frames, "verify") == 0,
			fmt.Sprintf("verify events=%d", countEvent(frames, "verify")))

		vFound, _, vErr := h.store.GetProjectVerifyState(h.projectID)
		check("no project verify state row", vErr == nil && !vFound,
			fmt.Sprintf("found=%v err=%v", vFound, vErr))

		p, err := h.store.GetLatestPrompt(h.projectID)
		if err != nil {
			t.Fatalf("GetLatestPrompt: %v", err)
		}
		wire := promptRowWire(t, h, p.ID)
		wireFiles := jsonStringSlice(wire, "files_touched")
		check("stored row has no files_touched", len(wireFiles) == 0,
			fmt.Sprintf("files_touched=%v", wireFiles))

		reqs, _, titles := provider.snapshot()
		check("exactly 1 build request", len(reqs) == 1, livenessReqIndexes(reqs))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))

		res.ExtraAttempts = livenessExtra(len(reqs), done)
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.SSETail = livenessTail(out, 1500)
		res.LastDone = done
		res.ElapsedMS = elapsed.Milliseconds()
	})

	// ── F4: ask_user-paused leg, then Answer with no new writes ──
	runLivenessSubtest(t, art, save, "answer_verifies_prior_legs", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			switch idx {
			case 1:
				// Round 1: write a non-lintable file (same determinism
				// basis as F1).
				_, _ = io.WriteString(w, livenessToolCallStart("call_1", "write_file"))
				_, _ = io.WriteString(w, livenessToolCallArgs(`{"path":"features/launch.json","content":"{\"checklist\": []}"}`))
				_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			case 2:
				// Round 2: ask_user pauses the turn before executing.
				_, _ = io.WriteString(w, livenessToolCallStart("call_2", "ask_user"))
				_, _ = io.WriteString(w, livenessToolCallArgs(
					`{"question":"Which ledger layout should the app use?","choices":["append-only","double-entry"]}`))
				_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
				_, _ = io.WriteString(w, livenessDoneFrame())
				flusher.Flush()
			case 3:
				// The Answer leg: stop with no tools — the same resume law
				// through the answer door.
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
		h := newLivenessHarness(t, provider, map[string]string{
			"TALONSTUDIO_VERIFY_POLICY": "",
			"TALONSTUDIO_LSP":           "",
		})

		// ── leg 1: write, then pause on ask_user ──
		out1, elapsed1 := h.runBuildTurn(t)
		frames1 := livenessParseSSE(out1)
		check("leg 1 has no error events", len(livenessErrorMessages(frames1)) == 0,
			strings.Join(livenessErrorMessages(frames1), " | "))
		check("leg 1 asked a question", countEvent(frames1, "question") >= 1,
			fmt.Sprintf("question events=%d", countEvent(frames1, "question")))
		done1 := livenessLastDone(frames1)
		check("leg 1 done status waiting", livenessDoneString(done1, "status") == "waiting",
			fmt.Sprintf("status=%q", livenessDoneString(done1, "status")))

		p, err := h.store.GetLatestPrompt(h.projectID)
		if err != nil {
			t.Fatalf("GetLatestPrompt after leg 1: %v", err)
		}
		check("leg 1 row status waiting", p.Status == "waiting", fmt.Sprintf("status=%q", p.Status))

		ws, werr := h.handler.workspacePath(h.projectID)
		check("workspace resolves", werr == nil, fmt.Sprintf("err=%v", werr))
		if werr == nil {
			_, statErr := os.Stat(filepath.Join(ws, "features", "launch.json"))
			check("features/launch.json written on disk", statErr == nil, fmt.Sprintf("stat=%v", statErr))
		}
		reqs1, _, _ := provider.snapshot()
		check("leg 1 made exactly 2 build requests", len(reqs1) == 2, livenessReqIndexes(reqs1))

		// ── leg 2: Answer — the model stops without writing ──
		start2 := time.Now()
		out2 := answerBuildTurn(t, h, p.ID)
		elapsed2 := time.Since(start2)
		frames2 := livenessParseSSE(out2)
		done2 := checkResumeDone(check, out2, frames2, "features/launch.json")

		vFound, _, vErr := h.store.GetProjectVerifyState(h.projectID)
		check("answer recorded project verify state", vErr == nil && vFound,
			fmt.Sprintf("found=%v err=%v", vFound, vErr))

		wire := promptRowWire(t, h, p.ID)
		wireFiles := jsonStringSlice(wire, "files_touched")
		check("stored prompt row carries features/launch.json", hasPath(wireFiles, "features/launch.json"),
			fmt.Sprintf("files_touched=%v", wireFiles))

		p2, err2 := h.store.GetPrompt(p.ID)
		status2 := "missing"
		if err2 == nil && p2 != nil {
			status2 = p2.Status
		}
		check("answer row status completed", status2 == "completed",
			fmt.Sprintf("status=%q err=%v", status2, err2))

		reqs, _, titles := provider.snapshot()
		check("exactly 3 build requests across both legs", len(reqs) == 3, livenessReqIndexes(reqs))
		allErrs := append(livenessErrorMessages(frames1), livenessErrorMessages(frames2)...)
		check("no error events across the turn", len(allErrs) == 0, strings.Join(allErrs, " | "))
		check("at most one async title request", titles <= 1, fmt.Sprintf("titles=%d", titles))

		e1 := livenessExtra(len(reqs1), done1)
		res.ExtraAttempts = e1
		if e1 >= 0 && done2 != nil {
			if e2 := livenessExtra(len(reqs)-len(reqs1), done2); e2 >= 0 {
				res.ExtraAttempts = e1 + e2
			} else {
				res.ExtraAttempts = -1
			}
		} else {
			res.ExtraAttempts = -1
		}
		res.BuildRequests = reqs
		res.TitleRequests = titles
		res.SSETail = livenessTail(out2, 1500)
		res.LastDone = done2
		res.ElapsedMS = (elapsed1 + elapsed2).Milliseconds()
	})
}
