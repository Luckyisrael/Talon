package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// FM-ANTa — written first (TEST_RULES §3). The failure: ChatWithTools
// forwards the StreamSink to chatWithToolsOpenAI and calls
// chatWithToolsAnthropic WITHOUT one — the parameter does not exist on
// that function — so every build round on an anthropic provider is
// buffered: one silent request that returns the whole answer at once.
// The caller cannot see which wire it got. Run-8 scored the cost:
// S12's max gap was 133s (`phase 00:05:08 → usage 00:07:21` — one
// round of 2m12.9s with nothing on the wire) where Run-6's openai
// rounds streamed their deltas throughout and measured 31s. FM-L8a
// already legislates that heartbeats are not progress, so there is no
// cheaper way to make the gap smaller — the round has to actually
// stream.
//
// The law: every protocol path receives the sink, and a non-nil sink
// means the round streams — one SSE reader feeding both the plan
// turn's events and the build round's sink, with the result contract
// unchanged (only its latency is). A nil sink keeps today's buffered
// shape: buffering on request is correct, buffering unconditionally is
// the defect.

// toolInputJSON is the tool argument both transports must return
// byte-identically: the buffered path takes it from the JSON response,
// the streaming path assembles it from input_json_delta partials.
const toolInputJSON = `{"path":"a.txt","content":"hi"}`

// bufferedRoundJSON is the same round the SSE frames below describe,
// served to a non-streaming request.
const bufferedRoundJSON = `{"content":[` +
	`{"type":"thinking","thinking":"planning the write"},` +
	`{"type":"text","text":"Writing the file."},` +
	`{"type":"tool_use","id":"toolu_1","name":"write_file","input":` + toolInputJSON + `}` +
	`],"stop_reason":"tool_use","usage":{"input_tokens":42,"output_tokens":99,"cache_read_input_tokens":7,"cache_creation_input_tokens":0}}`

type sinkCall struct {
	text      string
	thinking  bool
	duringRun bool // captured before the server finished writing
}

// sinkRoundServer serves ONE round two ways: SSE when the request asks
// to stream, JSON otherwise — so the same server can prove that
// streaming and buffering return the same result, and can record
// whether the round asked to stream at all.
type sinkRoundServer struct {
	sawStream   atomic.Bool
	serverDone  atomic.Bool
	callsMu     sync.Mutex
	calls       []sinkCall
	sinkErrText strings.Builder
}

// sink is the StreamSink under test: it records every delta and
// whether the stream was still being written at the time.
func (s *sinkRoundServer) sink(text string, thinking bool) {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	s.calls = append(s.calls, sinkCall{text: text, thinking: thinking, duringRun: !s.serverDone.Load()})
}

func (s *sinkRoundServer) recorded() []sinkCall {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	return append([]sinkCall(nil), s.calls...)
}

