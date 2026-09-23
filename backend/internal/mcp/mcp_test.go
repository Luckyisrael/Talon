package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestMain doubles as the stdio fixture: when Talon dials the test binary
// with TALON_MCP_TEST_SERVER=1 (see TestStdioClient), it speaks MCP on its
// own stdio instead of running tests — the helper-process idiom, so the
// real subprocess path (pipes, group kill, stderr tail) is exercised for
// real.
func TestMain(m *testing.M) {
	if os.Getenv("TALON_MCP_TEST_SERVER") == "1" {
		runStdioFixture()
		return
	}
	os.Exit(m.Run())
}

// --- shared fixture protocol ---

// handleRPC answers the three methods Talon uses, plus honest errors.
func handleRPC(req rpcRequest) (rpcResponse, bool) {
	if req.ID == 0 {
		return rpcResponse{}, false // notification (initialized) — no reply
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: &req.ID}
	switch req.Method {
	case "initialize":
		result, _ := json.Marshal(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fixture", "version": "1"},
		})
		resp.Result = result
	case "tools/list":
		result, _ := json.Marshal(map[string]any{
			"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "echoes its message back",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"message": map[string]any{"type": "string"}},
						"required":   []string{"message"},
					},
				},
				{"name": "boom", "description": "always fails", "inputSchema": map[string]any{"type": "object"}},
			},
		})
		resp.Result = result
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		// Params decodes as any on the wire — round-trip through marshal
		// so the fixture reads it back as a struct.
		if raw, mErr := json.Marshal(req.Params); mErr == nil {
			_ = json.Unmarshal(raw, &params)
		}
		switch params.Name {
		case "echo":
			result, _ := json.Marshal(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "echo:" + string(params.Arguments)}},
			})
			resp.Result = result
		case "boom":
			result, _ := json.Marshal(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "boom failed"}},
				"isError": true,
			})
			resp.Result = result
		default:
			resp.Error = &rpcError{Code: -32602, Message: "unknown tool " + params.Name}
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, true
}

func runStdioFixture() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		if resp, ok := handleRPC(req); ok {
			if err := enc.Encode(resp); err != nil {
				return
			}
		}
	}
}

// --- config ---

