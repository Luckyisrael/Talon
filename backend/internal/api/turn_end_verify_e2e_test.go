package api

// Turn-end verify evidence + project verify-state E2E (row 44): what a
// finished turn leaves behind so a RELOAD restores exactly what streamed.
//
// The gap: turn_end carried only {status, error,rounds} — files_touched,
// verified, verified_best_effort, verify_policy and skipped_gates lived
// solely in the ephemeral done frame, so a restored turn lost all of it
// (no footer evidence, diff counts at zero, verify chips gone on every
// page reload), and the project-level verify state — the durable row
// export and release already fail closed on — had no read endpoint at
// all: Topbar's "Build verified" badge had nothing to feed it.
//
// Fix under test: persistTurnEnd attaches a `verify` payload to the
// marker (the durable twin of the done frame — same outcome fields,
// written at the same instant), and GET /v1/projects/{projectID}/verify-
// state reads the ProjectVerifyState row (absence = 404: "not verified",
// never a green claim).
//
// TEST_RULES rule 1 — the failure modes this file enumerates, each
// pinned by one subtest:
//
// F1. A finished build turn writes a file and completes. GET
//     /v1/projects/{id}/conversation must carry a turn_end block whose
//     `verify` payload matches the done frame it streamed:
//     files_touched (including features/ledger.json), verified,
//     verify_policy (non-empty), verified_best_effort, skipped_gates.
//     Pre-fix: no key at all.        [turn_end_carries_verify_evidence]
//
// F2. A no-op turn (immediate stop, no tools): the payload must still be
//     present and HONEST — files_touched [], verified false,
//     verify_policy "" (no sweep classified the turn). Presence means
//     "finished turn, here is what is known"; only legacy and crash
//     markers stay absent.
//                  [question_turn_evidence_honest]
//
// F3. GET /v1/projects/{id}/verify-state after the finished turn: 200
//     with the durable row — status in the sweep vocabulary, policy
//     equal to the done frame's verify_policy, skipped array present,
//     updated_at present. Pre-fix: 404 (handler answers like "never
//     verified").                        [verify_state_endpoint]
//
// F4. A project that never swept: 404 — absence reads as "not verified",
//     exactly what export/release already fail closed on. Polarity
//     guard: the staged-RED skeleton 404s too, so this passes both
//     ways.                     [verify_state_absent_is_404]
//
// TEST_RULES rule 2 — artifact: e2e/artifacts/turn-end-verify-e2e.json,
// written by the liveness runner after every subtest.
//
// Staged RED capture (plumbing first — the ai.Block.Verify field, the
// route and a GetVerifyState skeleton land so RED is assertion failures,
// not a build error; from backend/):
//
//	go test ./internal/api -count=1 -v -timeout 120s -run TestTurnEndVerifyE2E
//
// GREEN gate: the same command passes post-fix; F1–F3 fail pre-fix, F4
// passes both ways.
//
// Determinism: scripted stand-in provider (liveness harness), no
// network, bare workspace — infrastructure that cannot run lands in
// skipped_gates, never a hang.

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

// conversationWire GETs /projects/{id}/conversation through the real
// handler (production param name, authed) and decodes the turn array —
// the endpoint answers {turns, latest, planTasks}, turns first.
func conversationWire(t *testing.T, h *livenessHarness) []map[string]any {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", fmt.Sprintf("%d", h.projectID))
	req := authedRequest(h.userID, "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handler.GetConversation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET conversation: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Turns []map[string]any `json:"turns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET conversation: decode: %v", err)
	}
	return body.Turns
}

// turnEndBlock finds the assistant turn's terminal marker.
func turnEndBlock(turns []map[string]any) (map[string]any, bool) {
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn["role"] != "assistant" {
			continue
		}
		blocks, _ := turn["blocks"].([]any)
		for _, b := range blocks {
			bm, _ := b.(map[string]any)
			if bm["type"] == "turn_end" {
				return bm, true
			}
		}
	}
	return nil, false
}

// payloadString reads a string key from a wire map (absent → "").
func payloadString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// verifyStateWire GETs /projects/{id}/verify-state through the real
// handler and returns the status code plus decoded body.
func verifyStateWire(t *testing.T, h *livenessHarness, projectID int64) (int, map[string]any) {
	t.Helper()
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", fmt.Sprintf("%d", projectID))
	req := authedRequest(h.userID, "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.handler.GetVerifyState(rec, req)
	var m map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
	}
	return rec.Code, m
}

