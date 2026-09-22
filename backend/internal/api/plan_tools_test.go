package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// ── W3 plan-with-eyes gates (PLAN_WORLDCLASS W3 eval gate) ─────────────
//
// The plan's gate, in four assertions the tests below make literal:
//   1. a plan turn over a fixture repo cites a file that exists (the plan
//      JSON is grepped for real paths and each one is stat'ed);
//   2. no mutating tool in the plan registry (byte-assert of the exact set,
//      twice: the registry's own names and the wire's tools field);
//   3. the turn is bounded (maxPlanRounds tool rounds, then one tool-less
//      finalize call — the request count is the byte-bound);
//   4. a provider without native tools falls back to the tool-less path
//      instead of failing the turn (TALONSTUDIO_PLAN_TOOLS also gates the
//      answer: unset = today's path, asserted by TestPlanModeSendsNoToolsToModel).

// planFixture is one fixture project: a real workspace on disk with real
// files, a stand-in Anthropic model driven by each test's respond closure,
// and the handler wired to both. Every request the model receives is
// captured, byte for byte, alongside its parsed body.
type planFixture struct {
	h    *PromptHandler
	st   *store.Store
	ai   *ai.Client
	orch *orchestrator.Orchestrator
	ws   string
	uid  int64
	pid  int64

	mu     sync.Mutex
	bodies []map[string]any
	raws   []string
}

