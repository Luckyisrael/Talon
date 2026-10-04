package api

import (
	"bytes"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
)

// FM-RAWa — written first (TEST_RULES §3). The raw gateway error is the
// only record that can say what actually happened: agentrouter's 401 body
// carried "unauthorized client detected" while the friendly rewrite it
// feeds every durable record said "check the server's AI_API_KEY" — a
// wrong instruction about a key that was never invalid (2026-10-03,
// prompts 84/85; stdout logged "model-request-failed" and nothing else).
// Red today: friendlyAIError returns the rewrite and logs nothing, so the
// log buffer stays empty.
func TestFriendlyErrorLeavesRawErrorInServerLog(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	raw := `AI service returned HTTP 401: {"error":{"message":"unauthorized client detected","type":"unauthorized_client_error"}}`
	got := friendlyAIError(errors.New(raw))

	if !strings.Contains(got, "credentials") {
		t.Errorf("the user-facing rewrite must stay the friendly mapping, got %q", got)
	}
	if got == raw {
		t.Errorf("the rewrite must not pass the raw error through to the user, got %q", got)
	}
	if !strings.Contains(buf.String(), "unauthorized client detected") {
		t.Errorf("FM-RAWa: the raw error must land on the server log before the rewrite softens it; log = %q", buf.String())
	}
}
