package ai

import (
	"encoding/json"
	"strings"
)

// This file holds the history surgery behind ask_user — the tool that lets the
// agent pause a build to ask the user a clarifying question. Pausing leaves the
// transcript ending on an assistant turn whose tool_use has no result yet; both
// providers reject that shape on replay. The two helpers here recognise that
// paused shape on resume and close it with the user's answer, without going
// through TrimToValidHistory (which would drop the trailing tool_use as an
// orphan — exactly the wrong move when we mean to answer it).

// PendingToolUse inspects a restored history for a paused tool call: a trailing
// assistant turn whose tool_uses have not been answered yet. It returns the id
// of the tool_use for the named tool (the call the loop paused on), the ids of
// every tool_use in that same assistant turn (so a resume can satisfy siblings
// too, keeping the turn replayable), and whether such a pending call was found.
//
// A trailing assistant turn is by definition unanswered — tool_results only ever
// arrive in a following user turn, and there is none — so the trailing-role check
// is all that is needed.
func PendingToolUse(history []Message, tool string) (askID string, allIDs []string, ok bool) {
	if len(history) == 0 {
		return "", nil, false
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		return "", nil, false
	}

	var found string
	var ids []string
	for _, b := range last.Blocks {
		if b.Type != "tool_use" {
			continue
		}
		ids = append(ids, b.ID)
		if b.Name == tool {
			found = b.ID
		}
	}
	if found == "" {
		return "", nil, false
	}
	return found, ids, true
}

// PendingQuestion extracts the question text of a trailing unanswered ask_user
// call, if the history ends on one. The replay trim drops trailing orphan
// tool_uses, so a fresh turn after a paused one would otherwise never see the
// question the user might be replying to ("go with your fallback" means
// nothing without it). Callers carry the returned text forward explicitly.
func PendingQuestion(history []Message) (string, bool) {
	if len(history) == 0 {
		return "", false
	}
	last := history[len(history)-1]
	if last.Role != "assistant" {
		return "", false
	}
	for _, b := range last.Blocks {
		if b.Type != "tool_use" || b.Name != "ask_user" {
			continue
		}
		var input struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(b.Input, &input); err != nil {
			continue
		}
		if q := strings.TrimSpace(input.Question); q != "" {
			return q, true
		}
	}
	return "", false
}

// AnswerTurn builds the user turn that resolves a paused ask_user. The answer
// becomes the tool_result for askID; every other pending tool_use in the same
// assistant turn gets a neutral note that it did not run, so the assistant turn
// is fully satisfied and both providers will replay it — a tool_use left without
// a matching tool_result is a 400. Siblings are rare (ask_user is meant to be
// called alone) but a model can batch it with other calls, and an unanswered
// sibling would wedge every following request.
func AnswerTurn(askID string, allIDs []string, answer string) Message {
	blocks := make([]Block, 0, len(allIDs))
	for _, id := range allIDs {
		if id == askID {
			blocks = append(blocks, Block{Type: "tool_result", ToolUseID: id, Content: answer})
			continue
		}
		blocks = append(blocks, Block{
			Type:      "tool_result",
			ToolUseID: id,
			Content: "This call was not run — the build paused here to ask the user a question. " +
				"Reissue it now if you still need it.",
		})
	}
	return Message{Role: "user", Blocks: blocks}
}

// AnsweredHistory assembles the conversation to replay when resuming a turn
// paused on ask_user: the stored transcript (which ends on the assistant turn
// whose tool_use paused the loop) with the answer turn appended.
//
// It deliberately does NOT run TrimToValidHistory over the result. That trim cuts
// the tail back until the history ends on an assistant turn — correct for a fresh
// prompt, fatal here: the answer turn is a user tool_result by design, and the
// whole point of resuming is to send it. So only the front is cleaned (a history
// truncated mid-exchange upstream must not open on an orphan tool_result), and
// the tail — the answered tool_use and the answer itself — is preserved exactly.
func AnsweredHistory(stored []Message, answer Message) []Message {
	full := make([]Message, 0, len(stored)+1)
	full = append(full, stored...)
	full = append(full, answer)

	start := 0
	for start < len(full) && !isCleanBoundary(full[start]) {
		start++
	}
	return full[start:]
}
