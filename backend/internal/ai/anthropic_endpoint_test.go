package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// FM-URLa — written first (TEST_RULES §3). The failure: four call sites
// build the Messages endpoint two different ways, so no single
// AI_BASE_URL runs an anthropic provider end-to-end — plan mode streams
// (`/messages`) while the build loop buffers (`/v1/messages`), and
// exactly one of those is right for whatever base the operator set. On
// agentrouter.org the wrong path does not 404; it answers 200 with the
// SPA's index.html, so the mismatch surfaces as a parse failure or a
// blank plan rather than as "wrong URL".
//
// The law: one endpoint builder, called by all four sites, accepting
// both spellings of the root (`https://host` and `https://host/v1` —
// the convention the openai side and every gateway use).
//
// Red today, and each base spelling fails a different half of the
// matrix, so fixing two sites and leaving two cannot pass:
//
//	base ends in /v1:  Chat, ChatWithTools   → /v1/v1/messages (404)
//	bare root base:    ChatStream, Vision    → /messages       (HTML)
const anthropicEndpointWant = "/v1/messages"

// anthropicEndpointRecorder serves every path the four anthropic call
// sites can build, records what each one asked for, and answers in the
// shape that caller expects: SSE for a `"stream":true` request, JSON
// otherwise. It never rejects a path — the point of the test is which
// path was *chosen*, so a wrong choice must still be recorded.
type anthropicEndpointRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *anthropicEndpointRecorder) served() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

const anthropicSSEBody = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"test","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

func (r *anthropicEndpointRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)

		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()

		if strings.Contains(string(body), `"stream":true`) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, anthropicSSEBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	})
}

// TestAnthropicMessagePathsAgree drives the four real call sites — the
// buffered chat, the agentic loop (the build turn's path), the stream
// (the plan turn's path), and vision — against one recording server and
// demands that every one of them chose the same endpoint, for both
// spellings of the base.
func TestAnthropicMessagePathsAgree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		basePath string
	}{
		{"base ends in /v1", "/v1"},
		{"bare root base", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &anthropicEndpointRecorder{}
			srv := httptest.NewServer(rec.handler())
			defer srv.Close()

			c := &Client{
				protocol:   ProtocolAnthropic,
				baseURL:    srv.URL + tc.basePath,
				apiKey:     "k",
				model:      "test-model",
				httpClient: http.DefaultClient,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			// 1. buffered chat — chatAnthropic.
			_, _ = c.Chat(ctx, "system", nil)

			// 2. agentic round — chatWithToolsAnthropic, the path every
			//    build-turn round takes.
			_, _ = c.ChatWithTools(ctx, "system", nil, nil, nil)

			// 3. stream — streamAnthropic, the path plan mode takes.
			events := c.ChatStream(ctx, "system", nil)
			for {
				select {
				case _, ok := <-events:
					if !ok {
						goto streamed
					}
				case <-ctx.Done():
					t.Fatal("ChatStream never closed its channel")
				}
			}
		streamed:

			// 4. vision.
			_, _ = c.VisionCheck(ctx, []byte("fake-png-bytes"), "the home screen")

			paths := rec.served()
			if len(paths) != 4 {
				t.Fatalf("expected all four anthropic call sites to make a request, got %d: %v", len(paths), paths)
			}
			for i, p := range paths {
				if p != anthropicEndpointWant {
					t.Errorf("call site %d requested %q, want %q (base %q)", i+1, p, anthropicEndpointWant, tc.basePath)
				}
			}
		})
	}
}

// TestMessagesURLAcceptsBothRootSpellings pins the builder itself: the
// openai side's convention is a base that already carries /v1 (the .env
// ships `https://api.atria-asi.ai/v1`), Anthropic's own SDK convention
// is a bare root. A base ending in /v1 must not be doubled; a bare root
// must not be left short; and a trailing slash must not produce `//`.
func TestMessagesURLAcceptsBothRootSpellings(t *testing.T) {
	for _, tc := range []struct {
		base string
		want string
	}{
		{"https://gw.example/v1", "https://gw.example/v1/messages"},
		{"https://gw.example", "https://gw.example/v1/messages"},
		{"https://gw.example/v1/", "https://gw.example/v1/messages"},
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
	} {
		c := &Client{baseURL: tc.base}
		if got := c.messagesURL(); got != tc.want {
			t.Errorf("messagesURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}
