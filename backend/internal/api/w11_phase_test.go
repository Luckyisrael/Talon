package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/skills"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// W11 phase gates: a real MCP fixture server over HTTP; the registry's
// catalog is byte-stable across policy changes (H3's law: refusal, not
// hiding); calls ride the one pipeline (validateArgs → policy → transport);
// mutating tools need explicit allow AND a write-capable posture; plugin
// tools register only for pinned workspaces.

type w11Fixture struct {
	srv   *httptest.Server
	calls atomic.Int32 // tools/call count — refusals must never reach it
}

func newW11Fixture(t *testing.T) *w11Fixture {
	t.Helper()
	f := &w11Fixture{}
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
			out.Result = json.RawMessage(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"fixture"}}`)
		case "tools/list":
			out.Result = json.RawMessage(`{"tools":[
				{"name":"echo","description":"echo back",
				 "inputSchema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"]}},
				{"name":"sneaky","description":"policy-denied tool","inputSchema":{"type":"object"}},
				{"name":"write_remote","description":"workspace-allowed mutation","inputSchema":{"type":"object"}}
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

// writeW11MCPConfig points the workspace's .talon/config.json at the
// fixture with the given deny list (mutate allow fixed).
func writeW11MCPConfig(t *testing.T, ws, url string, deny []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"mcp": map[string]any{"servers": []map[string]any{{
		"name": "fixture", "transport": "http", "url": url,
		"deny": deny, "mutate": []string{"write_remote"},
	}}}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".talon", "config.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPhaseW11MCPCatalogStable(t *testing.T) {
	st, projID := seamFixture(t)
	h := seamHandler(t, st)
	fx := newW11Fixture(t)
	ws := t.TempDir()
	t.Setenv("TALONSTUDIO_MCP", "1")

	ctx := context.Background()
	writeW11MCPConfig(t, ws, fx.srv.URL, []string{"sneaky"})
	bs := h.newBuildSession(ws, projID, 0, "", io.Discard)
	defer bs.closeMCP()

	// Catalog: mcp tools present, name-sorted (byte-stable by construction).
	cat1, err := json.Marshal(bs.defs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range bs.defs {
		names = append(names, d.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("catalog not sorted: %v", names)
	}
	for _, want := range []string{"mcp__fixture__echo", "mcp__fixture__sneaky", "mcp__fixture__write_remote"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from catalog: %v", want, names)
		}
	}
	if fx.calls.Load() != 0 {
		t.Fatalf("registration must only list, not call: calls=%d", fx.calls.Load())
	}

	// H3's catalog-stability law: tightening policy (deny echo too) does
	// not change one byte of the advertised catalog — refusal happens at
	// call time, never by hiding.
	writeW11MCPConfig(t, ws, fx.srv.URL, []string{"sneaky", "echo"})
	bs2 := h.newBuildSession(ws, projID, 0, "", io.Discard)
	defer bs2.closeMCP()
	cat2, err := json.Marshal(bs2.defs)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cat1, cat2) {
		t.Errorf("catalog bytes changed across a policy flip (H3 violation):\nA: %s\nB: %s", cat1, cat2)
	}

	run := bs.executor()

	// Policy refusal: denied tool stays listed, refuses with the reason,
	// and the server never sees the call.
	before := fx.calls.Load()
	out, isErr := run(ctx, "mcp__fixture__sneaky", json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "MCP policy") || !strings.Contains(out, "deny") {
		t.Errorf("denied call must refuse with the policy reason: isErr=%v out=%q", isErr, out)
	}
	if fx.calls.Load() != before {
		t.Errorf("denied call reached the server: %d → %d", before, fx.calls.Load())
	}

	// Pipeline validation (the same validateArgs every native tool passes):
	// missing required → refused before dispatch, server untouched.
	out, isErr = run(ctx, "mcp__fixture__echo", json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "did not run") || !strings.Contains(out, "Missing required: message") {
		t.Errorf("schema violation must refuse at the pipeline: isErr=%v out=%q", isErr, out)
	}
	if fx.calls.Load() != before {
		t.Errorf("invalid args reached the server: %d → %d", before, fx.calls.Load())
	}

	// Happy path: executes through the whole pipeline to the fixture.
	out, isErr = run(ctx, "mcp__fixture__echo", json.RawMessage(`{"message":"hi"}`))
	if isErr || !strings.Contains(out, `ok:echo:{"message":"hi"}`) {
		t.Errorf("echo through pipeline: isErr=%v out=%q", isErr, out)
	}
	if fx.calls.Load() != before+1 {
		t.Errorf("echo should hit the server once: %d → %d", before, fx.calls.Load())
	}

	// Mutating tool under the default posture (ask + workspace-write
	// sandbox) with the explicit mutate allow: runs.
	out, isErr = run(ctx, "mcp__fixture__write_remote", json.RawMessage(`{}`))
	if isErr {
		t.Errorf("allowed mutation under write posture must run: out=%q", out)
	}
	if fx.calls.Load() != before+2 {
		t.Errorf("write_remote should hit the server: %d → %d", before, fx.calls.Load())
	}

	// Read-only session: mutation refused (permission_mode branch) — and
	// read-only-classified MCP tools still run, proving the refusal is
	// about mutation, not about MCP. (Restore echo's allow first: `ro` is
	// built after the H3 flip above, which denied echo on purpose.)
	writeW11MCPConfig(t, ws, fx.srv.URL, []string{"sneaky"})
	ro := h.newBuildSession(ws, projID, 0, "", io.Discard, "read-only")
	defer ro.closeMCP()
	before = fx.calls.Load()
	out, isErr = ro.executor()(ctx, "mcp__fixture__write_remote", json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "permission_mode is read-only") {
		t.Errorf("read-only session must refuse mutation: isErr=%v out=%q", isErr, out)
	}
	if fx.calls.Load() != before {
		t.Errorf("refused mutation reached the server: %d → %d", before, fx.calls.Load())
	}
	out, isErr = ro.executor()(ctx, "mcp__fixture__echo", json.RawMessage(`{"message":"x"}`))
	if isErr {
		t.Errorf("read-only MCP tool must run under read-only posture: out=%q", out)
	}

	// Read-only sandbox (env posture): same refusal, sandbox branch.
	t.Setenv("TALONSTUDIO_SANDBOX", "read-only")
	sw := h.newBuildSession(ws, projID, 0, "", io.Discard)
	defer sw.closeMCP()
	before = fx.calls.Load()
	out, isErr = sw.executor()(ctx, "mcp__fixture__write_remote", json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "TALONSTUDIO_SANDBOX") {
		t.Errorf("read-only sandbox must refuse mutation: isErr=%v out=%q", isErr, out)
	}
	if fx.calls.Load() != before {
		t.Errorf("sandbox-refused mutation reached the server: %d → %d", before, fx.calls.Load())
	}

	// Flag off: no MCP at all — byte-identical registry without mcp__.
	t.Setenv("TALONSTUDIO_MCP", "")
	plain := h.newBuildSession(ws, projID, 0, "", io.Discard)
	defer plain.closeMCP()
	if _, ok := plain.reg.Get("mcp__fixture__echo"); ok {
		t.Error("flag off must register no MCP tools")
	}
}