func (s *sinkRoundServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(body), `"stream":true`) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, bufferedRoundJSON)
		return
	}
	s.sawStream.Store(true)

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	emit := func(event, data string) {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
		// A beat between frames: with it, "the sink was called while the
		// stream was still open" is observable rather than theoretical —
		// an end-of-round one-shot call can only happen after serverDone.
		time.Sleep(25 * time.Millisecond)
	}

	emit("message_start", `{"type":"message_start","message":{"id":"m1","role":"assistant","model":"test","content":[],"stop_reason":null,"usage":{"input_tokens":42,"output_tokens":0,"cache_read_input_tokens":7,"cache_creation_input_tokens":0}}}`)
	emit("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	emit("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"planning the write"}}`)
	emit("content_block_stop", `{"type":"content_block_stop","index":0}`)
	emit("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)
	emit("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Writing "}}`)
	emit("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"the file."}}`)
	emit("content_block_stop", `{"type":"content_block_stop","index":1}`)
	emit("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"write_file","input":{}}}`)

	// The tool argument, split across two partials exactly as anthropic
	// streams it. The delta must be raw JSON — a []byte in a map would
	// marshal as base64 and silently produce an unreadable frame.
	for _, part := range []string{`{"path":"a.txt",`, `"content":"hi"}`} {
		quoted, _ := json.Marshal(part)
		frame, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": 2,
			"delta": json.RawMessage(`{"type":"input_json_delta","partial_json":` + string(quoted) + `}`),
		})
		emit("content_block_delta", string(frame))
	}
	emit("content_block_stop", `{"type":"content_block_stop","index":2}`)
	emit("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":42,"output_tokens":99,"cache_read_input_tokens":7,"cache_creation_input_tokens":0}}`)
	emit("message_stop", `{"type":"message_stop"}`)
	s.serverDone.Store(true)
}

func newAnthropicRoundClient(base string) *Client {
	return &Client{
		protocol:   ProtocolAnthropic,
		baseURL:    base,
		apiKey:     "k",
		model:      "test-model",
		httpClient: http.DefaultClient,
	}
}

var sinkTestTool = ToolDefinition{
	Name:        "write_file",
	Description: "Write a file",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
}

// TestAnthropicRoundStreamsToTheSinkWhileTheRoundRuns is the FM-ANTa
// law asserted three independent ways, so partial answers fail:
//
//  1. the request must ask to stream (today it never does — a buffered
//     request cannot emit a single frame mid-round, which is Run-6/7/8's
//     dark wire);
//  2. the sink must be called once per delta (today: zero times; a
//     single end-of-round call is not streaming either);
//  3. the first call must land while the stream is still open.
func TestAnthropicRoundStreamsToTheSinkWhileTheRoundRuns(t *testing.T) {
	srv := &sinkRoundServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	c := newAnthropicRoundClient(ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := c.ChatWithTools(ctx, "system", nil, []ToolDefinition{sinkTestTool}, srv.sink)
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}

	if !srv.sawStream.Load() {
		t.Error(`the round was sent without "stream":true — the FM-ANTa state: ` +
			`a buffered request cannot put a single frame on the wire before it finishes`)
	}

	calls := srv.recorded()
	if len(calls) == 0 {
		t.Fatal("the sink was never called — ChatWithTools dropped it at the anthropic dispatch (FM-ANTa)")
	}
	// thinking + two text deltas must each reach the sink.
	if len(calls) < 3 {
		t.Errorf("sink called %d time(s) for 3 deltas — a single end-of-round call is not streaming", len(calls))
	}
	if !calls[0].duringRun {
		t.Errorf("first sink call arrived after the stream ended (duringRun=false) — the round buffered then spoke once: %+v", calls)
	}
	var sawThinking bool
	for _, cl := range calls {
		if cl.thinking {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Error("thinking deltas never reached the sink with thinking=true — the openai branch forwards them, so the contract differs by protocol")
	}

	// The result contract must be complete despite the round having been
	// assembled from frames: same fields the buffered path returns.
	if result.Text != "Writing the file." {
		t.Errorf("Text = %q, want %q", result.Text, "Writing the file.")
	}
	if result.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", result.StopReason)
	}
	if result.Usage.InputTokens != 42 || result.Usage.OutputTokens != 99 || result.Usage.CacheReadTokens != 7 {
		t.Errorf("Usage = %+v, want in=42 out=99 cached=7 (message_start/message_delta carry it)", result.Usage)
	}
	if len(result.ToolUses) != 1 {
		t.Fatalf("ToolUses = %d, want 1 (input_json_delta must assemble into one tool_use)", len(result.ToolUses))
	}
	if got := string(result.ToolUses[0].Input); got != toolInputJSON {
		t.Errorf("tool input = %s, want %s (partial JSON concatenated byte-identically)", got, toolInputJSON)
	}
	if len(result.Blocks) != 3 || result.Blocks[0].Type != "thinking" || result.Blocks[1].Type != "text" || result.Blocks[2].Type != "tool_use" {
		t.Errorf("Blocks = %+v, want thinking, text, tool_use in arrival order", result.Blocks)
	}
}

// TestAnthropicRoundStreamAndBufferReturnTheSameResult pins the part
// of FM-ANTa's law that must NOT move: streaming changes latency, not
// the result. The same server, one round each way, deep-equal.
func TestAnthropicRoundStreamAndBufferReturnTheSameResult(t *testing.T) {
	srv := &sinkRoundServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	streamed, err := newAnthropicRoundClient(ts.URL).
		ChatWithTools(ctx, "system", nil, []ToolDefinition{sinkTestTool}, srv.sink)
	if err != nil {
		t.Fatalf("streamed round: %v", err)
	}
	buffered, err := newAnthropicRoundClient(ts.URL).
		ChatWithTools(ctx, "system", nil, []ToolDefinition{sinkTestTool}, nil)
	if err != nil {
		t.Fatalf("buffered round: %v", err)
	}

	if !reflect.DeepEqual(streamed, buffered) {
		t.Errorf("streaming changed the result contract:\nstreamed: %+v\nbuffered: %+v", streamed, buffered)
	}
}

// TestAnthropicRoundWithoutSinkStillBuffers is the keep-test for the
// other half of the law: five planloop callers pass nil and must keep
// today's buffered shape exactly — no stream flag on the wire, no
// behaviour change for them.
func TestAnthropicRoundWithoutSinkStillBuffers(t *testing.T) {
	srv := &sinkRoundServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	result, err := newAnthropicRoundClient(ts.URL).
		ChatWithTools(ctx, "system", nil, []ToolDefinition{sinkTestTool}, nil)
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if srv.sawStream.Load() {
		t.Error("a nil-sink round asked to stream — buffering on request is correct; unconditional streaming is not the fix")
	}
	if got := srv.recorded(); len(got) != 0 {
		t.Errorf("a nil-sink round called the sink %d time(s)", len(got))
	}
	if result.StopReason != "tool_use" || result.Text != "Writing the file." {
		t.Errorf("buffered result changed: %+v", result)
	}
}
