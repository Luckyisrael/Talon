package api

import (
	"context"
	"strings"
	"unicode"

	"github.com/seekersstudio/backend/internal/ai"
)

// SessionTitleFallback is the synchronous title: the honest truncated
// first prompt, no model call — stored at creation so the list line is
// human from the first millisecond.
func (h *PromptHandler) SessionTitleFallback(firstPrompt string) string {
	return truncateTitle(firstPrompt)
}

// SessionTitle derives a human title from a project's first prompt: the
// LLM path when a client is available (bounded — the DSH session-title
// pattern: a few words, a hard byte cap, no quotes/newlines), the honest
// truncated prompt otherwise. A dashboard list shows people, not walls.
//
// The law: the title never fails a request. Every error path degrades to
// the truncated prompt — a title is decoration; the prompt is the record.
// Called after the turn completes (never before it): a synchronous pre-turn
// model call would consume the provider script and add latency to every
// turn for decoration.
func (h *PromptHandler) SessionTitle(ctx context.Context, firstPrompt string) string {
	firstPrompt = strings.TrimSpace(firstPrompt)
	if firstPrompt == "" {
		return "Untitled project"
	}
	// The honest fallback works even when everything else is down: the
	// prompt's first line, cut at a word boundary under the byte cap.
	fallback := truncateTitle(firstPrompt)

	if h.ai == nil {
		return fallback
	}
	// The LLM path: one bounded request, cheap model, no tools — a title
	// must never cost a real round.
	resp, err := h.ai.WithModel(titleModel(h.ai)).Chat(ctx,
		"Title this app-development request in at most six words. "+
			"Reply with the title alone: no quotes, no punctuation at the end, no prefix like 'Title:'.",
		[]ai.PromptMessage{{Role: "user", Content: firstPrompt}})
	if err != nil || strings.TrimSpace(resp) == "" {
		return fallback
	}
	return truncateTitle(resp)
}

// titleModel picks the cheapest configured model — the title path is a
// one-liner, and burning a heavy round on it wastes the user's budget.
func titleModel(c *ai.Client) string {
	pol := ai.LoadRoutingPolicy()
	if m, ok := pol.Models[ai.TierWeak]; ok && strings.TrimSpace(m) != "" {
		return m
	}
	return c.Model()
}

// truncateTitle bounds a title: one line, word-boundary cut, hard byte cap
// (DSH: bounded words/bytes), whitespace squeezed.
func truncateTitle(s string) string {
	// One line.
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	// Hard byte cap at a word boundary.
	const maxBytes = 80
	if len(s) > maxBytes {
		cut := s[:maxBytes]
		if i := strings.LastIndexFunc(cut, unicode.IsSpace); i > 0 {
			cut = cut[:i]
		}
		s = cut
	}
	// Squeeze runs of whitespace to single spaces.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
