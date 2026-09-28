package ai

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

// FM-TELf — written first (TEST_RULES §3). The failure, measured in
// Run-7: the server log carried 60 request-ledger lines and ZERO
// est_input_tok= lines (Run-6: 59), because that log lives inside
// chatWithToolsOpenAI only. The scorecard therefore returned
// `? (needs --serverlog)` for S9 and S10, and under §5 an unmeasured
// required row blocks the run — the gap did not degrade Run-7, it
// voided two rows and made the run unusable as a comparison point.
//
// The law: one emitter, every agentic path, in the exact format the
// scorecard parses. The openai line's bytes must not move: the parser
// is a contract, and forking the format would fix the telemetry by
// breaking the reader.

// estInputLine is e2e/scorecard.py's regex for the line, transcribed —
// if this does not match what parse_serverlog looks for, S9/S10 read
// as unmeasured and the run is blocked.
var estInputLine = regexp.MustCompile(`est_input_tok=\d+ \(system=\d+ hist_msgs=\d+ tools=\d+ body_bytes=\d+\)`)

// captureLog returns what the standard logger writes during fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	fn()
	return buf.String()
}

// TestAnthropicPathEmitsTheEstimateLineTheScorecardParses drives a real
// anthropic agentic round and demands the estimate line on the wire —
// the Run-7 state (red) was no est_input_tok= line at all.
func TestAnthropicPathEmitsTheEstimateLineTheScorecardParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := &Client{
		protocol:   ProtocolAnthropic,
		baseURL:    srv.URL,
		apiKey:     "k",
		model:      "test-model",
		httpClient: http.DefaultClient,
	}

	out := captureLog(t, func() {
		if _, err := c.ChatWithTools(context.Background(), "system prompt", nil, nil, nil); err != nil {
			t.Errorf("ChatWithTools: %v", err)
		}
	})

	if !estInputLine.MatchString(out) {
		t.Errorf("the anthropic path emitted no est_input_tok= line the scorecard can parse:\n%s", out)
	}
}

// TestOpenAIEstimateLineFormatUnchanged is the keep-test: moving the
// log into a shared emitter must leave the openai line byte-identical,
// label included — drift here is silent, because the scorecard only
// cares that the pattern still matches.
func TestOpenAIEstimateLineFormatUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	c := &Client{
		protocol:   ProtocolOpenAI,
		baseURL:    srv.URL,
		apiKey:     "k",
		model:      "test-model",
		httpClient: http.DefaultClient,
	}

	out := captureLog(t, func() {
		if _, err := c.ChatWithTools(context.Background(), "system prompt", nil, nil, nil); err != nil {
			t.Errorf("ChatWithTools: %v", err)
		}
	})

	want := `ChatWithTools(OpenAI): model=test-model est_input_tok=`
	if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(out) {
		t.Errorf("the openai estimate line changed shape (scorecard contract):\ngot: %s", out)
	}
	if !estInputLine.MatchString(out) {
		t.Errorf("the openai estimate line no longer matches the scorecard regex:\n%s", out)
	}
}
