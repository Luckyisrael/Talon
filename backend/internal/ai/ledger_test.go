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
//
// FM-TELe (L19), written after Run-4 caught the law broken in
// production: attribution must read the MARSHALED BODY, not the stored
// transcript — the serializer drops `thinking` (both providers) and
// `plan` blocks, yet L13 counted their bytes in `history` and never
// sent them, and Run-4's live line shows `envelope=-1894 → -26156`: a
// remainder named "JSON structure" cannot go negative without the
// account lying. Bucket spans must therefore be disjoint subtrees of
// the body itself, so `envelope >= 0` holds by construction and a
// block the wire never carries cannot be counted at all.
func TestRequestLedgerAccountsEveryByte(t *testing.T) {
	// A byte-for-byte request body — the parser carves exact, disjoint
	// spans out of what the provider will read, and nothing is measured
	// from anywhere else. Fixtures are fixed-size so every expected
	// bucket is hand-computed from the pieces the body is built from.
	system := strings.Repeat("S", 64)
	payload := strings.Repeat("P", 2048)
	tryPayload := strings.Repeat("T", 512)

	sysContent, _ := json.Marshal(system)
	userText, _ := json.Marshal("build the home screen")
	assistantText, _ := json.Marshal(strings.Repeat("A", 512))
	toolResultSpan := `"ok"` // a tool message's content span, quotes included
	toolsArr := `[{"name":"grep_codebase","parameters":{"type":"object"}}]`

	writeInput := `{"path":"app/index.tsx","content":"` + payload + `"}`
	tryInput := `{"path":"app/x.tsx","content":"` + tryPayload + `"}`
	grepInput := `{"pattern":"expo"}`
	// arguments ride the wire as JSON-encoded strings of the input JSON.
	writeArgs, _ := json.Marshal(writeInput)
	tryArgs, _ := json.Marshal(tryInput)
	grepArgs, _ := json.Marshal(grepInput)

	body := `{"model":"m","max_tokens":10,"messages":[` +
		`{"role":"system","content":` + string(sysContent) + `},` +
		`{"role":"user","content":` + string(userText) + `},` +
		`{"role":"assistant","content":null,"tool_calls":[` +
		`{"id":"w1","type":"function","function":{"name":"write_file","arguments":` + string(writeArgs) + `}},` +
		`{"id":"t1","type":"function","function":{"name":"try_edit","arguments":` + string(tryArgs) + `}},` +
		`{"id":"g1","type":"function","function":{"name":"grep_codebase","arguments":` + string(grepArgs) + `}}]},` +
		`{"role":"tool","tool_call_id":"w1","content":` + toolResultSpan + `},` +
		`{"role":"assistant","content":` + string(assistantText) + `}],` +
		`"tools":` + toolsArr + `}`
	if !json.Valid([]byte(body)) {
		t.Fatalf("fixture body is not valid JSON: %.200s", body)
	}

	led := AttributeRequestBody([]byte(body))

	wantSystem := len(sysContent)
	wantTools := len(toolsArr)
	wantPayloads := len(writeArgs) + len(tryArgs) // write_file AND try_edit are payloads
	wantResults := len(toolResultSpan)
	wantHistory := len(userText) + len(grepArgs) + len(assistantText)
	wantEnvelope := len(body) - (wantSystem + wantTools + wantPayloads + wantResults + wantHistory)

	if led.System != wantSystem {
		t.Errorf("system = %d, want %d", led.System, wantSystem)
	}
	if led.Tools != wantTools {
		t.Errorf("tools = %d, want %d", led.Tools, wantTools)
	}
	if led.Payloads != wantPayloads {
		t.Errorf("payloads = %d, want %d (write_file AND try_edit bodies are payloads)", led.Payloads, wantPayloads)
	}
	if led.Results != wantResults {
		t.Errorf("results = %d, want %d", led.Results, wantResults)
	}
	if led.History != wantHistory {
		t.Errorf("history = %d, want %d (user/assistant text + non-payload tool arguments only)", led.History, wantHistory)
	}
	if led.Envelope != wantEnvelope {
		t.Errorf("envelope = %d, want exactly %d — a different remainder means spans were double-counted or dropped", led.Envelope, wantEnvelope)
	}
	if led.Envelope < 0 {
		t.Errorf("envelope = %d — bucket spans exceed the body they were carved from", led.Envelope)
	}
}

// FM-TELe: the production failure as a test. A transcript full of
// thinking and plan blocks — what Run-4's multi-minute silent rounds
// produced — goes out through toOpenAI, which drops both. The ledger,
// reading the marshaled body, must count neither: the envelope stays
// non-negative and the thinking bytes stay out of every bucket.
// Red before the fix: attribution read the transcript, so the 10 KB of
// dropped thinking landed in `history` and drove the envelope to
// -26156 on Run-4's line.
func TestRequestLedgerIgnoresBlocksTheSerializerDrops(t *testing.T) {
	thinking := strings.Repeat("THINK", 2048) // 10 KB both serializers drop
	planBlob := strings.Repeat("PLAN", 1024)  // 4 KB: no serializer case for it
	history := []Message{
		msg("user", text("build the home screen")),
		msg("assistant",
			Block{Type: "thinking", Text: thinking},
			writeCall("w1", "app/index.tsx", strings.Repeat("P", 1024)),
			Block{Type: "plan", Plan: json.RawMessage(planBlob)}),
		msg("user", toolResult("w1")),
		msg("assistant", text("done")),
	}
	messages := toOpenAI("system prompt", history)
	body, err := json.Marshal(map[string]any{
		"model": "m", "max_tokens": 10, "messages": messages,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if s := string(body); strings.Contains(s, "THINK") || strings.Contains(s, "PLAN") {
		t.Fatal("fixture invalid: toOpenAI must drop thinking and plan blocks for this test to mean anything")
	}

	led := AttributeRequestBody(body)

	if led.Envelope < 0 {
		t.Errorf("envelope = %d — the ledger counted bytes that never reach the wire (FM-TELe)", led.Envelope)
	}
	if led.History >= len(thinking) {
		t.Errorf("history = %d — the dropped thinking's bytes leaked into a bucket", led.History)
	}
	if led.System == 0 {
		t.Error("the system message was not attributed — the parser missed the wire's system role")
	}
	if led.Payloads == 0 {
		t.Error("the write_file arguments were not attributed — FM-TELb's payload bucket is missing")
	}
}

// FM-TELc: attribution observes, never acts — the body that goes to the
// provider must be byte-identical after the ledger reads it.
func TestRequestLedgerDoesNotMutateWhatIsSent(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10,"messages":[` +
		`{"role":"system","content":"system prompt"},` +
		`{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":null,"tool_calls":[` +
		`{"id":"w1","type":"function","function":` +
		`{"name":"write_file","arguments":"{\"path\":\"a.ts\",\"content\":\"x\"}"}}]},` +
		`{"role":"tool","tool_call_id":"w1","content":"ok"}],"tools":[]}`)
	before := append([]byte(nil), body...)

	AttributeRequestBody(body)

	if string(body) != string(before) {
		t.Fatalf("ledger mutated the request body (%d → %d bytes) — measurement changed what is sent",
			len(before), len(body))
	}
}
