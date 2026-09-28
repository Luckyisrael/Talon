package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// FM-TELa/FM-TELb/FM-TELc — written before the implementation (TEST_RULES §3).
//
// FM-TELa: a ledger that does not add up is worse than no ledger. Every
// bucket must be measured from the objects bound for the wire, and the
// named envelope must be exactly the remainder — a double-count (system
// counted twice, or a payload also counted as history) shows up as an
// envelope that does not match.
//
// FM-TELb: write payloads get their own bucket. The leak RESEARCH's P0.1
// names — full file bodies replayed every round — is invisible if
// write_file/try_edit inputs fold into generic history.
//
// FM-TELc: the ledger reads; it never writes. A mutated wire conversation
// would make every S10 number taken so far a lie.
func TestRequestLedgerAccountsEveryByte(t *testing.T) {
	payload := strings.Repeat("P", 2048)
	tryBody := strings.Repeat("T", 512)
	history := []Message{
		msg("user", text("build the home screen")),
		msg("assistant",
			writeCall("w1", "app/index.tsx", payload),
			Block{Type: "tool_use", ID: "g1", Name: "grep_codebase",
				Input: json.RawMessage(`{"pattern":"expo"}`)},
			Block{Type: "tool_use", ID: "t1", Name: "try_edit",
				Input: json.RawMessage(`{"path":"app/x.tsx","content":"` + tryBody + `"}`)},
		),
		msg("user", toolResult("w1"), toolResult("g1"), toolResult("t1")),
		msg("assistant", text(strings.Repeat("A", 512))),
		msg("user", text(strings.Repeat("U", 512))),
	}
	system := strings.Repeat("S", 4096)
	toolsJSON := strings.Repeat(`{"name":"tool"},`, 40)

	wantPayloads := len(findInput(t, history, "w1")) + len(findInput(t, history, "t1"))
	wantResults := 3 * len("ok")
	wantHistory := len("build the home screen") +
		len(`{"pattern":"expo"}`) + 512 + 512
	const wantEnvelope = 777
	body := len(system) + len(toolsJSON) + wantPayloads + wantResults +
		wantHistory + wantEnvelope

	led := AttributeRequest(system, history, len(toolsJSON), body)

	if led.System != len(system) {
		t.Errorf("system = %d, want %d", led.System, len(system))
	}
	if led.Tools != len(toolsJSON) {
		t.Errorf("tools = %d, want %d", led.Tools, len(toolsJSON))
	}
	if led.Payloads != wantPayloads {
		t.Errorf("payloads = %d, want %d (write_file AND try_edit bodies are payloads)", led.Payloads, wantPayloads)
	}
	if led.Results != wantResults {
		t.Errorf("results = %d, want %d", led.Results, wantResults)
	}
	if led.History != wantHistory {
		t.Errorf("history = %d, want %d (text + non-payload tool inputs only)", led.History, wantHistory)
	}
	if led.Envelope != wantEnvelope {
		t.Errorf("envelope = %d, want exactly %d — a different remainder means bytes were double-counted or dropped", led.Envelope, wantEnvelope)
	}
	if led.System+led.Tools+led.Payloads+led.Results+led.History+led.Envelope != led.Body {
		t.Errorf("buckets sum to %d, body = %d — the ledger does not account every byte",
			led.System+led.Tools+led.Payloads+led.Results+led.History+led.Envelope, led.Body)
	}
}

// FM-TELc: attribution observes, never acts — the conversation that goes
// to the provider must be byte-identical after the ledger runs over it.
func TestRequestLedgerDoesNotMutateWhatIsSent(t *testing.T) {
	history := []Message{
		msg("user", text("build the home screen")),
		msg("assistant", writeCall("w1", "app/index.tsx", strings.Repeat("P", 1024))),
		msg("user", toolResult("w1")),
		msg("assistant", text("done for now")),
	}
	before := make([]byte, len(mustMarshalJSON(history)))
	copy(before, mustMarshalJSON(history))

	AttributeRequest("system prompt", history, 64, 4096)

	if after := mustMarshalJSON(history); string(after) != string(before) {
		t.Fatalf("ledger mutated the wire conversation (%d → %d bytes) — measurement changed what is sent",
			len(before), len(after))
	}
}
