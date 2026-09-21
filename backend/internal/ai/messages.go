package ai

import (
	"encoding/json"
	"fmt"
)

// Block is one piece of a conversation turn. It is provider-neutral: the
// serialisers below translate it into Anthropic content blocks or OpenAI
// tool_calls / tool messages. Storing turns in this shape is what lets the
// agent replay a conversation verbatim across restarts and across providers.
type Block struct {
	Type string `json:"type"` // text | thinking | tool_use | tool_result | image | doc | plan | image

	// text / thinking
	Text string `json:"text,omitempty"`

	// image (user-attached). Stored blocks carry the reference
	// (attachment_id, plus the shared name field, and mime); Data holds
	// base64 bytes only in memory for the provider request and is never
	// serialised (json:"-"), so the database and the session log never hold
	// megabytes of pixels.
	AttachmentID int64  `json:"attachment_id,omitempty"`
	Mime         string `json:"mime,omitempty"`
	Data         string `json:"-"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_use (Gemini): Gemini's OpenAI-compatible endpoint returns a
	// thought_signature inside extra_content.google on every assistant message
	// that calls tools, and refuses to accept that turn again unless the
	// signature is echoed back. Dropping it at parse time is what surfaced as
	// "Function call is missing a thought_signature" on the second tool call.
	ThoughtSignature string `json:"thought_signature,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// plan (Talon plan-mode reply). Carries the structured app plan as
	// raw JSON so a reload can rebuild the plan card. Deliberately not sent to
	// the provider — the accompanying text block carries the replayable message,
	// and the model does not need its own plan echoed back.
	Plan json.RawMessage `json:"plan,omitempty"`

	// turn_end is Talon's terminal marker, modelled on DSH's turn/end with a
	// reason. It is written once, after the loop settles, and it is what makes
	// a failed or cancelled turn legible on reload: without it the transcript
	// holds the tool calls that ran but never says what happened to them, so a
	// turn that died looks the same as one still in flight. Status is
	// completed | failed | cancelled | interrupted; Error carries the provider
	// message on a failure; Rounds is how far it got.
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Rounds int    `json:"rounds,omitempty"`

	// context is machine text the orchestrator injects to steer the model —
	// an auto-attached skill body, or the repair feedback after a verify gate
	// fails. It is real context the model needs and must replay verbatim, but
	// it is not a message from the user, and rendering it as a chat bubble is
	// what put a 400-line skill document in the middle of the conversation.
	// The transcript shows only the one-line Title; Body replays to the model
	// and opens on demand.
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	// Kind names the injection source: "skill" for an auto-attached skill,
	// "verify" for a failed-gate repair note.
	Kind string `json:"kind,omitempty"`
}

// Message is one conversation turn.
type Message struct {
	Role   string  `json:"role"` // user | assistant
	Blocks []Block `json:"blocks"`
}

// Usage is the token accounting a provider reports for one request.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

// ── Constructors ──────────────────────────────────────

func UserText(text string) Message {
	return Message{Role: "user", Blocks: []Block{{Type: "text", Text: text}}}
}

func AssistantText(text string) Message {
	return Message{Role: "assistant", Blocks: []Block{{Type: "text", Text: text}}}
}

// AssistantPlan builds the assistant turn for a plan-mode reply. The text block
// carries the human-readable message — replayed to the model as context and
// shown as prose on reload. The plan block carries the structured plan as raw
// JSON purely so the UI can rebuild its plan card; it is dropped from provider
// replay (see toAnthropic / toOpenAI), since the model does not need its own
// plan echoed back.
func AssistantPlan(message string, planJSON json.RawMessage) Message {
	blocks := make([]Block, 0, 2)
	if message != "" {
		blocks = append(blocks, Block{Type: "text", Text: message})
	}
	if len(planJSON) > 0 {
		blocks = append(blocks, Block{Type: "plan", Plan: planJSON})
	}
	return Message{Role: "assistant", Blocks: blocks}
}

// ToolResults wraps a batch of results as the single user turn that answers an
// assistant turn's tool_use blocks. Both providers require every tool call from
// the previous turn to be answered before the next assistant turn.
func ToolResults(results []Block) Message {
	return Message{Role: "user", Blocks: results}
}

