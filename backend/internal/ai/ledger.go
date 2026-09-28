package ai

// RequestLedger attributes one request body to its layers — L13's
// per-layer request telemetry (RESEARCH P0.4: "without this breakdown,
// it is easy to optimize the base prompt when the real cost is
// historical write payloads"). One line per model call answers "what is
// actually costing input tokens" instead of guessing from one number.
//
// The law (FM-TELa): buckets are measured from the same objects bound
// for the wire — the post-trim conversation, never the stored
// transcript — and Envelope is the NAMED remainder (JSON structure,
// request fields, cache markers, provider presentation deltas), so
// System+Tools+Payloads+Results+History+Envelope == Body holds exactly.
// Nothing is silently unattributed; nothing is counted twice. Size
// only: the ledger reads what is already being sent and never mutates
// it (FM-TELc).
type RequestLedger struct {
	Body     int // marshaled request size
	System   int // system prompt bytes
	Tools    int // serialized tool schemas as sent
	Payloads int // write-class tool_use bodies (FM-TELb)
	Results  int // tool_result content bytes
	History  int // all other message content: text, non-payload inputs, card, images
	Envelope int // named remainder: JSON structure + request fields
}

// ledgerPayloadTools are the payload-carrying write tools: L9's trim
// set plus try_edit, whose patch bodies replay under the patch-buffer
// lane. These bodies are RESEARCH P0.1's leak, so they get a bucket of
// their own instead of dissolving into generic history (FM-TELb).
var ledgerPayloadTools = map[string]bool{
	"write_file": true,
	"edit_file":  true,
	"multi_edit": true,
	"try_edit":   true,
}

// AttributeRequest sizes one request's layers. toolsBytes is the
// serialized tool schema length of the tools actually being sent
// (post-strip); body is the final marshaled request size. Unknown block
// types and presentation deltas (an "ERROR: " prefix on a failed result,
// an empty-result filler) land in Envelope — a named catch-all, never a
// silent gap.
func AttributeRequest(system string, history []Message, toolsBytes, body int) RequestLedger {
	led := RequestLedger{
		Body:   body,
		System: len(system),
		Tools:  toolsBytes,
	}
	for _, m := range history {
		for _, b := range m.Blocks {
			switch b.Type {
			case "text", "thinking":
				led.History += len(b.Text)
			case "context":
				led.History += len(b.Body)
			case "tool_use":
				if ledgerPayloadTools[b.Name] {
					led.Payloads += len(b.Input)
				} else {
					led.History += len(b.Input)
				}
			case "tool_result":
				led.Results += len(b.Content)
			case "image":
				led.History += len(b.Data)
			case "plan":
				led.History += len(b.Plan)
			default:
				// Unknown block type: its bytes fall through to Envelope,
				// which the sum below still accounts for (FM-TELa).
			}
		}
	}
	led.Envelope = body - (led.System + led.Tools + led.Payloads + led.Results + led.History)
	return led
}