func TestLoadGatesOnFlagAndValidity(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(ws+"/.talon", 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"mcp":{"servers":[
		{"name":"ok-stdio","command":["some-server","--stdio"]},
		{"name":"bad name","command":["x"]},
		{"name":"no-transport"},
		{"name":"ok-stdio","command":["dup"]},
		{"name":"weird","transport":"carrier-pigeon","command":["x"]},
		{"name":"http1","url":"https://example.test/mcp"}
	]}}`
	if err := os.WriteFile(ws+"/.talon/config.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Flag off: config present but never consulted.
	t.Setenv("TALONSTUDIO_MCP", "")
	if got := Load(ws); got != nil {
		t.Errorf("flag off must load nothing, got %d servers", len(got))
	}

	t.Setenv("TALONSTUDIO_MCP", "1")
	got := Load(ws)
	if len(got) != 2 {
		t.Fatalf("want the two valid unique servers, got %+v", got)
	}
	if got[0].Name != "ok-stdio" || got[1].Name != "http1" {
		t.Errorf("valid servers out of order: %v, %v", got[0].Name, got[1].Name)
	}
	if got[1].Transport != "http" {
		t.Errorf("transport must derive from url, got %q", got[1].Transport)
	}
}

func TestLoadBadJSONDisablesAll(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(ws+"/.talon", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws+"/.talon/config.json", []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALONSTUDIO_MCP", "true")
	if got := Load(ws); got != nil {
		t.Errorf("invalid config JSON must disable MCP, got %+v", got)
	}
}

func TestLoadExpandsEnvInHeaders(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(ws+"/.talon", 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"mcp":{"servers":[{"name":"auth","url":"https://example.test/mcp",
		"headers":{"Authorization":"Bearer ${TALON_TEST_TOKEN}"}}]}}`
	if err := os.WriteFile(ws+"/.talon/config.json", []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALONSTUDIO_MCP", "v1")
	t.Setenv("TALON_TEST_TOKEN", "sekret")
	got := Load(ws)
	if len(got) != 1 {
		t.Fatalf("want 1 server, got %+v", got)
	}
	if got[0].Headers["Authorization"] != "Bearer sekret" {
		t.Errorf("header env expansion failed: %q", got[0].Headers["Authorization"])
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{
		"echo": true, "do-thing": true, "x_1": true,
		"": false, "bad name": false, "-lead": false, "_lead": false, "a.b": false, "a/b": false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
}

// --- http transport ---

func TestHTTPClientRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("configured header missing, got %q", r.Header.Get("Authorization"))
		}
		resp, _ := handleRPC(req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, err := Dial(ctx, ServerConfig{Name: "fx", Transport: "http", URL: srv.URL,
		Headers: map[string]string{"Authorization": "Bearer tok"}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	toolsList, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(toolsList) != 2 || toolsList[0].Name != "echo" || toolsList[1].Name != "boom" {
		t.Fatalf("unexpected tools: %+v", toolsList)
	}
	if !json.Valid(toolsList[0].InputSchema) {
		t.Error("echo inputSchema must be valid JSON")
	}

	text, isErr, err := client.CallTool(ctx, "echo", json.RawMessage(`{"message":"hi"}`))
	if err != nil || isErr {
		t.Fatalf("echo: text=%q isErr=%v err=%v", text, isErr, err)
	}
	if !strings.Contains(text, `{"message":"hi"}`) {
		t.Errorf("echo text = %q", text)
	}

	_, isErr, err = client.CallTool(ctx, "boom", nil)
	if err != nil || !isErr {
		t.Errorf("boom must be a tool-level failure: isErr=%v err=%v", isErr, err)
	}

	_, _, err = client.CallTool(ctx, "nope", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("protocol error must surface, got %v", err)
	}
}

func TestDecodeHTTPResponseSSE(t *testing.T) {
	// A streamable-HTTP answer: notification frame first, reply second.
	body := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
		"event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"ok\":true}}\n\n"
	got, err := decodeHTTPResponse("text/event-stream; charset=utf-8", []byte(body))
	if err != nil {
		t.Fatalf("decode sse: %v", err)
	}
	if got.ID == nil || *got.ID != 7 || string(got.Result) != `{"ok":true}` {
		t.Errorf("picked wrong frame: %+v", got)
	}
}

// --- stdio transport ---

func TestStdioClient(t *testing.T) {
	ctx := context.Background()
	client, err := Dial(ctx, ServerConfig{
		Name:      "proc",
		Transport: "stdio",
		Command:   []string{os.Args[0]},
		Env:       map[string]string{"TALON_MCP_TEST_SERVER": "1"},
	})
	if err != nil {
		t.Fatalf("dial stdio fixture: %v", err)
	}

	toolsList, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(toolsList) != 2 {
		t.Fatalf("want 2 tools from the fixture, got %+v", toolsList)
	}

	text, isErr, err := client.CallTool(ctx, "echo", json.RawMessage(`{"message":"x"}`))
	if err != nil || isErr {
		t.Fatalf("echo: %q/%v/%v", text, isErr, err)
	}
	if !strings.HasPrefix(text, "echo:") {
		t.Errorf("echo text = %q", text)
	}

	if err := client.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestStdioDialFailureCarriesStderr(t *testing.T) {
	// A command that starts and dies without answering: the handshake must
	// fail (bounded by dialTimeout) and the error must explain why.
	ctx := context.Background()
	_, err := Dial(ctx, ServerConfig{
		Name:      "dead",
		Transport: "stdio",
		Command:   []string{"/bin/sh", "-c", "echo boom-no-mcp >&2; exit 1"},
	})
	if err == nil {
		t.Fatal("dead server must fail the handshake")
	}
	if !strings.Contains(err.Error(), "handshake failed") {
		t.Errorf("error must name the handshake, got %v", err)
	}
	if !strings.Contains(err.Error(), "boom-no-mcp") {
		t.Errorf("error must carry the stderr tail, got %v", err)
	}
}

func TestStdioCloseKillsProcessGroup(t *testing.T) {
	client, err := Dial(context.Background(), ServerConfig{
		Name:      "proc2",
		Transport: "stdio",
		Command:   []string{os.Args[0]},
		Env:       map[string]string{"TALON_MCP_TEST_SERVER": "1"},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Second close must be safe too (session teardown may race it).
	if err := client.Close(); err != nil {
		t.Errorf("double close: %v", err)
	}
}

func TestDialHTTPBadSchemeRejected(t *testing.T) {
	_, err := Dial(context.Background(), ServerConfig{
		Name: "bad", Transport: "http", URL: "ftp://example.test",
	})
	if err == nil || !strings.Contains(err.Error(), "non-http(s)") {
		// Load validates this, but Dial must not trust callers blindly.
		t.Errorf("non-http scheme must be refused, got %v", err)
	}
}