// Context is an orchestrator injection — skill guidance or verify-gate repair
// feedback — that the model needs verbatim but the user never typed. It rides
// a user-role turn so the providers accept it in the tool_results→user shape,
// but the block is what keeps it out of the chat bubble: the transcript shows
// only the one-line Title, and Body stays collapsed until it is asked for.
// Kind is "skill" or "verify".
func Context(kind, title, body string) Block {
	return Block{Type: "context", Kind: kind, Title: title, Body: body}
}

// Text concatenates the message's text blocks.
func (m Message) Text() string {
	var s string
	for _, b := range m.Blocks {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

// ToolUses returns the tool_use blocks in the message.
func (m Message) ToolUses() []Block {
	var out []Block
	for _, b := range m.Blocks {
		if b.Type == "tool_use" {
			out = append(out, b)
		}
	}
	return out
}

// ── Persistence ───────────────────────────────────────

// MarshalBlocks serialises a turn's blocks for storage.
func MarshalBlocks(blocks []Block) string {
	data, err := json.Marshal(blocks)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// UnmarshalBlocks restores blocks from storage.
func UnmarshalBlocks(s string) []Block {
	var blocks []Block
	if json.Unmarshal([]byte(s), &blocks) != nil {
		return nil
	}
	return blocks
}

// ── Anthropic serialisation ───────────────────────────

// anthropicContentBlock mirrors the Messages API content-block shape. Input must be a
// JSON object (not a string), and tool_result content is a plain string here.
type anthropicContentBlock struct {
	Type         string                `json:"type"`
	Text         string                `json:"text,omitempty"`
	ID           string                `json:"id,omitempty"`
	Name         string                `json:"name,omitempty"`
	Input        json.RawMessage       `json:"input,omitempty"`
	ToolUseID    string                `json:"tool_use_id,omitempty"`
	Content      string                `json:"content,omitempty"`
	IsError      bool                  `json:"is_error,omitempty"`
	Source       *anthropicImageSource `json:"source,omitempty"`
	CacheControl *cacheControl         `json:"cache_control,omitempty"`
}

// anthropicImageSource is the base64 payload of an image content block.
type anthropicImageSource struct {
	Type      string `json:"type"` // base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type anthropicBlockMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

// toAnthropic converts messages to Messages API form. cacheBreakpoint marks the
// last block of that message index with cache_control so the stable prefix of
// the conversation is cached; pass -1 to disable.
func toAnthropic(msgs []Message, cacheBreakpoint int) []anthropicBlockMessage {
	out := make([]anthropicBlockMessage, 0, len(msgs))
	for i, m := range msgs {
		blocks := make([]anthropicContentBlock, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				if b.Text == "" {
					continue
				}
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: b.Text})
			case "thinking":
				// Thinking blocks are not replayed: without the original
				// signature the API rejects them.
				continue
			case "tool_use":
				input := b.Input
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicContentBlock{
					Type: "tool_use", ID: b.ID, Name: b.Name, Input: input,
				})
			case "tool_result":
				blocks = append(blocks, anthropicContentBlock{
					Type: "tool_result", ToolUseID: b.ToolUseID,
					Content: b.Content, IsError: b.IsError,
				})
			case "image":
				// Image bytes ride resolved Data; a block that lost its file
				// (deleted attachment) degrades to a named text note rather
				// than failing the whole request.
				if b.Data == "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: "[attached image " + b.Name + " is unavailable — the file is missing]"})
					continue
				}
				mime := b.Mime
				if mime == "" {
					mime = "image/png"
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:   "image",
					Source: &anthropicImageSource{Type: "base64", MediaType: mime, Data: b.Data},
				})
			case "context":
				// Real guidance the model must see, just not chat. Replay the body
				// as plain text; the title is already its first line.
				if b.Body != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: b.Body})
				}
			}
		}
		if len(blocks) == 0 {
			// An empty content array is a 400. Keep the turn well-formed.
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: "(no content)"})
		}
		if i == cacheBreakpoint {
			blocks[len(blocks)-1].CacheControl = &cacheControl{Type: "ephemeral"}
		}
		out = append(out, anthropicBlockMessage{Role: m.Role, Content: blocks})
	}
	return out
}

// ── OpenAI serialisation ──────────────────────────────

type openaiToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolCallFunc `json:"function"`
	// Gemini requires the thought_signature it issued on a tool call part to be
	// echoed back on that exact part. Parallel calls only sign the first part,
	// so the signature must stay positional — hoisting it to the message would
	// fail the next request ("missing a thought_signature ... position N").
	ExtraContent map[string]any `json:"extra_content,omitempty"`
}

type openaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openaiChatMessage covers all four OpenAI roles. Content is `any` because an
// assistant turn that only calls tools must send content:null, not "".
type openaiChatMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"`
	ToolCalls  []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// cacheableText wraps a string as an OpenAI content-parts array carrying an
// ephemeral cache_control marker. This is the OpenAI-side equivalent of the
// cache_control set on the Anthropic system block: OpenRouter reads it to place
// a prompt-cache breakpoint for the providers that support caching, and the
// providers that cache automatically ignore the extra field, so it degrades
// safely to a plain text part.
func cacheableText(text string) []map[string]any {
	return []map[string]any{
		{
			"type":          "text",
			"text":          text,
			"cache_control": map[string]any{"type": "ephemeral"},
		},
	}
}

// toOpenAI converts messages to Chat Completions form. The shape differs
// structurally from Anthropic's: tool results are separate messages with
// role:"tool" rather than blocks inside a user turn, so one Message can expand
// into several openaiChatMessages.
//
// The system prompt is sent as a content-parts array carrying an ephemeral
// cache_control marker, mirroring the Anthropic path. It is the largest block
// that repeats verbatim every round, so caching it is where the round-over-round
// token savings live. OpenRouter reads the marker for the providers that support
// prompt caching (Anthropic, Gemini) and ignores it for the ones that cache
// automatically (OpenAI, DeepSeek), so it is safe to send unconditionally.
func toOpenAI(system string, msgs []Message) []openaiChatMessage {
	out := make([]openaiChatMessage, 0, len(msgs)+1)
	if system != "" {
		out = append(out, openaiChatMessage{Role: "system", Content: cacheableText(system)})
	}

	for _, m := range msgs {
		var text string
		var calls []openaiToolCall
		var results []openaiChatMessage
		var images []Block

		for _, b := range m.Blocks {
			switch b.Type {
			case "text":
				text += b.Text
			case "tool_use":
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				call := openaiToolCall{
					ID:       b.ID,
					Type:     "function",
					Function: openaiToolCallFunc{Name: b.Name, Arguments: args},
				}
				if b.ThoughtSignature != "" {
					call.ExtraContent = map[string]any{
						"google": map[string]any{"thought_signature": b.ThoughtSignature},
					}
				}
				calls = append(calls, call)
			case "tool_result":
				content := b.Content
				if b.IsError && content != "" {
					content = "ERROR: " + content
				}
				if content == "" {
					content = "(empty)"
				}
				results = append(results, openaiChatMessage{
					Role: "tool", Content: content, ToolCallID: b.ToolUseID,
				})
			case "context":
				// Real guidance the model must see, just not chat: replay the
				// body as ordinary user text.
				if b.Body != "" {
					text += b.Body
				}
			case "image":
				// Unresolved images (missing file) are dropped here; the
				// Anthropic path names them instead. A user turn always
				// carries prompt text, so the turn itself survives.
				if b.Data != "" {
					images = append(images, b)
				}
			}
		}

		if m.Role == "assistant" {
			if text != "" || len(calls) > 0 {
				msg := openaiChatMessage{Role: "assistant", ToolCalls: calls}
				if text != "" {
					msg.Content = text
				} else {
					// Required by the spec for tool-only assistant turns.
					msg.Content = nil
				}
				out = append(out, msg)
			}
		} else {
			// Tool results must come before any accompanying user prose so they
			// sit directly after the assistant turn that requested them.
			out = append(out, results...)
			results = nil
			switch {
			case len(images) > 0:
				// A user turn with attached images goes out as a content-parts
				// array: text first, then one image_url part per image.
				parts := make([]map[string]any, 0, len(images)+1)
				parts = append(parts, map[string]any{"type": "text", "text": text})
				for _, im := range images {
					mime := im.Mime
					if mime == "" {
						mime = "image/png"
					}
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": "data:" + mime + ";base64," + im.Data},
					})
				}
				out = append(out, openaiChatMessage{Role: "user", Content: parts})
			case text != "":
				out = append(out, openaiChatMessage{Role: "user", Content: text})
			}
		}
		out = append(out, results...)
	}
	return out
}

// ── Compaction ────────────────────────────────────────