// newPlanFixture builds the fixture and points a real ai.Client at the
// stand-in model — ai.New reads the four AI_* vars, same as the existing
// plan-path tests. TALONSTUDIO_PLAN_TOOLS=v1 is set here: every test in
// this file exercises the eyes-on path (flag-off is pinned separately by
// TestPlanModeSendsNoToolsToModel, which never sets the flag).
func newPlanFixture(t *testing.T, respond func(call int, body map[string]any) (int, map[string]any)) *planFixture {
	t.Helper()

	f := &planFixture{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.raws = append(f.raws, string(raw))
		call := len(f.bodies)
		f.mu.Unlock()

		status, reply := respond(call, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if reply != nil {
			_ = json.NewEncoder(w).Encode(reply)
		}
	}))
	t.Cleanup(model.Close)

	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")
	t.Setenv("TALONSTUDIO_PLAN_TOOLS", "v1")

	client, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}

	st, err := store.New(filepath.Join(t.TempDir(), "planw3.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("w3-plan@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Eyes", "v1", "eyes", "com.local.eyes", "com.local.eyes", "eyes")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// The fixture repo: workspacePath resolves to workspaceDir/<projectID>,
	// the same directory the plan loop's registry reads.
	wsRoot := t.TempDir()
	ws := filepath.Join(wsRoot, strconv.FormatInt(project.ID, 10))
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatalf("fixture workspace: %v", err)
	}
	// The marker is only reachable by actually reading the file's contents —
	// a directory listing names it, but never spells it out.
	if err := os.WriteFile(filepath.Join(ws, "src", "app.ts"),
		[]byte("export const marker = \"W3_FIXTURE_MARKER\";\n"), 0o644); err != nil {
		t.Fatalf("fixture file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("# Eyes fixture\n"), 0o644); err != nil {
		t.Fatalf("fixture file: %v", err)
	}

	orch := orchestrator.New(client, st, nil)
	h := NewPromptHandler(st, client, wsRoot, "", orch, nil, nil, nil)

	f.h, f.st, f.ai, f.orch = h, st, client, orch
	f.ws, f.uid, f.pid = ws, user.ID, project.ID
	return f
}

// runPlan drives one plan turn through the non-streaming Send door — the
// same door prompts_test.go pins — and returns the recorder.
func (f *planFixture) runPlan(t *testing.T, h *PromptHandler) *httptest.ResponseRecorder {
	t.Helper()
	if h == nil {
		h = f.h
	}
	rec := httptest.NewRecorder()
	h.Create(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan the wallet screen","mode":"plan"}`, f.pid)))
	if rec.Code != http.StatusOK {
		t.Fatalf("plan turn: status %d, body %s", rec.Code, rec.Body.String())
	}
	return rec
}

// planEnvelope builds the reply contract plan mode speaks: a message plus
// the structured plan, with the given features cited inside it.
func planEnvelope(t *testing.T, features ...string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"message": "Read the workspace. Here is the plan.",
		"plan": map[string]any{
			"screens":    []map[string]string{{"name": "Home", "description": "Landing"}},
			"features":   features,
			"navigation": "stack",
			"packages":   []string{},
		},
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	return string(raw)
}

func textReply(text string) map[string]any {
	return map[string]any{
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": "end_turn",
	}
}

func toolUseReply(id, name, input string) map[string]any {
	return map[string]any{
		"content":     []map[string]any{{"type": "tool_use", "id": id, "name": name, "input": json.RawMessage(input)}},
		"stop_reason": "tool_use",
	}
}

// planPathRe finds path-like tokens in a plan reply — the eval gate's
// literal "grep the plan JSON for real paths".
var planPathRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_./-]*\.(?:ts|tsx|js|jsx|json|md|go)\b`)

// assertCitesExisting greps the blob for path-like tokens and stats each in
// the fixture workspace: every file the plan names must exist.
func (f *planFixture) assertCitesExisting(t *testing.T, blob string) {
	t.Helper()
	found := 0
	for _, tok := range planPathRe.FindAllString(blob, -1) {
		found++
		if _, err := os.Stat(filepath.Join(f.ws, tok)); err != nil {
			t.Errorf("plan cites %q but it does not exist in the fixture workspace: %v", tok, err)
		}
	}
	if found == 0 {
		t.Error("the plan cites no file paths at all — the gate found nothing to check")
	}
}

// systemText pulls the system prompt out of a captured request — the plan
// path sends it as a plain string (ai.chat), the tool loop as a cached
// block array (chatWithToolsAnthropic); both must carry the plan prompt.
func systemText(t *testing.T, body map[string]any) string {
	t.Helper()
	switch sys := body["system"].(type) {
	case string:
		return sys
	case []any:
		if len(sys) > 0 {
			if m, ok := sys[0].(map[string]any); ok {
				s, _ := m["text"].(string)
				return s
			}
		}
	}
	return ""
}

// requestToolNames returns the sorted tool names a captured request offered.
func requestToolNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["tools"]
	if !ok {
		t.Fatal("request carried no tools field")
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("tools field is %T, want an array", raw)
	}
	names := make([]string, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("tool entry is %T, want an object", e)
		}
		name, _ := m["name"].(string)
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// The read-only subset every plan request must offer — the registry's
// Names() sorts, so this is also the wire order.
var wantPlanTools = []string{"explore", "find_files", "grep_codebase", "list_files", "project_map", "read_file"}

// Eval gate 1+2 at the wire: the survey round runs over a fixture repo, the
// loop reads real bytes (the marker round-trips through read_file's
// result), the plan cites paths that exist, and the tools offered are
// exactly the read-only subset — nothing mutating, nothing else.
func TestPhaseW3PlanTurnSeesFixtureRepo(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		if call == 1 {
			return 200, toolUseReply("tool_1", "read_file", `{"path":"src/app.ts"}`)
		}
		return 200, textReply(planEnvelope(t, "Wire src/app.ts as the entry point"))
	})

	rec := f.runPlan(t, nil)
	f.assertCitesExisting(t, rec.Body.String())

	// The turn settled: status completed, plan persisted on the project row.
	var out struct {
		ID       int64           `json:"id"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response: %v", err)
	}
	prompt, err := f.st.GetPrompt(out.ID)
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if prompt.Status != "completed" {
		t.Errorf("prompt status = %q, want completed", prompt.Status)
	}
	if !bytes.Contains(out.Response, []byte("src/app.ts")) {
		t.Errorf("persisted response does not carry the cited plan: %s", out.Response)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 2 {
		t.Fatalf("model called %d times, want 2 (survey round + answer)", len(f.bodies))
	}
	// The survey round is a plan turn: plan system prompt, read-only subset.
	if sys := systemText(t, f.bodies[0]); sys != f.h.planPrompt {
		t.Error("survey round did not use the plan system prompt — the turn left the plan path")
	}
	if got := requestToolNames(t, f.bodies[0]); !reflect.DeepEqual(got, wantPlanTools) {
		t.Errorf("survey tools = %v, want exactly %v", got, wantPlanTools)
	}
	// The eyes read REAL bytes: the answer round carries the fixture file's
	// marker, which is reachable only through read_file's result.
	if !strings.Contains(f.raws[1], "W3_FIXTURE_MARKER") {
		t.Error("the answer round does not carry the file's contents — the planner never read the workspace")
	}
}

// Eval gate 2 (registry half): the plan registry is exactly the read-only
// subset — byte-asserted as the sorted name set, as marshalled definitions
// containing no mutating name, and as every member reporting read-only.
func TestPhaseW3PlanRegistryReadOnly(t *testing.T) {
	h := NewPromptHandler(nil, nil, t.TempDir(), "", nil, nil, nil, nil)
	reg := h.newPlanRegistry(t.TempDir())

	if got := reg.Names(); !reflect.DeepEqual(got, wantPlanTools) {
		t.Fatalf("plan registry = %v, want exactly %v", got, wantPlanTools)
	}
	for _, name := range reg.Names() {
		if !reg.IsReadOnly(name) {
			t.Errorf("plan tool %q is not marked read-only", name)
		}
	}

	blob, err := json.Marshal(reg.List())
	if err != nil {
		t.Fatalf("marshal defs: %v", err)
	}
	for _, banned := range []string{
		"write_file", "edit_file", "multi_edit", "delete_file",
		"run_command", "run_shell", "run_typecheck", "run_lint", "run_checks",
		"install_package", "build_apk", "run_gradle", "git_commit", "git_branch",
		"ask_user", "mark_task_done", "try_edit", "keep_edit", "delegate",
		"write_adr", "write_memory",
	} {
		if bytes.Contains(blob, []byte(`"`+banned+`"`)) {
			t.Errorf("plan registry exposes mutating tool %q: %s", banned, blob)
		}
	}
}

