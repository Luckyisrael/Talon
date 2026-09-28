package ai

import "encoding/json"

// RequestLedger attributes one request body to its layers — L13's
// per-layer request telemetry (RESEARCH P0.4: "without this breakdown,
// it is easy to optimize the base prompt when the real cost is
// historical write payloads"). One line per model call answers "what is
// actually costing input tokens" instead of guessing from one number.
//
// The law (FM-TELa, corrected by FM-TELe): buckets are disjoint byte
// spans carved out of the MARSHALED BODY — the only object that is the
// wire truth — so System+Tools+Payloads+Results+History+Envelope ==
// Body holds exactly and Envelope (the named remainder: JSON
// structure, request fields, cache markers, presentation deltas) is
// >= 0 by construction. L13 originally measured the stored transcript,
// but the serializer is the gate: toOpenAI and toAnthropic both drop
// `thinking`, both drop `plan`, and Run-4 caught the result —
// envelope=-1894 → -26156, bytes counted that never went out. Reading
// the body itself makes that class of lie impossible: content the wire
// does not carry is not in the body, so it cannot be counted. Size
// only: the ledger reads what is already being sent and never mutates
// it (FM-TELc).
type RequestLedger struct {
	Body     int // marshaled request size
	System   int // system prompt bytes (top-level field or system message)
	Tools    int // serialized tool schemas as sent
	Payloads int // write-class tool_use bodies (FM-TELb)
	Results  int // tool_result content bytes
	History  int // all other message content: text, non-payload inputs, images
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

// AttributeRequestBody sizes one request's layers by reading the
// marshaled body — both provider paths pass the exact bytes they are
// about to send, and one attributer serves both. A body that will not
// parse attributes nothing: every byte falls to the named envelope,
// which still sums exactly and invents nothing.
func AttributeRequestBody(body []byte) RequestLedger {
	led := RequestLedger{Body: len(body)}
	var top struct {
		System   json.RawMessage   `json:"system"`   // Anthropic's system field
		Messages []json.RawMessage `json:"messages"` // both paths
		Tools    json.RawMessage   `json:"tools"`    // both paths
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return led
	}
	if rawLen(top.System) > 0 {
		led.System += rawLen(top.System)
	}
	if rawLen(top.Tools) > 0 {
		led.Tools += rawLen(top.Tools)
	}
	for _, mraw := range top.Messages {
		attributeMessage(mraw, &led)
	}
	led.Envelope = led.Body - (led.System + led.Tools + led.Payloads + led.Results + led.History)
	return led
}

// attributeMessage buckets one serialized message's spans: the system
// message feeds the system bucket, an OpenAI role-"tool" message's
// content and an Anthropic tool_result block feed results, write-class
// tool arguments feed payloads (anything else's feed history), and all
// remaining content spans — text, images, block wrappers — are
// history. Unknown shapes fall through to the envelope (named, never
// silent).
func attributeMessage(mraw json.RawMessage, led *RequestLedger) {
	var m struct {
		Role      string            `json:"role"`
		Content   json.RawMessage   `json:"content"`
		ToolCalls []json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal(mraw, &m) != nil {
		return
	}
	switch {
	case m.Role == "system" && rawLen(m.Content) > 0: // OpenAI's system message
		led.System += rawLen(m.Content)
	case m.Role == "tool" && rawLen(m.Content) > 0: // OpenAI tool-result message
		led.Results += rawLen(m.Content)
	case len(m.ToolCalls) > 0: // OpenAI assistant with calls
		for _, craw := range m.ToolCalls {
			var call struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			}
			if json.Unmarshal(craw, &call) != nil || rawLen(call.Function.Arguments) == 0 {
				continue
			}
			attributeArguments(call.Function.Name, call.Function.Arguments, led)
		}
		led.History += rawLen(m.Content) // the assistant's own text, if any
	default: // user/assistant content: string, or a content-block array
		if len(m.Content) > 0 && m.Content[0] == '[' {
			var blocks []json.RawMessage
			if json.Unmarshal(m.Content, &blocks) != nil {
				return
			}
			for _, braw := range blocks {
				attributeBlock(braw, led)
			}
			return
		}
		led.History += rawLen(m.Content)
	}
}

// attributeBlock buckets one Anthropic-style content block (OpenAI's
// user content-parts ride here too): tool_use inputs split by tool
// name, tool_result content to results, everything else — text,
// thinking (Anthropic replays it, so it is on the wire), images,
// part wrappers — to history.
func attributeBlock(braw json.RawMessage, led *RequestLedger) {
	var b struct {
		Type    string          `json:"type"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(braw, &b) != nil {
		return
	}
	switch b.Type {
	case "tool_use":
		if rawLen(b.Input) > 0 {
			attributeArguments(b.Name, b.Input, led)
		}
	case "tool_result":
		led.Results += rawLen(b.Content)
	default:
		led.History += rawLen(braw)
	}
}

// attributeArguments buckets one tool call's argument bytes: the
// write-class bodies (FM-TELb) get the payloads bucket, every other
// tool's arguments are ordinary history.
func attributeArguments(name string, args json.RawMessage, led *RequestLedger) {
	if ledgerPayloadTools[name] {
		led.Payloads += rawLen(args)
	} else {
		led.History += rawLen(args)
	}
}

// rawLen is a span's byte count; an absent or null value contributes
// nothing (its bytes are structure and belong to the envelope).
func rawLen(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	return len(raw)
}