// approxTokens estimates a message's token cost. Deliberately cheap: 1 token
// per 3.5 characters is close enough to decide when to compact. Resolved image
// bytes count at full weight — pixels cost context on the wire, and pretending
// otherwise would let an image-heavy history sail past the compaction budget.
func approxTokens(m Message) int {
	n := 8 // per-message overhead
	for _, b := range m.Blocks {
		n += (len(b.Text) + len(b.Content) + len(b.Input) + len(b.Name) + len(b.Data)) / 3
	}
	return n
}

// EstimateTokens approximates the token cost of a conversation.
func EstimateTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += approxTokens(m)
	}
	return total
}

// Compact drops the oldest turns until the conversation fits maxTokens, then
// prepends a summary of what was dropped. It never splits a tool_use turn from
// the tool_result turn that answers it — both providers reject a tool_result
// with no matching call, and an assistant tool_use with no result.
func Compact(msgs []Message, maxTokens int) []Message {
	if EstimateTokens(msgs) <= maxTokens || len(msgs) <= 2 {
		return msgs
	}

	// Always keep the first turn: it carries the original request.
	first := msgs[0]
	rest := msgs[1:]

	// Walk backwards accumulating turns that fit, then snap the cut forward to
	// the next user turn that is not a tool_result batch.
	budget := maxTokens - approxTokens(first) - 200 // headroom for the summary
	keepFrom := len(rest)
	used := 0
	for i := len(rest) - 1; i >= 0; i-- {
		t := approxTokens(rest[i])
		if used+t > budget {
			break
		}
		used += t
		keepFrom = i
	}

	for keepFrom < len(rest) && !isCleanBoundary(rest[keepFrom]) {
		keepFrom++
	}
	if keepFrom >= len(rest) {
		// Nothing can be kept cleanly; fall back to the first turn alone.
		return []Message{first}
	}

	dropped := keepFrom
	out := make([]Message, 0, len(rest)-keepFrom+2)
	out = append(out, first)
	out = append(out, UserText(fmt.Sprintf(
		"[Earlier context compacted: %d turns removed to stay within the context window. "+
			"The files already written are on disk — use read_file if you need to see them.]", dropped)))
	out = append(out, AssistantText("Understood. Continuing from the current state of the files."))
	out = append(out, rest[keepFrom:]...)
	return out
}

// isCleanBoundary reports whether a conversation can resume at this message
// without orphaning a tool call.
func isCleanBoundary(m Message) bool {
	if m.Role != "user" {
		return false
	}
	for _, b := range m.Blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}

// TrimToValidHistory makes a stored conversation safe to replay.
//
// A history read back from the database can be ragged at both ends: the oldest
// turns may have been trimmed away mid tool call, and the newest turn may be an
// assistant tool_use whose results were never written because the process died.
// Both providers reject either shape outright, which would fail every subsequent
// request for that project. So cut the ends back to a clean boundary.
func TrimToValidHistory(msgs []Message) []Message {
	// Front: the first turn must be a user turn that is not a batch of tool
	// results answering a call we no longer have.
	start := 0
	for start < len(msgs) && !isCleanBoundary(msgs[start]) {
		start++
	}
	if start >= len(msgs) {
		return nil
	}
	msgs = msgs[start:]

	// Back: drop trailing turns until no tool_use is left unanswered, and until
	// the history ends on an assistant turn. The caller appends the new prompt as
	// a user turn straight after this, and a stored history that already ends with
	// a user turn would put two of them back to back — which Anthropic rejects
	// outright, and which shows the model an abandoned request as if it were still
	// waiting to be answered.
	end := len(msgs)
	for end > 0 {
		if !hasUnansweredToolUse(msgs[:end]) && msgs[end-1].Role == "assistant" {
			break
		}
		end--
	}
	if end <= 0 {
		return nil
	}

	out := make([]Message, end)
	copy(out, msgs[:end])
	return out
}

// hasUnansweredToolUse reports whether the conversation ends with a tool call
// that has no matching result.
func hasUnansweredToolUse(msgs []Message) bool {
	pending := make(map[string]bool)
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch b.Type {
			case "tool_use":
				pending[b.ID] = true
			case "tool_result":
				delete(pending, b.ToolUseID)
			}
		}
	}
	return len(pending) > 0
}

// ── Superseded-output trimming ─────────────────────────

