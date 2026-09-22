package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// History index and search: the turn catalog behind the History drawer
// (web) and History screen (mobile). Metadata only — transcripts still come
// from the conversation endpoints.

// ── GET /v1/projects/{id}/prompts ────────────────────────────────────
// The turn index, newest first, cursor-paged by id. This is the endpoint the
// mobile History screen was already calling before it existed — it failed
// silent into "no turns". Titles come from the prompts table (LLM-titled or
// the honest truncated prompt), so the list reads without opening turns.
func (h *PromptHandler) ListPrompts(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid before cursor")
			return
		} else {
			before = n
		}
	}
	entries, err := h.store.ListPrompts(projectID, before, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list turns")
		return
	}
	if entries == nil {
		entries = []store.PromptIndexEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// ── GET /v1/projects/{id}/conversation/search ────────────────────────
// Search over stored turns: FTS5 tokens unioned with the escaped literal
// substring, so "connecting" finds "connected" (stemmed) and "onnected"
// still finds it (substring) — neither recall class lost. LIKE
// metacharacters are escaped store-side, so a query for "100%" matches
// literally. Returns whole turns (same shape as GetConversation) with the
// prompt ids clients need to scroll to them — excerpts would strand the
// reader with no way to jump.
func (h *PromptHandler) SearchConversation(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeError(w, http.StatusBadRequest, "q must be at least 2 characters")
		return
	}
	if len(q) > 200 {
		writeError(w, http.StatusBadRequest, "q must be at most 200 characters")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	stored, err := h.store.SearchConversationBlocks(projectID, q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not search the conversation")
		return
	}
	type turn struct {
		PromptID int64      `json:"prompt_id"`
		Seq      int64      `json:"seq"`
		Role     string     `json:"role"`
		Blocks   []ai.Block `json:"blocks"`
		At       string     `json:"at"`
	}
	out := make([]turn, 0, len(stored))
	for _, m := range stored {
		out = append(out, turn{PromptID: m.PromptID, Seq: m.Seq, Role: m.Role, Blocks: ai.UnmarshalBlocks(m.Blocks), At: m.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"turns": out})
}
