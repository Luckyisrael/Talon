package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Tool is one remote declaration from tools/list. Wiring drops entries
// whose names do not fit the namespace (mcp.ValidName — a hostile or
// sloppy name never reaches the catalog), and the tools layer swaps a
// missing or malformed inputSchema for an empty-object schema
// (SchemaOrObject) so the pipeline's own validateArgs still admits object
// arguments.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Client is one dialled MCP server. Every method takes the caller's ctx so
// the registry's tool budget bounds remote work exactly as it bounds local
// tools — MCP gets no second execution path, starting with its clock.
type Client interface {
	ListTools(ctx context.Context) ([]Tool, error)
	// CallTool runs tools/call. isError is the server's own verdict on the
	// call (tool-level failure the model reads); err is protocol/transport
	// failure below that layer.
	CallTool(ctx context.Context, name string, args json.RawMessage) (text string, isError bool, err error)
	Close() error
}

// protocolVersion is the initialize handshake we offer. The server answers
// with its own; we proceed on either (the tools/list + tools/call surface
// has been stable across the versions we target, and a hard mismatch would
// surface honestly as protocol errors on the first call rather than as a
// dead server).
const protocolVersion = "2024-11-05"

// dialTimeout bounds the handshake so a wedged server cannot hold session
// construction open — it fails, gets logged with its stderr tail, and the
// workspace builds without it.
const dialTimeout = 15 * time.Second

// stderrTailLines is how much captured stderr rides back in a dial error:
// enough to name the failure (command not found, module missing), bounded
// so a chatty server cannot flood the log.
const stderrTailLines = 16

// --- JSON-RPC 2.0 wire types ---

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message) }

// Dial connects one configured server: stdio spawns and handshakes the
// process, http handshakes the endpoint. Validation already ran in Load.
func Dial(ctx context.Context, sc ServerConfig) (Client, error) {
	transport := sc.Transport
	if transport == "" {
		if len(sc.Command) > 0 {
			transport = "stdio"
		} else {
			transport = "http"
		}
	}
	switch transport {
	case "stdio":
		return dialStdio(ctx, sc)
	case "http":
		return dialHTTP(ctx, sc)
	}
	return nil, fmt.Errorf("mcp: server %q: unknown transport %q", sc.Name, transport)
}

// --- stdio transport ---

type stdioClient struct {
	name    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex // one writer at a time (request + the rare reply)

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan rpcResponse
	dead    chan struct{} // closed when stdout ends: server gone

	stderr     *stderrTail   // ring of recent stderr for dial diagnostics
	stderrDone chan struct{} // closed when the stderr scanner hits EOF
}

func dialStdio(parent context.Context, sc ServerConfig) (Client, error) {
	cmd := exec.Command(sc.Command[0], sc.Command[1:]...)
	if len(sc.Env) > 0 {
		env := cmd.Environ()
		for k, v := range sc.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	// Own process group: npx-style wrappers spawn children, and Close must
	// take the whole tree — killing only the wrapper leaks the real server.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s stdin: %w", sc.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s stdout: %w", sc.Name, err)
	}
	c := &stdioClient{
		name: sc.Name, cmd: cmd, stdin: stdin,
		pending:    make(map[int64]chan rpcResponse),
		dead:       make(chan struct{}),
		stderr:     newStderrTail(),
		stderrDone: make(chan struct{}),
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s stderr: %w", sc.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: cannot start %q: %w", sc.Command[0], err)
	}
	// Capture stderr for dial diagnostics, then keep draining so a chatty
	// server can never block on a full pipe. stderrDone marks the drain's
	// end so failure paths can read a complete tail before Close's Wait
	// yanks the pipe out from under the scanner.
	go func() {
		defer close(c.stderrDone)
		sc2 := bufio.NewScanner(stderrPipe)
		sc2.Buffer(make([]byte, 64*1024), 64*1024)
		for sc2.Scan() {
			c.stderr.add(sc2.Text())
		}
	}()
	go c.readLoop(stdout)

	ctx, cancel := context.WithTimeout(parent, dialTimeout)
	defer cancel()
	if err := c.initialize(ctx); err != nil {
		c.awaitStderr()
		_ = c.Close()
		return nil, fmt.Errorf("mcp: %s handshake failed: %w (stderr tail: %s)",
			sc.Name, err, strings.Join(c.stderr.tail(), " | "))
	}
	return c, nil
}

