package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// W2.1 — the derive ≡ legacy parity harness (PLAN_WORLDCLASS:264-266).
//
// Every fixture is written through the production dual-write pair (one
// conversation_messages row + the same message through the H1 bridge), and
// both reads run the production read function in its two flag states:
// TALONSTUDIO_SESSIONLOG off/legacy → conversation_messages, =derive → the
// session log projection. Parity means the model carries a byte-identical
// message sequence either way, tool pairing intact. Shadow shapes (compacted,
// trimmed) compare the post-boundary window under the summary head — the
// projection's honest form of the same history.

type w2World struct {
	st    *store.Store
	h     *PromptHandler
	pid   int64
	lg    *sessionlog.Log
	docID int64
	imgID int64
}

// newW2World builds a fixture project with real attachment rows so
// resolveImages hydrates both reads identically (a missing row would let one
// path drop a block the other kept, masking a projection bug).
func newW2World(t *testing.T) *w2World {
	t.Helper()
	st, uid, pid := testAttachmentStore(t)
	h := &PromptHandler{
		store:        st,
		workspaceDir: filepath.Join(t.TempDir(), "ws"),
		slog:         newSessionLogCache(),
	}
	lg := h.slog.get(st, pid)
	if lg == nil {
		t.Fatal("session log did not open")
	}

	docRec, docReq := testUploadRequest(t, uid, pid, "notes.md", []byte("# notes\nlime only\n"))
	h.UploadAttachment(docRec, docReq)
	var doc store.Attachment
	if err := json.Unmarshal(docRec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("doc attachment: %v", err)
	}
	imgRec, imgReq := testUploadRequest(t, uid, pid, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(imgRec, imgReq)
	var img store.Attachment
	if err := json.Unmarshal(imgRec.Body.Bytes(), &img); err != nil {
		t.Fatalf("image attachment: %v", err)
	}

	return &w2World{st: st, h: h, pid: pid, lg: lg, docID: doc.ID, imgID: img.ID}
}

// dualWrite mirrors prompts.go's production persist pair (runLoop Persist
// :1020-1035): one legacy row, then the same message through the bridge.
func (w *w2World) dualWrite(t *testing.T, m ai.Message) {
	t.Helper()
	if err := w.st.AppendMessage(w.pid, 1, m.Role, ai.MarshalBlocks(m.Blocks), 0, 0); err != nil {
		t.Fatalf("legacy append: %v", err)
	}
	if m.Role == "user" {
		appendLoggedUser(w.lg, m, 1)
	} else {
		appendLoggedAssistant(w.lg, m, 1)
	}
}

// reads runs the production read in both flag states — exactly the history
// the model carries today (legacy) and after the W2.2 flip (derive).
func (w *w2World) reads(t *testing.T) (legacy, derived []ai.Message) {
	t.Helper()
	t.Setenv("TALONSTUDIO_SESSIONLOG", "legacy")
	legacy = w.h.storedConversation(w.pid)
	t.Setenv("TALONSTUDIO_SESSIONLOG", "derive")
	derived = w.h.storedConversation(w.pid)
	return legacy, derived
}

// w2AssertParity compares the derived sequence against the legacy window:
// message-for-message byte equality, preceded by the summary head when the
// fixture is shadowed. legacyStart is the first legacy message the shadow
// replaced (0 for unshadowed transcripts — full equality).
func w2AssertParity(t *testing.T, legacy, derived []ai.Message, summary bool, legacyStart int) {
	t.Helper()
	w2AssertPairing(t, derived)
	if summary {
		if len(derived) == 0 {
			t.Fatal("derived is empty: expected the shadow summary head")
		}
		head := derived[0]
		if head.Role != "user" || len(head.Blocks) == 0 || head.Blocks[0].Type != "text" ||
			!strings.HasPrefix(head.Blocks[0].Text, "[earlier conversation condensed]") {
			b, _ := json.Marshal(head)
			t.Errorf("shadow head is not the compaction summary: %s", b)
		}
		derived = derived[1:]
	}
	if legacyStart > len(legacy) {
		t.Fatalf("legacyStart %d beyond legacy length %d", legacyStart, len(legacy))
	}
	want := legacy[legacyStart:]
	if len(derived) != len(want) {
		t.Fatalf("parity: derived %d messages, legacy window %d (legacyStart=%d)", len(derived), len(want), legacyStart)
	}
	for i := range want {
		if !reflect.DeepEqual(derived[i], want[i]) {
			dl, _ := json.Marshal(want[i])
			dd, _ := json.Marshal(derived[i])
			t.Errorf("parity break at message %d:\n  legacy:  %s\n  derived: %s", i+legacyStart, dl, dd)
		}
	}
}

// w2AssertPairing walks the sequence: every tool_result must reference a
// tool_use seen earlier — the invariant both providers enforce and the one
// a lossy projection breaks first.
func w2AssertPairing(t *testing.T, msgs []ai.Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			switch b.Type {
			case "tool_use":
				seen[b.ID] = true
			case "tool_result":
				if !seen[b.ToolUseID] {
					t.Errorf("unpaired tool_result %q (no earlier tool_use)", b.ToolUseID)
				}
			}
		}
	}
}

