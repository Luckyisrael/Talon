package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// rpcResponse is one server→client reply: a result or a JSON-RPC error.
type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcErrorBody
}

type rpcErrorBody struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (r rpcResponse) err(method string) error {
	if r.Error != nil {
		return fmt.Errorf("lsp: %s: %s", method, r.Error.Message)
	}
	return nil
}

// rpcConn is a framed JSON-RPC 2.0 conversation over the server's stdio:
// Content-Length framing, concurrent requests with per-id waiters, a death
// signal that fails everything in flight when the server exits, and a
// reader loop that ANSWERS server-initiated requests (workspace/
// configuration and friends) so the server never stalls waiting on us.
type rpcConn struct {
	in    *bufio.Reader
	out   io.WriteCloser // the child's stdin
	outMu sync.Mutex

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse
	closed  bool
	readErr error
	dead    chan struct{}

	// onNotify receives server→client notifications (publishDiagnostics).
	onNotify func(method string, params json.RawMessage)
}

func newRPCConn(in io.Reader, out io.WriteCloser, onNotify func(string, json.RawMessage)) *rpcConn {
	c := &rpcConn{
		in:       bufio.NewReader(in),
		out:      out,
		pending:  map[int]chan rpcResponse{},
		dead:     make(chan struct{}),
		onNotify: onNotify,
	}
	go c.readLoop()
	return c
}

func (c *rpcConn) readLoop() {
	for {
		body, err := c.readMessage()
		if err != nil {
			c.fail(err)
			return
		}
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *rpcErrorBody   `json:"error"`
		}
		if json.Unmarshal(body, &msg) != nil {
			continue // a frame we cannot parse carries no id to answer; drop it
		}
		switch {
		case msg.Method != "" && msg.ID != nil:
			c.reply(*msg.ID, c.serverAnswer(msg.Method, msg.Params))
		case msg.Method != "":
			if c.onNotify != nil {
				c.onNotify(msg.Method, msg.Params)
			}
		case msg.ID != nil:
			c.deliver(*msg.ID, rpcResponse{Result: msg.Result, Error: msg.Error})
		}
	}
}

// readMessage parses one Content-Length-framed message body. Other headers
// (Content-Type) are spec-legal and ignored.
func (c *rpcConn) readMessage() ([]byte, error) {
	length := -1
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", v)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: message without Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.in, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *rpcConn) write(msg []byte) error {
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if _, err := fmt.Fprintf(c.out, "Content-Length: %d\r\n\r\n", len(msg)); err != nil {
		return err
	}
	_, err := c.out.Write(msg)
	return err
}

// request sends a method and waits for its reply, the context, or the
// server's death — whichever comes first.
func (c *rpcConn) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.readErr
		c.mu.Unlock()
		return nil, fmt.Errorf("lsp: connection closed: %w", err)
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.abandon(id)
		return nil, err
	}
	if err := c.write(payload); err != nil {
		c.abandon(id)
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp.Result, resp.err(method)
	case <-ctx.Done():
		c.abandon(id)
		return nil, ctx.Err()
	case <-c.dead:
		return nil, fmt.Errorf("lsp: server exited: %w", c.connectionErr())
	}
}

func (c *rpcConn) notify(method string, params any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	return c.write(payload)
}

func (c *rpcConn) abandon(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *rpcConn) deliver(id int, resp rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

// fail closes the conversation once: every in-flight request gets the
// server's dying words, and later requests fail fast.
func (c *rpcConn) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.readErr = err
	pend := c.pending
	c.pending = map[int]chan rpcResponse{}
	close(c.dead)
	c.mu.Unlock()
	for _, ch := range pend {
		ch <- rpcResponse{Error: &rpcErrorBody{Message: err.Error()}}
	}
	_ = c.out.Close()
}

func (c *rpcConn) connectionErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

func (c *rpcConn) reply(id int, result any) {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return
	}
	_ = c.write(payload)
}

// serverAnswer satisfies the requests servers send mid-session.
// workspace/configuration must answer one entry per requested item (null
// settings — we configure servers through initialize, not dynamically);
// everything else gets null, which servers accept for the optional
// requests we do not implement (window/workDoneProgress, applyEdit…).
func (c *rpcConn) serverAnswer(method string, params json.RawMessage) any {
	if method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		return make([]any, len(p.Items))
	}
	return nil
}