// twoRoundScript: round 1 writes a non-lintable file through the real
// file tools, round 2 stops with text — the turn completes in two model
// requests and the verify ladder sweeps the touched file.
func twoRoundScript(t *testing.T) func(int, http.ResponseWriter, *http.Request) {
	return func(idx int, w http.ResponseWriter, r *http.Request) {
		flusher := livenessOpenStream(w)
		switch idx {
		case 1:
			_, _ = io.WriteString(w, livenessToolCallStart("call_1", "write_file"))
			_, _ = io.WriteString(w, livenessToolCallArgs(`{"path":"features/ledger.json","content":"{\"entries\": []}"}`))
			_, _ = io.WriteString(w, livenessFinishFrame("tool_calls"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		case 2:
			_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		default:
			t.Errorf("unexpected build request #%d", idx)
			panic(http.ErrAbortHandler)
		}
	}
}

func TestTurnEndVerifyE2E(t *testing.T) {
	art := &livenessArtifact{Test: "TestTurnEndVerifyE2E"}
	save := func() {
		art.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
		data, err := json.MarshalIndent(art, "", "  ")
		if err != nil {
			t.Logf("artifact marshal: %v", err)
			return
		}
		path := filepath.Join("..", "..", "..", "e2e", "artifacts", "turn-end-verify-e2e.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Logf("artifact dir: %v", err)
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Logf("artifact write: %v", err)
			return
		}
		t.Logf("turn-end verify artifact: %s", path)
	}
	verifyEnv := map[string]string{
		"AI_MAX_ROUNDS":             "2",
		"TALONSTUDIO_VERIFY_POLICY": "",
		"TALONSTUDIO_LSP":           "",
	}

	// ── F1: the marker carries what the done frame streamed ──
	runLivenessSubtest(t, art, save, "turn_end_carries_verify_evidence", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		provider := newLivenessProvider(t, twoRoundScript(t))
		h := newLivenessHarness(t, provider, verifyEnv)

		out, _ := h.runBuildTurn(t)
		frames := livenessParseSSE(out)
		check("F1 has no error events", len(livenessErrorMessages(frames)) == 0,
			strings.Join(livenessErrorMessages(frames), " | "))
		done := livenessLastDone(frames)
		check("F1 done frame present", done != nil, livenessTail(out, 400))
		check("F1 done status success", livenessDoneString(done, "status") == "success",
			fmt.Sprintf("status=%q", livenessDoneString(done, "status")))
		doneFiles := jsonStringSlice(done, "files_touched")
		check("F1 done frame touched the file", hasPath(doneFiles, "features/ledger.json"),
			fmt.Sprintf("files_touched=%v", doneFiles))

		turns := conversationWire(t, h)
		marker, markerOK := turnEndBlock(turns)
		check("F1 turn_end marker exists", markerOK, "no turn_end block in conversation")
		payload, _ := marker["verify"].(map[string]any)
		check("F1 turn_end carries verify evidence", payload != nil,
			fmt.Sprintf("marker=%v", marker))

		files := jsonStringSlice(payload, "files_touched")
		check("F1 marker files include the written file", hasPath(files, "features/ledger.json"),
			fmt.Sprintf("files_touched=%v", files))
		check("F1 marker files match the done frame", len(files) == len(doneFiles),
			fmt.Sprintf("marker=%v frame=%v", files, doneFiles))

		_, vOK := payload["verified"].(bool)
		check("F1 verified present and matches the frame", vOK && payload["verified"] == done["verified"],
			fmt.Sprintf("marker=%v frame=%v", payload["verified"], done["verified"]))
		_, bePresent := payload["verified_best_effort"]
		check("F1 verified_best_effort present", bePresent && payload["verified_best_effort"] == done["verified_best_effort"],
			fmt.Sprintf("marker=%v frame=%v", payload["verified_best_effort"], done["verified_best_effort"]))

		policy := payloadString(payload, "verify_policy")
		check("F1 verify_policy non-empty", policy != "", fmt.Sprintf("policy=%q", policy))
		check("F1 verify_policy matches the frame", policy == livenessDoneString(done, "verify_policy"),
			fmt.Sprintf("marker=%q frame=%q", policy, livenessDoneString(done, "verify_policy")))

		skips := jsonStringSlice(payload, "skipped_gates")
		frameSkips := jsonStringSlice(done, "skipped_gates")
		check("F1 skipped_gates matches the frame", len(skips) == len(frameSkips),
			fmt.Sprintf("marker=%v frame=%v", skips, frameSkips))
	})

	// ── F2: a no-op turn's evidence stays present and empty ──
	runLivenessSubtest(t, art, save, "question_turn_evidence_honest", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			flusher := livenessOpenStream(w)
			if idx != 1 {
				t.Errorf("unexpected build request #%d", idx)
				panic(http.ErrAbortHandler)
			}
			_, _ = io.WriteString(w, livenessContentFrame(livenessFinalText))
			_, _ = io.WriteString(w, livenessFinishFrame("stop"))
			_, _ = io.WriteString(w, livenessDoneFrame())
			flusher.Flush()
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, verifyEnv)

		out, _ := h.runBuildTurn(t)
		frames := livenessParseSSE(out)
		done := livenessLastDone(frames)
		check("F2 done status success", done != nil && livenessDoneString(done, "status") == "success",
			livenessTail(out, 400))

		turns := conversationWire(t, h)
		marker, markerOK := turnEndBlock(turns)
		check("F2 turn_end marker exists", markerOK, "no turn_end block in conversation")
		payload, _ := marker["verify"].(map[string]any)
		check("F2 turn_end carries verify evidence", payload != nil, fmt.Sprintf("marker=%v", marker))

		files := jsonStringSlice(payload, "files_touched")
		check("F2 files_touched is an honest empty", len(files) == 0, fmt.Sprintf("files=%v", files))
		// L54 / FM-VRFYa — the contract change: an empty payload must NOT
		// carry a verdict. Pre-fix the marker shipped `verified:false` here
		// (a check that never ran claiming it failed — the "checks did not
		// pass" on every no-changes turn), and F2 pinned that lie as
		// "evidence present". Absence is the no-claim state now: payload
		// present, files empty, no verified key at all.
		_, vOK := payload["verified"].(bool)
		check("F2 verified is absent — nothing could have been checked", !vOK,
			fmt.Sprintf("verified=%v present=%v", payload["verified"], vOK))
		_, beOK := payload["verified_best_effort"].(bool)
		check("F2 verified_best_effort is absent too", !beOK,
			fmt.Sprintf("verified_best_effort=%v present=%v", payload["verified_best_effort"], beOK))
		check("F2 verify_policy is empty (no sweep)", payloadString(payload, "verify_policy") == "",
			fmt.Sprintf("policy=%q", payloadString(payload, "verify_policy")))
	})

	// ── F3: the durable sweep row is readable over HTTP ──
	runLivenessSubtest(t, art, save, "verify_state_endpoint", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		provider := newLivenessProvider(t, twoRoundScript(t))
		h := newLivenessHarness(t, provider, verifyEnv)

		out, _ := h.runBuildTurn(t)
		frames := livenessParseSSE(out)
		done := livenessLastDone(frames)
		check("F3 turn finished", done != nil && livenessDoneString(done, "status") == "success",
			livenessTail(out, 400))

		code, state := verifyStateWire(t, h, h.projectID)
		check("F3 verify-state responds 200", code == http.StatusOK,
			fmt.Sprintf("code=%d body=%v", code, state))
		status := payloadString(state, "status")
		check("F3 status in the sweep vocabulary", status == "verified" || status == "best-effort",
			fmt.Sprintf("status=%q", status))
		policy := payloadString(state, "policy")
		check("F3 policy matches the done frame", policy != "" && policy == livenessDoneString(done, "verify_policy"),
			fmt.Sprintf("state=%q frame=%q", policy, livenessDoneString(done, "verify_policy")))
		_, hasUpdated := state["updated_at"]
		check("F3 updated_at present", hasUpdated, fmt.Sprintf("state=%v", state))
		_, hasSkipped := state["skipped"].([]any)
		check("F3 skipped is an array", hasSkipped, fmt.Sprintf("state=%v", state))
	})

	// ── F4: a project that never swept answers 404 (polarity) ──
	runLivenessSubtest(t, art, save, "verify_state_absent_is_404", func(t *testing.T, res *livenessSubtest, check livenessCheck) {
		script := func(idx int, w http.ResponseWriter, r *http.Request) {
			t.Errorf("no turn expected, got build request #%d", idx)
			panic(http.ErrAbortHandler)
		}
		provider := newLivenessProvider(t, script)
		h := newLivenessHarness(t, provider, verifyEnv)

		fresh, err := h.store.CreateProject(h.userID, "Fresh", "v1", "fresh",
			"com.local.fresh", "com.local.fresh", "fresh")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		code, body := verifyStateWire(t, h, fresh.ID)
		check("F4 absent state is 404", code == http.StatusNotFound,
			fmt.Sprintf("code=%d body=%v", code, body))
	})
}
