package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A provider that streams text delta by delta must reach the sink as each chunk
// lands, not all at once at the end. This is the mobile blank-spinner fix: the
// model call is the dead window, and the sink is what fills it.
func TestChatWithToolsStreamsDeltasToSink(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer cannot flush")
		}
		// Three chunks, each flushed separately — a real provider's cadence.
		for _, chunk := range []string{
			`data: {"choices":[{"delta":{"content":"Hello "}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":"there "}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":"world"}}]}` + "\n\n",
			`data: {"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}` + "\n\n",
			`data: [DONE]` + "\n\n",
		} {
			w.Write([]byte(chunk))
			f.Flush()
		}
	}))
	defer srv.Close()

	c := &Client{protocol: ProtocolOpenAI, baseURL: srv.URL, apiKey: "k", model: "m", httpClient: http.DefaultClient}

	var got strings.Builder
	var deliveries int32
	result, err := c.ChatWithTools(context.Background(), "system", nil, nil, func(text string, thinking bool) {
		atomic.AddInt32(&deliveries, 1)
		got.WriteString(text)
	})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if got.String() != "Hello there world" {
		t.Errorf("sink saw %q, want %q", got.String(), "Hello there world")
	}
	if n := atomic.LoadInt32(&deliveries); n != 3 {
		t.Errorf("sink delivered %d times, want 3 (one per delta)", n)
	}
	if result.Text != "Hello there world" {
		t.Errorf("result.Text = %q, want the assembled text", result.Text)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("stop = %q, want end_turn", result.StopReason)
	}
	if result.Usage.OutputTokens != 3 {
		t.Errorf("usage out = %d, want 3 (streamed usage frame)", result.Usage.OutputTokens)
	}
}

// A provider that ignores the stream flag and answers with one buffered object
// must still parse — the branch peeks the body and falls back instead of
// returning an empty result.
func TestChatWithToolsHandlesUnstreamedProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"buffered"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	c := &Client{protocol: ProtocolOpenAI, baseURL: srv.URL, apiKey: "k", model: "m", httpClient: http.DefaultClient}
	var got strings.Builder
	result, err := c.ChatWithTools(context.Background(), "system", nil, nil, func(text string, thinking bool) {
		got.WriteString(text)
	})
	if err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}
	if result.Text != "buffered" {
		t.Errorf("result.Text = %q, want buffered", result.Text)
	}
	if got.String() != "" {
		t.Errorf("sink should be silent for a buffered response, got %q", got.String())
	}
}
