package api

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/harness/events"
	"github.com/seekersstudio/backend/internal/harness/sessionlog"
)

// The H1 session-log bridge: every conversation write the prompt paths make
// lands in the append-only event log alongside the legacy
// conversation_messages rows. Dual-write keeps the read path on the legacy
// table until derivation is proven everywhere (the flag flip is a read-path
// change, not a migration); the log is additive, so rollback is free.
//
// The bridge is small on purpose: it converts ai.Block values to taxonomy
// payloads and back. Projection logic lives in sessionlog; provider shapes
// live in ai. This file owns neither — it is the adapter between them, and it
// is the only place that knows both types exist.

// sessionLogKey is the stable log key for one project's build conversation.
func sessionLogKey(projectID int64) string {
	return "project-" + strconv.FormatInt(projectID, 10)
}

// sessionLogCache holds one open Log per project. Logs are opened lazily and
// kept for the handler's lifetime; sessionlog.Log is itself concurrency-safe,
// so the cache only needs to guard the map.
type sessionLogCache struct {
	mu   sync.Mutex
	logs map[string]*sessionlog.Log
}

func newSessionLogCache() *sessionLogCache {
	return &sessionLogCache{logs: map[string]*sessionlog.Log{}}
}

// get opens (or resumes) the project's log. A failed open logs and returns
// nil — log loss degrades replay fidelity, it must never fail a live build.
func (c *sessionLogCache) get(store sessionStore, projectID int64) *sessionlog.Log {
	key := sessionLogKey(projectID)
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.logs[key]; ok {
		return l
	}
	l, err := store.SessionLog(key)
	if err != nil {
		log.Printf("project %d: could not open the session log: %v", projectID, err)
		return nil
	}
	c.logs[key] = l
	return l
}

// sessionStore is the subset of *store.Store the bridge needs — a one-method
// seam so tests can drive it without a database.
type sessionStore interface {
	SessionLog(key string) (*sessionlog.Log, error)
}

// appendLoggedAssistant writes one assistant ai.Message into the log as
// surface events: tool_use blocks ride the message anchor (the durable record
// of one model call), tool_result blocks become their own tool/result events
// paired by id. A turn with no anchor blocks (pure tool results) is
// represented by its tool/result events alone.
func appendLoggedAssistant(l *sessionlog.Log, m ai.Message, promptID int64) {
	if l == nil {
		return
	}
	var anchorBlocks []events.BlockPayload
	for _, b := range m.Blocks {
		switch b.Type {
		case "tool_use":
			if _, err := l.Append(events.ToolDispatch, events.ToolDispatchPayload{
				CallID: b.ID, Name: b.Name,
			}); err != nil {
				log.Printf("session log: tool dispatch append failed: %v", err)
			}
			var input *events.RawJSON
			if len(b.Input) > 0 {
				in := events.RawJSON(b.Input)
				input = &in
			}
			anchorBlocks = append(anchorBlocks, events.BlockPayload{
				Type: "tool_use", ID: b.ID, Name: b.Name, Input: input,
				Signature: b.ThoughtSignature,
			})
		case "tool_result":
			if _, err := l.Append(events.ToolResult, events.ToolResultPayload{
				CallID: b.ToolUseID, Name: b.Name, Content: b.Content, IsError: b.IsError,
			}); err != nil {
				log.Printf("session log: tool result append failed: %v", err)
			}
		default:
			anchorBlocks = append(anchorBlocks, events.BlockPayload{
				Type: b.Type, Text: b.Text, Signature: b.ThoughtSignature,
			})
			// A plan block rides the anchor as raw JSON for the restore path.
			if len(b.Plan) > 0 {
				p := events.RawJSON(b.Plan)
				anchorBlocks[len(anchorBlocks)-1].Plan = &p
			}
		}
	}
	if len(anchorBlocks) > 0 {
		if _, err := l.Append(events.AssistantMessage, events.MessagePayload{
			Role: m.Role, Blocks: anchorBlocks, PromptID: promptID,
		}); err != nil {
			log.Printf("session log: assistant anchor append failed: %v", err)
		}
	}
}

// appendLoggedUser writes a user turn. A user tool_result block (the answer
// path) becomes its own tool/result event so derivation pairs it with the
// question's tool_use by id.
func appendLoggedUser(l *sessionlog.Log, m ai.Message, promptID int64) {
	if l == nil {
		return
	}
	var blocks []events.BlockPayload
	for _, b := range m.Blocks {
		if b.Type == "tool_result" {
			if _, err := l.Append(events.ToolResult, events.ToolResultPayload{
				CallID: b.ToolUseID, Name: b.Name, Content: b.Content, IsError: b.IsError,
			}); err != nil {
				log.Printf("session log: tool result append failed: %v", err)
			}
			continue
		}
		blocks = append(blocks, events.BlockPayload{Type: b.Type, Text: b.Text})
	}
	if len(blocks) > 0 {
		if _, err := l.Append(events.UserMessage, events.MessagePayload{
			Role: m.Role, Blocks: blocks, PromptID: promptID,
		}); err != nil {
			log.Printf("session log: user message append failed: %v", err)
		}
	}
}

// deriveToAIMessages converts a derived projection back into ai.Message values
// for the provider request. This is the read path the flag flip will select.
func deriveToAIMessages(derived []sessionlog.DerivedMessage) []ai.Message {
	out := make([]ai.Message, 0, len(derived))
	for _, m := range derived {
		msg := ai.Message{Role: m.Role}
		for _, b := range m.Blocks {
			blk := ai.Block{
				Type: b.Type, Text: b.Text, ID: b.ID, Name: b.Name,
				Input: json.RawMessage(b.Input), Content: b.Content,
				IsError: b.IsError, ThoughtSignature: b.Signature,
			}
			// A derived tool_result block carries the pairing id in ID (the
			// taxonomy field); ai.Block pairs results through ToolUseID.
			if blk.Type == "tool_result" {
				blk.ToolUseID = b.ID
				blk.ID = ""
			}
			msg.Blocks = append(msg.Blocks, blk)
		}
		if len(msg.Blocks) > 0 {
			out = append(out, msg)
		}
	}
	return out
}

// deriveFromLog returns the project's conversation derived from the session
// log when SEEKERSTUDIO_SESSIONLOG=derive selects the new read path, and nil
// otherwise (the caller falls back to the legacy table). A log that fails to
// open also returns nil — a read-path failure falls back, it never kills a
// build turn.
func (h *PromptHandler) deriveFromLog(projectID int64) []ai.Message {
	if strings.ToLower(strings.TrimSpace(os.Getenv("SEEKERSTUDIO_SESSIONLOG"))) != "derive" {
		return nil
	}
	lg := h.slog.get(h.store, projectID)
	if lg == nil {
		return nil
	}
	return deriveToAIMessages(lg.DeriveMessages())
}