// readLoop dispatches server stdout: responses route to their waiting call
// by id; id-less frames are server notifications (ignored — progress spam
// is not our concern); a server-initiated *request* gets an honest
// method-not-supported reply, since Talon does not implement the reverse
// channel (sampling and friends).
func (c *stdioClient) readLoop(stdout io.Reader) {
	defer close(c.dead)
	dec := json.NewDecoder(stdout)
	for {
		var resp rpcResponse
		if err := dec.Decode(&resp); err != nil {
			return // EOF or parse break: every pending call sees c.dead
		}
		if resp.ID == nil {
			continue
		}
		if resp.Method != "" {
			_ = c.write(rpcResponse{
				JSONRPC: "2.0", ID: resp.ID,
				Error: &rpcError{Code: -32601, Message: "talon: server-initiated requests are not supported"},
			})
			continue
		}
		c.mu.Lock()
		ch := c.pending[*resp.ID]
		delete(c.pending, *resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *stdioClient) write(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.stdin.Write(data)
	return err
}

func (c *stdioClient) call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("mcp: %s: write %s: %w", c.name, method, err)
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("mcp: %s: bad %s result: %w", c.name, method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("mcp: %s: %s: %w", c.name, method, ctx.Err())
	case <-c.dead:
		c.awaitStderr()
		return fmt.Errorf("mcp: %s: server exited (%s)", c.name, strings.Join(c.stderr.tail(), " | "))
	}
}

// initialize runs the handshake and fires notifications/initialized.
func (c *stdioClient) initialize(ctx context.Context) error {
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "talon", "version": "w11"},
	}, &result); err != nil {
		return err
	}
	// Fire-and-forget: a server that requires it proceeds; one that ignores
	// it is unaffected. No response is ever waited on for a notification.
	return c.write(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
}

func (c *stdioClient) ListTools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor,omitempty"`
		}
		if err := c.call(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *stdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	text, isError, err := callToolOver(c.call, c.name, ctx, name, args)
	return text, isError, err
}

func (c *stdioClient) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		// The group, not the pid: wrappers (npx et al.) leave orphans
		// otherwise. SIGKILL — the server is stateless per this design and
		// the workspace's next request dials fresh anyway.
		_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = c.cmd.Wait() // reap; readLoop closes c.dead on stdout EOF
	return nil
}

// awaitStderr gives the stderr drain a bounded moment to finish so a
// failure message reads a complete tail. Fast path: the process is already
// dead, the scanner hits EOF immediately. The cap only matters when the
// server is alive but the handshake failed — diagnostics are best-effort
// there, never worth blocking on.
func (c *stdioClient) awaitStderr() {
	select {
	case <-c.stderrDone:
	case <-time.After(500 * time.Millisecond):
	}
}

// --- http transport ---

type httpClient struct {
	name    string
	url     string
	headers map[string]string
	hc      *http.Client
	nextID  atomic.Int64
}

func dialHTTP(parent context.Context, sc ServerConfig) (Client, error) {
	// Load validates this, but Dial must not trust callers blindly — a
	// stray scheme here would exfiltrate the initialize payload to a
	// non-MCP endpoint.
	u, err := url.Parse(sc.URL)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s bad url: %w", sc.Name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("mcp: %s non-http(s) scheme %q", sc.Name, u.Scheme)
	}
	c := &httpClient{
		name:    sc.Name,
		url:     sc.URL,
		headers: sc.Headers,
		// No client-level timeout: the caller's ctx (the registry's tool
		// budget) is the bound, same as every local tool. Dial/TLS still
		// get the transport's own defaults so a dead host fails fast.
		hc: &http.Client{Transport: http.DefaultTransport},
	}
	ctx, cancel := context.WithTimeout(parent, dialTimeout)
	defer cancel()
	if err := c.initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp: %s handshake failed: %w", sc.Name, err)
	}
	return c, nil
}

