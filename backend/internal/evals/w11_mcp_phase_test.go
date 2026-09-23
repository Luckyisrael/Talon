package evals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	transport "github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/hooks"
	"github.com/luckyisrael/talon/backend/internal/mcp"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W11 phase gate.
//
// The plan's eval clauses, each against a real RunAgentLoop over a
// request-aware mock provider and a real MCP client dialed at a fixture
// HTTP server:
//
//  1. The fixture's tool appears sorted in the catalog, byte-stable across
//     listings (H3: schemas ride the cached prefix untouched).
//  2. It executes through the pipeline — validateArgs, guard stage,
//     dispatch — and its result lands in the conversation the model reads.
//  3. Guard coverage: the guard stage refuses an MCP call exactly as a
//     native one (model-visible reason, guardrail_reject emitted, hook/exec
//     audited) and the fixture never sees the refused call. The built-in
//     guardrail rules key native tool names by design; MCP's policy and
//     posture live in the tool, and this stage covers both.
//  4. A schema violation refuses at validateArgs before dispatch — the
//     same validation every native tool passes.

// w11MCPFixture is a real MCP server over HTTP with a call counter.
type w11MCPFixture struct {
	srv   *httptest.Server
	calls atomic.Int32
}

func newW11MCPFixture(t *testing.T) *w11MCPFixture {
	t.Helper()
	f := &w11MCPFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out := struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Result  json.RawMessage `json:"result,omitempty"`
			Error   map[string]any  `json:"error,omitempty"`
		}{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			out.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"eval-fixture"}}`)
		case "tools/list":
			out.Result = json.RawMessage(`{"tools":[
				{"name":"echo","description":"echo back",
				 "inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}}
			]}`)
		case "tools/call":
			f.calls.Add(1)
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if raw, err := json.Marshal(req.Params); err == nil {
				_ = json.Unmarshal(raw, &p)
			}
			result, _ := json.Marshal(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok:" + p.Name + ":" + string(p.Arguments)}},
			})
			out.Result = result
		default:
			out.Error = map[string]any{"code": -32601, "message": "no method " + req.Method}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// w11Registry dials the fixture for real and registers its tools into a
// real registry — the catalog + pipeline under test are production code.
func w11Registry(t *testing.T, fx *w11MCPFixture) *tools.Registry {
	t.Helper()
	ctx := context.Background()
	client, err := mcp.Dial(ctx, mcp.ServerConfig{
		Name: "fixture", Transport: "http", URL: fx.srv.URL,
	})
	if err != nil {
		t.Fatalf("dial fixture: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	remote, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list fixture tools: %v", err)
	}
	reg := tools.NewRegistry()
	for _, rt := range remote {
		reg.Register(&tools.MCPTool{
			Server: "fixture", Remote: rt.Name, RemoteDesc: rt.Description,
			Schema: rt.InputSchema, Client: client,
		})
	}
	return reg
}

// w11Turn is one executed turn plus what the gates measure.
type w11Turn struct {
	outcome   *orchestrator.BuildOutcome
	audit     *w10AuditLog
	requests  func() int
	guardrail bool // a guardrail_reject was emitted during the turn
}

// runW11Turn drives one full loop with Execute wired to the REAL registry
// (validateArgs, policy, transport — production dispatch), so the MCP call
// rides the same executeTools pipeline every native tool rides.
func runW11Turn(t *testing.T, ws string, reg *tools.Registry, toolName, toolArgs string) *w11Turn {
	t.Helper()

	t.Setenv("TALONSTUDIO_HOOKS", "1")

	var reqMu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMu.Lock()
		requests++
		n := requests
		reqMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"finish_reason": "tool_calls",
					"message": map[string]any{
						"content": "",
						"tool_calls": []map[string]any{{
							"id":   "call-1",
							"type": "function",
							"function": map[string]any{
								"name":      toolName,
								"arguments": toolArgs,
							},
						}},
					},
				}},
				"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "stop",
				"message":       map[string]any{"content": "done"},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	t.Cleanup(srv.Close)

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "w11-model")

	client, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}
	o := orchestrator.New(client, nil, nil)
	audit := &w10AuditLog{}
	sess := orchestrator.NewSession(0, 1, ws, transport.NewSessionEmitter(0, 64))
	defer sess.Emitter.Close()

	// Catalog conversion: what the model would be shown.
	defs := make([]ai.ToolDefinition, 0)
	for _, d := range reg.List() {
		defs = append(defs, ai.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}

	res := &w11Turn{audit: audit, requests: func() int {
		reqMu.Lock()
		defer reqMu.Unlock()
		return requests
	}}

	outcome, runErr := o.RunAgentLoop(context.Background(), sess, orchestrator.RunOptions{
		SystemPrompt: "build it",
		Mode:         "build",
		History:      []ai.Message{ai.UserText("build the thing")},
		Tools:        defs,
		EventLog:     audit,
		Hooks:        hooks.Load(ws),
		ReadOnlyTool: func(name string) bool {
			tool, ok := reg.Get(name)
			if !ok {
				return false
			}
			ro, ok := tool.(tools.ReadOnly)
			return ok && ro.ReadOnly()
		},
		Execute: func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			tr, err := reg.Execute(ctx, name, input)
			if err != nil {
				return err.Error(), true
			}
			return tr.Content, tr.IsError
		},
		FilesTouched: func() []string { return nil },
	})
	if runErr != nil {
		t.Fatalf("W11 refusals are deliberate, not errors: %v", runErr)
	}
	res.outcome = outcome

	// Drain the buffered transport events: did the guard stage emit?
	for {
		select {
		case ev := <-sess.Emitter.Events():
			if ev.Type == transport.GuardrailEvent {
				res.guardrail = true
			}
		default:
			return res
		}
	}
}

// w11Catalog asserts the sorted + byte-stable clauses against the real
// registry's List (the source of every request's cached prefix).
func w11Catalog(t *testing.T, reg *tools.Registry) {
	t.Helper()
	list1, err := json.Marshal(reg.List())
	if err != nil {
		t.Fatal(err)
	}
	list2, err := json.Marshal(reg.List())
	if err != nil {
		t.Fatal(err)
	}
	if string(list1) != string(list2) {
		t.Errorf("catalog not byte-stable across listings:\n%s\n%s", list1, list2)
	}
	var defs []ai.ToolDefinition
	if err := json.Unmarshal(list1, &defs); err != nil {
		t.Fatalf("catalog must unmarshal as tool definitions: %v", err)
	}
	found := false
	for i, d := range defs {
		if i > 0 && defs[i-1].Name > d.Name {
			t.Errorf("catalog not sorted: %q before %q", defs[i-1].Name, d.Name)
		}
		if d.Name == "mcp__fixture__echo" {
			found = true
			if !strings.Contains(string(d.InputSchema), "message") {
				t.Errorf("fixture schema must ride into the catalog, got %s", d.InputSchema)
			}
		}
	}
	if !found {
		t.Errorf("fixture tool missing from catalog: %s", list1)
	}
}

// Clause 1 + 2 + 4 — sorted byte-stable catalog; execution through the
// pipeline with the result in the conversation; schema violations refused
// at validateArgs before dispatch.
func TestPhaseW11MCPThroughPipeline(t *testing.T) {
	fx := newW11MCPFixture(t)
	reg := w11Registry(t, fx)
	w11Catalog(t, reg)

	// Schema violation: missing required → refused at the pipeline, the
	// fixture never sees it, and the refusal is model-visible.
	ws := w10Workspace(t, `{}`)
	r := runW11Turn(t, ws, reg, "mcp__fixture__echo", `{}`)
	blob, err := json.Marshal(r.outcome.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if fx.calls.Load() != 0 {
		t.Errorf("invalid args must not reach the server: calls=%d", fx.calls.Load())
	}
	if !strings.Contains(string(blob), "did not run") || !strings.Contains(string(blob), "Missing required: message") {
		t.Errorf("pipeline refusal must be model-visible, got %s", blob)
	}
	if r.requests() != 2 {
		t.Errorf("the refusal must reach the model and let it answer: %d requests", r.requests())
	}

	// Happy path: executes through the pipeline; the remote result lands
	// in the conversation the model reads before finishing.
	ws2 := w10Workspace(t, `{}`)
	r2 := runW11Turn(t, ws2, reg, "mcp__fixture__echo", `{"message":"hi"}`)
	blob2, err := json.Marshal(r2.outcome.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if fx.calls.Load() != 1 {
		t.Errorf("echo must reach the server exactly once: calls=%d", fx.calls.Load())
	}
	// The blob JSON-escapes quotes inside tool_result content, so assert
	// on the transport-stable prefix: the remote text landed where the
	// model reads it.
	if !strings.Contains(string(blob2), "ok:echo:") {
		t.Errorf("remote result missing from the conversation: %s", blob2)
	}
	if r2.outcome.FinalText == "" {
		t.Error("the turn must finish normally after the MCP round")
	}
}

// Clause 3 — guard coverage: the guard stage refuses an MCP call exactly
// as a native one. Model-visible reason, guardrail_reject emitted,
// hook/exec audited, fixture untouched, loop uncorrupted (the model still
// gets its answering round).
func TestPhaseW11MCPGuardCoverage(t *testing.T) {
	fx := newW11MCPFixture(t)
	reg := w11Registry(t, fx)

	ws := w10Workspace(t, `{"hooks":{"PreToolUse":[{
		"name":"block-remote",
		"tools":["mcp__fixture__echo"],
		"command":"echo '{\"decision\":\"deny\",\"reason\":\"remote tools are off in this workspace\"}'"
	}]}}`)
	r := runW11Turn(t, ws, reg, "mcp__fixture__echo", `{"message":"hi"}`)

	if fx.calls.Load() != 0 {
		t.Errorf("a refused MCP call must never reach the server: calls=%d", fx.calls.Load())
	}
	blob, err := json.Marshal(r.outcome.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "remote tools are off in this workspace") {
		t.Errorf("the guard's reason must be model-visible, got %s", blob)
	}
	if !r.guardrail {
		t.Error("the guard stage must emit guardrail_reject for a refused MCP call")
	}
	if r.requests() != 2 {
		t.Errorf("the refusal must reach the model and let it answer: %d requests", r.requests())
	}
	hp, _, ok := r.audit.findHook("block-remote")
	if !ok {
		t.Fatal("the refusing hook must audit a hook/exec row")
	}
	if hp.Decision != hooks.Deny {
		t.Errorf("audited decision = %q, want deny", hp.Decision)
	}
	if hp.Error != "" {
		t.Errorf("a deny is not an extension failure, got %q", hp.Error)
	}
}