// supersedableTools are the read-only, latest-wins calls whose older results
// turn into dead weight the moment the same target is fetched again in the same
// turn: only the newest fetch has the current bytes, yet every stale copy is
// replayed in full to the provider on every subsequent round. Writes and
// one-shot actions are never touched — their results are not interchangeable.
var supersedableTools = map[string]bool{
	"read_file":  true,
	"list_files": true,
}

// minTrimmableResult is the result size, in bytes, below which a superseded
// result is left alone. Trimming a small result saves almost nothing, and on a
// caching provider a change deep in the replayed prefix can cost a re-cache of
// everything after it — so a result has to be genuinely large to be worth
// collapsing.
const minTrimmableResult = 800

// TrimSupersededToolOutput returns a view of msgs in which a large read_file or
// list_files result is collapsed to a one-line pointer when the SAME target is
// fetched again later in the conversation. Only the newest fetch of each target
// keeps its full body; the earlier ones — otherwise re-sent verbatim every round
// for the life of the turn — shrink to a line that names the target and points
// at the newer result.
//
// Validity is preserved exactly: the tool_result stays in place with its
// ToolUseID intact — dropping it would orphan the tool_use and both providers
// reject that with a 400 — and only its Content changes. The result shares the
// caller's Message and Block values everywhere except the handful of blocks it
// rewrites, and it rewrites those on fresh copies, so the caller's stored,
// full-fidelity transcript is never mutated. When nothing repeats, msgs is
// returned unchanged with no allocation.
func TrimSupersededToolOutput(msgs []Message) []Message {
	// id → what that call fetched, for the supersedable tools only.
	type target struct{ tool, path string }
	fetchedBy := make(map[string]target)
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_use" && supersedableTools[b.Name] {
				fetchedBy[b.ID] = target{tool: b.Name, path: toolPath(b.Input)}
			}
		}
	}
	if len(fetchedBy) == 0 {
		return msgs
	}

	// The survivor for each target is the last call that actually has a result;
	// results are appended in order, so the last one seen wins.
	survivor := make(map[target]string)
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "tool_result" {
				if t, ok := fetchedBy[b.ToolUseID]; ok {
					survivor[t] = b.ToolUseID
				}
			}
		}
	}

	// A tool_result is superseded when its call fetched a target that a later call
	// fetched too, and it is worth collapsing only when it is a large, non-error
	// body — an error is small and carries information the newer read does not.
	superseded := func(b Block) bool {
		if b.Type != "tool_result" || b.IsError || len(b.Content) < minTrimmableResult {
			return false
		}
		t, ok := fetchedBy[b.ToolUseID]
		return ok && survivor[t] != b.ToolUseID
	}

	any := false
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if superseded(b) {
				any = true
				break
			}
		}
		if any {
			break
		}
	}
	if !any {
		return msgs
	}

	out := make([]Message, len(msgs))
	copy(out, msgs)
	for mi := range out {
		var blocks []Block // copied lazily, only if this message has something to trim
		for bi, b := range out[mi].Blocks {
			if !superseded(b) {
				continue
			}
			if blocks == nil {
				blocks = make([]Block, len(out[mi].Blocks))
				copy(blocks, out[mi].Blocks)
			}
			t := fetchedBy[b.ToolUseID]
			blocks[bi].Content = supersededPointer(t.tool, t.path)
		}
		if blocks != nil {
			out[mi].Blocks = blocks
		}
	}
	return out
}

// toolPath pulls the fetch target out of a tool_use input. Two calls count as the
// same fetch only when their path matches; an unparseable or path-less input
// yields "" and so simply never matches another call under a different tool.
func toolPath(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &a)
	return a.Path
}

// supersededPointer is the one-line stand-in left where a stale result used to
// be. It names the tool and target so the model can see what was dropped and, if
// it genuinely needs the bytes, that a newer copy is present further down.
func supersededPointer(tool, path string) string {
	if path != "" {
		return fmt.Sprintf("(%s output for %q omitted here — it was fetched again later in this turn; the current contents are in that newer result below.)", tool, path)
	}
	return fmt.Sprintf("(%s output omitted here — superseded by a later call in this turn.)", tool)
}

// ── Legacy bridge ─────────────────────────────────────

// FromPromptMessages adapts the flat {Role, Content} shape used by the
// non-tool Plan and Chat paths.
func FromPromptMessages(msgs []PromptMessage) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, Message{
			Role:   m.Role,
			Blocks: []Block{{Type: "text", Text: m.Content}},
		})
	}
	return out
}