// TestPhaseW11PluginToolPinnedOnly: a pinned workspace registers the
// manifest's declared tool under plugin__ …; an unpinned one registers
// nothing (the W11 gate's fourth clause).
func TestPhaseW11PluginToolPinnedOnly(t *testing.T) {
	st, projID := seamFixture(t)
	h := seamHandler(t, st)

	script := "console.log('gen')\n"
	scriptHash := fmt.Sprintf("%x", sha256.Sum256([]byte(script)))

	mux := http.NewServeMux()
	mux.HandleFunc("/registry.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"registry": "talon-plugins", "version": 1, "base": "http://" + r.Host,
			"plugins": []map[string]any{{
				"name": "talon-codegen", "version": "1.0.0",
				"path": "plugins/talon-codegen", "license": "MIT",
			}},
		})
	})
	mux.HandleFunc("/plugins/talon-codegen/talon-plugin.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name": "talon-codegen", "version": "1.0.0",
			"description": "codegen", "entry": "index.js", "skills": []string{},
			"tools": []map[string]any{{
				"name": "gen_api_stub", "description": "generate an API stub",
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"model": map[string]any{"type": "string"}},
					"required":   []string{"model"},
				},
				"command": []string{"node", "scripts/gen.js"},
			}},
			"license": "MIT",
			"files":   map[string]string{"scripts/gen.js": scriptHash},
		})
	})
	mux.HandleFunc("/plugins/talon-codegen/scripts/gen.js", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(script))
	})
	regSrv := httptest.NewServer(mux)
	t.Cleanup(regSrv.Close)

	h.orch.AttachPluginRegistry(skills.RegistryProvider{
		IndexURL: regSrv.URL + "/registry.json",
		CacheDir: t.TempDir(),
	}, time.Minute)

	// Pinned workspace: registered, read-only, namespaced, schema intact.
	pinned := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pinned, ".talon"), 0o755); err != nil {
		t.Fatal(err)
	}
	pins := `{"plugins":{"talon-codegen":"1.0.0"}}`
	if err := os.WriteFile(filepath.Join(pinned, ".talon", "plugins.json"), []byte(pins), 0o644); err != nil {
		t.Fatal(err)
	}
	bs := h.newBuildSession(pinned, projID, 0, "", io.Discard)
	defer bs.closeMCP()
	tool, ok := bs.reg.Get("plugin__talon-codegen__gen_api_stub")
	if !ok {
		t.Fatal("pinned workspace must register the plugin tool under plugin__")
	}
	pt, ok := tool.(*tools.PluginTool)
	if !ok {
		t.Fatalf("registered tool is %T", tool)
	}
	if !pt.ReadOnly() {
		t.Error("plugin tools must be read-only")
	}
	if !json.Valid(pt.InputSchema()) {
		t.Error("plugin tool schema must be valid JSON")
	}

	// Unpinned workspace: nothing — the pin file is the approval.
	unpinned := t.TempDir()
	bs2 := h.newBuildSession(unpinned, projID, 0, "", io.Discard)
	defer bs2.closeMCP()
	if _, ok := bs2.reg.Get("plugin__talon-codegen__gen_api_stub"); ok {
		t.Error("unpinned workspace must not register plugin tools")
	}
}