func TestPhaseW2ReadParity(t *testing.T) {
	// Normal transcript with attachments and a tool round — the shape every
	// build turn takes.
	t.Run("normal", func(t *testing.T) {
		w := newW2World(t)
		w.dualWrite(t, ai.Message{Role: "user", Blocks: []ai.Block{
			{Type: "text", Text: "build a wallet screen"},
			{Type: "doc", AttachmentID: w.docID, Name: "notes.md", Mime: "text/markdown"},
			{Type: "image", AttachmentID: w.imgID, Name: "shot.png", Mime: "image/png"},
		}})
		w.dualWrite(t, ai.Message{Role: "assistant", Blocks: []ai.Block{
			{Type: "text", Text: "reading the project first"},
			{Type: "tool_use", ID: "call_1", Name: "list_files", Input: json.RawMessage(`{"path":""}`)},
		}})
		w.dualWrite(t, ai.ToolResults([]ai.Block{
			{Type: "tool_result", ToolUseID: "call_1", Name: "list_files", Content: "app/ components/"},
		}))
		w.dualWrite(t, ai.AssistantText("here is the wallet screen"))

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, false, 0)
	})

	// Interrupted run: the delivered prefix, then the terminal marker row
	// persistTurnEnd writes (status cancelled) — the marker must survive
	// projection or a dead turn looks alive after the flip.
	t.Run("interrupted", func(t *testing.T) {
		w := newW2World(t)
		w.dualWrite(t, ai.UserText("stream the changelog"))
		w.dualWrite(t, ai.AssistantText("partial deliv"))
		if !w.h.persistTurnEnd(w.pid, 1, "cancelled", errors.New("context canceled"), 1, nil) {
			t.Fatal("persistTurnEnd failed")
		}

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, false, 0)
	})

	// Answered: an ask_user question resolved by the user's answer — the
	// tool_use/tool_result pair the answer path persists.
	t.Run("answered", func(t *testing.T) {
		w := newW2World(t)
		w.dualWrite(t, ai.UserText("wire the login screen"))
		w.dualWrite(t, ai.Message{Role: "assistant", Blocks: []ai.Block{
			{Type: "text", Text: "need your call"},
			{Type: "tool_use", ID: "ask_1", Name: "ask_user", Input: json.RawMessage(`{"question":"which auth?"}`)},
		}})
		w.dualWrite(t, ai.Message{Role: "user", Blocks: []ai.Block{
			{Type: "tool_result", ToolUseID: "ask_1", Name: "ask_user", Content: "use passkeys"},
		}})
		w.dualWrite(t, ai.AssistantText("passkeys it is"))

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, false, 0)
	})

	// Plan exchange through the real persistPlanExchange — the structured
	// plan block is what the restore path rebuilds the plan card from.
	t.Run("plan", func(t *testing.T) {
		w := newW2World(t)
		w.h.persistPlanExchange(w.pid, 1, ai.UserText("plan the wallet"), "Three screens.",
			json.RawMessage(`{"screens":[{"name":"Wallet"}]}`))

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, false, 0)
	})

	// Compacted: OnCompacted shadows everything persisted so far, later
	// rounds land after the boundary — derived = summary + post-boundary,
	// compared against the same legacy tail.
	t.Run("compacted", func(t *testing.T) {
		w := newW2World(t)
		for i := 1; i <= 4; i++ {
			w.dualWrite(t, ai.UserText(fmt.Sprintf("early ask %d", i)))
			w.dualWrite(t, ai.AssistantText(fmt.Sprintf("early reply %d", i)))
		}
		if err := w.lg.Shadow(w.lg.Seq(),
			"context compacted from 8 to 2 turns; earlier files are on disk and re-readable",
			"wire compaction"); err != nil {
			t.Fatalf("shadow: %v", err)
		}
		for i := 5; i <= 6; i++ {
			w.dualWrite(t, ai.UserText(fmt.Sprintf("late ask %d", i)))
			w.dualWrite(t, ai.AssistantText(fmt.Sprintf("late reply %d", i)))
		}

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, true, 8)
	})

	// Trimmed: W1.1 retention — Retain keeps the newest 2 surface messages
	// derivation-visible and shadows the rest, deleting nothing. The derived
	// tail must equal the legacy tail it kept.
	t.Run("trimmed", func(t *testing.T) {
		w := newW2World(t)
		for i := 1; i <= 3; i++ {
			w.dualWrite(t, ai.UserText(fmt.Sprintf("ask %d", i)))
			w.dualWrite(t, ai.AssistantText(fmt.Sprintf("reply %d", i)))
		}
		if _, err := w.lg.Retain(2,
			"Older turns retained: the newest 2 messages are shown.", "retention"); err != nil {
			t.Fatalf("retain: %v", err)
		}

		legacy, derived := w.reads(t)
		w2AssertParity(t, legacy, derived, true, 4)
	})

	// Replay determinism (H1's own gate, promoted here permanently): the
	// same persisted rows re-opened in a second log derive byte-identical
	// history — derivation is a projection of storage, never of process.
	t.Run("determinism", func(t *testing.T) {
		w := newW2World(t)
		w.dualWrite(t, ai.UserText("first"))
		w.dualWrite(t, ai.AssistantText("second"))
		w.dualWrite(t, ai.Message{Role: "assistant", Blocks: []ai.Block{
			{Type: "tool_use", ID: "c9", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)},
		}})
		w.dualWrite(t, ai.ToolResults([]ai.Block{
			{Type: "tool_result", ToolUseID: "c9", Name: "read_file", Content: "package a"},
		}))

		reopened, err := w.st.SessionLog(sessionLogKey(w.pid))
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		a, _ := json.Marshal(w.lg.DeriveMessages())
		b, _ := json.Marshal(reopened.DeriveMessages())
		if string(a) != string(b) {
			t.Error("re-opened log derives different messages — replay is not re-derivation")
		}
	})
}
