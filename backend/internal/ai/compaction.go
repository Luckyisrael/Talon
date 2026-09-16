package ai

// compaction.go — the model-written context checkpoint.
//
// When the conversation exceeds the context window the oldest turns have to go.
// The cheap answer is to drop them and tell the model "earlier context was
// compacted" — which preserves nothing: every reason the model was working,
// every error it hit, every decision it made is gone, and only the files on
// disk remain. The model must re-derive intent from output it cannot see.
//
// The better answer, the one DSH uses, is to ask the model to write the
// checkpoint itself: one auxiliary call over the to-be-dropped span that
// produces a bounded summary in a fixed shape. The next request then carries
// the summary in place of the span. The auxiliary call is given the same
// system prompt and the dropped turns verbatim, so it is a genuine prefix of
// the conversation and reuses the provider's warm prefix cache — only the
// instruction and the output are uncached.
//
// The fixed sections are the contract: a reader (the next request) knows where
// to look for what, and the writer cannot ramble. Empty sections say "(none)"
// rather than vanishing, so the shape never changes.

import (
	"context"
	"fmt"
	"strings"
)

// compactionInstruction is the prompt for the auxiliary summarization call. It
// is deliberately prescriptive: the summary is the model's only memory of the
// dropped span, so the instruction pins what must survive and what must not.
const compactionInstruction = `This is an automatically generated checkpoint condensing an earlier span of the conversation to free up context. Treat the captured context as established background and build on it without restating it. Continue the task directly from the messages that follow, without acknowledging this checkpoint.

Summarize the conversation above as a checkpoint with exactly these sections, in this order, each as a markdown heading:

Primary Request and Intent
Key Technical Concepts
Files and Code
Errors and Fixes
Pending Jobs
Current Work
Next Step
Critical Context

Rules:
- Terse bullets. Never drop a section; a section with nothing to say writes "(none)".
- Preserve exact paths, command strings, error strings, identifiers, and numerics. A paraphrased path is a wrong path.
- Name the files that were written or changed and what each one does in one line.
- Errors and Fixes names each failure and the fix that resolved it — including fixes that failed and were abandoned, so they are not retried.
- Pending Jobs and Current Work are what the next request must be able to pick up cold.
- Critical Context holds anything the next request would otherwise get wrong: a decision already made, a constraint the user stated, a tool that does not work here.
- Do not mention this summarization request. Output only the checkpoint text.
- If a prior checkpoint is present in the conversation, consolidate it into this one — do not copy it forward verbatim.`

// minDroppedToSummarize is the floor below which summarizing is not worth a
// model call — a two-turn drop is cheaper to keep or lose than to summarize.
const minDroppedToSummarize = 4

// PlanCompaction reports the span a compaction would drop, without doing it.
// keepFrom is the index of the first message that survives; dropped is the span
// before it. The first message is always kept — it carries the original
// request. keepFrom 0 means nothing can be dropped cleanly.
func PlanCompaction(msgs []Message, maxTokens int) (keepFrom int, dropped []Message) {
	if len(msgs) <= 2 || EstimateTokens(msgs) <= maxTokens {
		return 0, nil
	}
	first := msgs[0]
	rest := msgs[1:]

	// Walk backwards accumulating turns that fit, then snap the cut forward to
	// the next clean boundary — the same logic Compact uses, mirrored here so
	// the summarizer sees exactly the span that will be dropped.
	budget := maxTokens - approxTokens(first) - 200
	keepFrom = len(rest)
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
	if keepFrom <= 0 || keepFrom >= len(rest) {
		return 0, nil
	}
	return keepFrom + 1, rest[:keepFrom]
}

// SummarizeForCompaction writes the checkpoint over a to-be-dropped span. It is
// the auxiliary call: same system prompt, dropped turns verbatim, one extra
// instruction. An error or an unusable answer returns "" and the caller falls
// back to the static one-line summary — a degraded checkpoint beats no
// compaction.
func (c *Client) SummarizeForCompaction(ctx context.Context, system string, dropped []Message) (string, error) {
	if len(dropped) < minDroppedToSummarize {
		return "", nil
	}
	history, ok := flatPromptMessages(dropped)
	if !ok {
		// Nothing in the span was readable at all: tool turns whose results are
		// also empty. There is nothing to summarize.
		return "", nil
	}
	history = append(history, PromptMessage{Role: "user", Content: compactionInstruction})

	out, err := c.chat(ctx, system, history)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if !looksLikeCheckpoint(out) {
		return "", fmt.Errorf("the compaction summary did not carry the checkpoint sections")
	}
	return out, nil
}

// flatPromptMessages converts rich messages to the flat pair the auxiliary call
// takes. Tool turns carry no text blocks — their results do — so the result
// text stands in for the message. A message with nothing readable is dropped
// rather than sent as an empty message, which both providers reject. ok is
// false when the whole span was unreadable.
func flatPromptMessages(msgs []Message) (out []PromptMessage, ok bool) {
	out = make([]PromptMessage, 0, len(msgs))
	for _, m := range msgs {
		text := m.Text()
		if strings.TrimSpace(text) == "" {
			var parts []string
			for _, b := range m.Blocks {
				if b.Type == "tool_result" && b.Text != "" {
					parts = append(parts, b.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, PromptMessage{Role: m.Role, Content: text})
	}
	return out, len(out) > 0
}

// looksLikeCheckpoint checks the shape, not the content: the first required
// section must appear near the top. A summary that lost the shape is not a
// checkpoint the next request can read, and the fallback is used instead.
func looksLikeCheckpoint(s string) bool {
	head := s
	if len(head) > 400 {
		head = head[:400]
	}
	return strings.Contains(head, "Primary Request and Intent")
}

// CompactWithSummary is the model-written compaction. It plans the cut,
// summarizes the dropped span through the client, and returns the conversation
// with the checkpoint in the span's place. When the summarizer cannot run or
// cannot produce a checkpoint, it degrades to the static line — the context
// budget is still honored, only the memory is lost.
func (c *Client) CompactWithSummary(ctx context.Context, system string, msgs []Message, maxTokens int) []Message {
	keepFrom, dropped := PlanCompaction(msgs, maxTokens)
	if keepFrom == 0 || len(dropped) == 0 {
		return Compact(msgs, maxTokens)
	}

	summary := ""
	if ctx != nil && ctx.Err() == nil {
		if s, err := c.SummarizeForCompaction(ctx, system, dropped); err == nil && s != "" {
			summary = s
		}
	}
	if summary == "" {
		return Compact(msgs, maxTokens)
	}

	out := make([]Message, 0, len(msgs)-len(dropped)+2)
	out = append(out, msgs[0])
	out = append(out, UserText(summary))
	out = append(out, AssistantText("Understood. Continuing from the current state of the files."))
	out = append(out, msgs[keepFrom:]...)
	return out
}