func (c *httpClient) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// MCP's streamable-HTTP servers answer either content type; we accept
	// both and parse whichever arrives.
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: %s: %s: %w", c.name, method, err)
	}
	defer resp.Body.Close()

	// 8MB: comfortably above the registry's own 32KB result cap, bounded
	// so a hostile endpoint cannot stream without end.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("mcp: %s: read %s: %w", c.name, method, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("mcp: %s: %s: HTTP %d: %s", c.name, method, resp.StatusCode, snippet(data))
	}

	rpcResp, err := decodeHTTPResponse(resp.Header.Get("Content-Type"), data)
	if err != nil {
		return fmt.Errorf("mcp: %s: %s: %w", c.name, method, err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if out != nil && len(rpcResp.Result) > 0 {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return fmt.Errorf("mcp: %s: bad %s result: %w", c.name, method, err)
		}
	}
	return nil
}

// decodeHTTPResponse picks the JSON-RPC reply out of either a direct JSON
// body or an SSE stream (the two answers streamable HTTP allows). In an SSE
// body the reply is the frame whose id is non-empty; notifications share the
// stream and are skipped.
func decodeHTTPResponse(contentType string, data []byte) (rpcResponse, error) {
	if !strings.Contains(contentType, "text/event-stream") {
		var r rpcResponse
		if err := json.Unmarshal(data, &r); err != nil {
			return rpcResponse{}, fmt.Errorf("not JSON (%s): %s", contentType, snippet(data))
		}
		return r, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &r); err != nil {
			continue
		}
		if r.ID == nil {
			continue // SSE notification on the shared stream
		}
		return r, nil
	}
	return rpcResponse{}, fmt.Errorf("no JSON-RPC reply in SSE stream")
}

func (c *httpClient) initialize(ctx context.Context) error {
	return c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "talon", "version": "w11"},
	}, nil)
}

func (c *httpClient) ListTools(ctx context.Context) ([]Tool, error) {
	var out []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor,omitempty"`
		}
		if err := c.call(ctx, "tools/list", params, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return out, nil
}

func (c *httpClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	return callToolOver(c.call, c.name, ctx, name, args)
}

func (c *httpClient) Close() error {
	c.hc.CloseIdleConnections()
	return nil
}

// --- shared protocol helpers ---

type caller func(ctx context.Context, method string, params any, out any) error

// callToolOver performs tools/call for either transport and renders the
// MCP content array: text parts joined, non-text parts honestly labeled
// (this build renders text; a binary blob never pretends to be readable).
func callToolOver(call caller, server string, ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var raw json.RawMessage
	// Keep the result raw here: the tools layer renders content, the
	// transport only decides isError vs protocol error.
	err := call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": json.RawMessage(args),
	}, &raw)
	if err != nil {
		return "", false, fmt.Errorf("mcp: %s: call %s: %w", server, name, err)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
		IsError bool `json:"isError,omitempty"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", false, fmt.Errorf("mcp: %s: bad tools/call result: %w", server, err)
	}
	var b strings.Builder
	for i, part := range result.Content {
		if i > 0 {
			b.WriteString("\n")
		}
		if part.Type == "text" || part.Type == "" {
			b.WriteString(part.Text)
			continue
		}
		fmt.Fprintf(&b, "[mcp %s content]", part.Type)
	}
	return b.String(), result.IsError, nil
}

func snippet(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// stderrTail keeps the last stderr lines for dial diagnostics without
// letting a noisy server grow without bound.
type stderrTail struct {
	mu    sync.Mutex
	lines []string
}

func newStderrTail() *stderrTail { return &stderrTail{} }

func (t *stderrTail) add(line string) {
	if len(line) > 500 {
		line = line[:500] + "…"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, line)
	if len(t.lines) > stderrTailLines {
		t.lines = t.lines[len(t.lines)-stderrTailLines:]
	}
}

func (t *stderrTail) tail() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) == 0 {
		return []string{"(no stderr)"}
	}
	return append([]string(nil), t.lines...)
}