// Eval gate 3 (W3.3 bound): a planner that never stops looking is cut off
// after maxPlanRounds tool rounds — the (maxPlanRounds+1)th call is the
// tool-less finalize, and the turn still settles with a plan.
func TestPhaseW3PlanRoundCap(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		if call <= maxPlanRounds {
			return 200, toolUseReply(fmt.Sprintf("tool_%d", call), "read_file", `{"path":"src/app.ts"}`)
		}
		return 200, textReply(planEnvelope(t, "capped survey re-read src/app.ts"))
	})

	rec := f.runPlan(t, nil)
	f.assertCitesExisting(t, rec.Body.String())

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != maxPlanRounds+1 {
		t.Fatalf("model called %d times, want %d (cap + one finalize)", len(f.bodies), maxPlanRounds+1)
	}
	for i := 0; i < maxPlanRounds; i++ {
		if _, has := f.bodies[i]["tools"]; !has {
			t.Errorf("round %d carried no tools", i+1)
		}
	}
	// The finalize call is tool-less: with the cap spent, the only thing left
	// is the answer.
	if _, has := f.bodies[maxPlanRounds]["tools"]; has {
		t.Error("the finalize call still carried tools — the round cap did not bound the loop")
	}
}

// Eval gate 4 (W3.3 fallback): a provider that rejects native tools never
// fails the turn — the eyes attempt degrades to the tool-less single-shot,
// and a workspace-less composition degrades before the first call at all.
func TestPhaseW3PlanToollessFallback(t *testing.T) {
	respond := func(t *testing.T) func(int, map[string]any) (int, map[string]any) {
		return func(call int, body map[string]any) (int, map[string]any) {
			if _, hasTools := body["tools"]; hasTools {
				return 400, map[string]any{
					"type":  "error",
					"error": map[string]any{"type": "invalid_request_error", "message": "tools are not supported by this provider"},
				}
			}
			return 200, textReply(planEnvelope(t, "fallback plan citing src/app.ts"))
		}
	}

	t.Run("provider rejects tools", func(t *testing.T) {
		f := newPlanFixture(t, respond(t))
		rec := f.runPlan(t, nil)
		f.assertCitesExisting(t, rec.Body.String())

		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.bodies) != 2 {
			t.Fatalf("model called %d times, want 2 (eyes attempt, then tool-less fallback)", len(f.bodies))
		}
		if _, has := f.bodies[0]["tools"]; !has {
			t.Error("first call carried no tools — the eyes never attempted")
		}
		if _, has := f.bodies[1]["tools"]; has {
			t.Error("fallback call still carried tools — it cannot be the tool-less path")
		}
	})

	t.Run("no workspace to read", func(t *testing.T) {
		f := newPlanFixture(t, respond(t))
		bare := NewPromptHandler(f.st, f.ai, "", "", f.orch, nil, nil, nil)
		rec := f.runPlan(t, bare)
		f.assertCitesExisting(t, rec.Body.String())

		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.bodies) != 1 {
			t.Fatalf("model called %d times, want 1 (straight to tool-less planning)", len(f.bodies))
		}
		if _, has := f.bodies[0]["tools"]; has {
			t.Error("workspace-less fallback still offered tools")
		}
	})
}

// The streaming door (which the Answer door inherits) takes the same eyes-on
// path: the survey round runs, the plan arrives as the plan event, and the
// done frame reports success.
func TestPhaseW3PlanStreamDoorWithEyes(t *testing.T) {
	f := newPlanFixture(t, func(call int, body map[string]any) (int, map[string]any) {
		if call == 1 {
			return 200, toolUseReply("tool_1", "read_file", `{"path":"src/app.ts"}`)
		}
		return 200, textReply(planEnvelope(t, "streamed plan citing src/app.ts"))
	})

	rec := httptest.NewRecorder()
	f.h.Stream(rec, authedRequest(f.uid,
		fmt.Sprintf(`{"project_id":%d,"prompt":"plan the wallet screen","mode":"plan"}`, f.pid)))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream: status %d, body %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	for _, want := range []string{"event: plan", `"status":"success"`, "src/app.ts"} {
		if !strings.Contains(out, want) {
			t.Errorf("SSE stream missing %q:\n%s", want, out)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.Contains(f.raws[1], "W3_FIXTURE_MARKER") {
		t.Error("the streamed answer round does not carry the file's contents — the eyes never ran")
	}
}
