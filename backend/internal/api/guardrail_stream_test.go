package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seekersstudio/backend/internal/events"
)

// The guardrail path, end to end on the backend: a rejection emitted by the
// orchestrator must arrive at the IDE as a guardrail_reject SSE frame carrying
// the rule slug and the human reason. The frontend builds its "Blocked" row from
// exactly these fields (tool = rule, error = reason), so this is the contract
// between the two halves — if the field mapping drifts, the row goes blank.
func TestGuardrailRejectStreamsEndToEnd(t *testing.T) {
	em := events.NewSessionEmitter(1, 64)
	em.EmitGuardrail("browser-wallet-package",
		"`@solana/wallet-adapter-react` cannot run on the device — use MWA transact() instead.")

	var evt events.Event
	select {
	case evt = <-em.Events():
	default:
		t.Fatal("EmitGuardrail produced no event")
	}
	if evt.Type != events.GuardrailEvent {
		t.Fatalf("EmitGuardrail produced the wrong type: %s", evt.Type)
	}

	rec := httptest.NewRecorder()
	(&PromptHandler{}).writeBuildEvent(rec, evt)
	body := rec.Body.String()

	if !strings.Contains(body, "event: guardrail_reject") {
		t.Fatalf("wrong SSE event name:\n%s", body)
	}
	for _, want := range []string{
		`"tool":"browser-wallet-package"`, // the rule the row names
		`"status":"rejected"`,
		"MWA transact()", // the reason survives into the frame
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE frame missing %q:\n%s", want, body)
		}
	}
}
