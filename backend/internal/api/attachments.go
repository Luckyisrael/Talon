package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/blobstore"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// Attachments: images and markdown docs a turn carries alongside its prompt.
// The composer uploads each file first (unclaimed), then sends their ids with
// the prompt; the turn claims them exactly once. Markdown rides to the model
// as inlined text; images ride as content blocks the serializers turn into
// provider image parts. Stored blocks carry references, never pixel bytes.

const maxAttachmentBytes = 5 << 20

// maxDocInlineChars caps how much of an attached markdown file is inlined
// into the prompt. Past it the model gets a truncation note, not silence.
const maxDocInlineChars = 24_000

var allowedImageMimes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
}

func (h *PromptHandler) attachmentsDir() string {
	return filepath.Join(h.workspaceDir, "..", "attachments")
}

// UploadAttachment stores one image or markdown file:
// POST /v1/projects/{id}/attachments (multipart: file=<png|jpeg|webp|md>).
// Validation failures are 400s with the reason — a bad file never enters the
// collection, and nothing is claimed until the turn that sends it.
func (h *PromptHandler) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the upload (max 32MB)")
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentBytes+1))
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "could not read the file")
		return
	}
	if len(data) > maxAttachmentBytes {
		writeError(w, http.StatusBadRequest, "that file is larger than 5MB")
		return
	}

	name := sanitizeAttachmentName(header.Filename)
	kind, mime, ext, verr := classifyAttachment(name, data)
	if verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	// The stored filename forces the validated extension: a png renamed to
	// .exe uploads as what it is, not what it claims to be.
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" {
		stem = "file"
	}
	if len(stem) > 64 {
		stem = stem[:64]
	}
	fname := fmt.Sprintf("%d-%s%s", time.Now().UnixNano(), stem, ext)
	ref, err := h.putAttachment(r.Context(), projectID, kind, fname, data, mime)
	if err != nil {
		log.Printf("project %d: attachment store failed: %v", projectID, err)
		writeError(w, http.StatusInternalServerError, "could not store the file")
		return
	}
	att, err := h.store.AddAttachment(projectID, kind, stem+ext, mime, ref, int64(len(data)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the file")
		return
	}
	go h.purgeOrphanAttachments(projectID)
	writeJSON(w, http.StatusCreated, att)
}

// DownloadAttachment serves one attachment back to its project:
// GET /v1/projects/{id}/attachments/{attID}. The chat restores image
// thumbnails through this; ownership is checked before any byte moves.
func (h *PromptHandler) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.ownedProject(w, r, projectID); !ok {
		return
	}
	attID, err := strconv.ParseInt(chi.URLParam(r, "attID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment id")
		return
	}
	att, err := h.store.GetAttachment(attID)
	if err != nil || att.ProjectID != projectID {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	data, err := h.readAttachment(r.Context(), att.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "attachment not found")
		return
	}
	w.Header().Set("Content-Type", att.Mime)
	w.Write(data)
}

// SetR2Store points attachment images at Cloudflare R2. Nil (the default)
// keeps images on local disk — dev, tests, and any deployment without R2 env.
// Markdown docs always stay local: small text already duplicated in the
// transcript, so they never needed object storage.
func (h *PromptHandler) SetR2Store(s blobstore.Store) {
	h.r2 = s
}

// putAttachment stores original bytes and returns the reference the DB row
// keeps: an r2: key for images when R2 is configured, a filesystem path
// otherwise (and always for docs).
func (h *PromptHandler) putAttachment(ctx context.Context, projectID int64, kind, fname string, data []byte, mime string) (string, error) {
	if kind == "image" && h.r2 != nil {
		key := fmt.Sprintf("%d/%s", projectID, fname)
		if err := h.r2.Put(ctx, key, data, mime); err != nil {
			return "", err
		}
		return blobstore.R2Prefix + key, nil
	}
	dir := filepath.Join(h.attachmentsDir(), strconv.FormatInt(projectID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fname)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// readAttachment loads bytes back through the reference: R2 for r2: keys,
// disk for paths. Callers pass their request context; background paths pass
// their own bounded one.
func (h *PromptHandler) readAttachment(ctx context.Context, ref string) ([]byte, error) {
	if strings.HasPrefix(ref, blobstore.R2Prefix) {
		if h.r2 == nil {
			return nil, fmt.Errorf("r2 attachment without r2 configured")
		}
		return h.r2.Get(ctx, strings.TrimPrefix(ref, blobstore.R2Prefix))
	}
	return os.ReadFile(ref)
}

// purgeOrphanAttachments sweeps uploads nobody ever sent: unclaimed rows
// older than a day lose their bytes first, then their rows. Fire-and-forget
// off the upload path — a slow object store never slows a send.
func (h *PromptHandler) purgeOrphanAttachments(projectID int64) {
	orphans, err := h.store.ListOrphanAttachments(projectID, 24*time.Hour)
	if err != nil || len(orphans) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var ids []int64
	for _, a := range orphans {
		if strings.HasPrefix(a.Path, blobstore.R2Prefix) {
			if h.r2 == nil {
				continue // unreachable now; the next pass retries
			}
			if err := h.r2.Delete(ctx, strings.TrimPrefix(a.Path, blobstore.R2Prefix)); err != nil {
				continue
			}
		} else if err := os.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			continue
		}
		ids = append(ids, a.ID)
	}
	if len(ids) > 0 {
		_ = h.store.DeleteAttachments(ids)
	}
}

// sanitizeAttachmentName reduces an upload filename to a safe display name:
// no directories, no control characters, bounded length.
func sanitizeAttachmentName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if len(name) > 128 {
		name = name[:128]
	}
	if name == "" || name == "." {
		name = "file"
	}
	return name
}

// classifyAttachment sniffs the bytes (never trusts the extension alone) and
// sorts the file into image or markdown doc. Anything else is rejected with
// the reason: attachments feed the model, so executables and zips have no
// business here.
func classifyAttachment(name string, data []byte) (kind, mime, ext string, err error) {
	sniff, _, _ := strings.Cut(http.DetectContentType(data), ";")
	sniff = strings.TrimSpace(sniff)
	if ext, ok := allowedImageMimes[sniff]; ok {
		return "image", sniff, ext, nil
	}
	if strings.EqualFold(filepath.Ext(name), ".md") &&
		(strings.HasPrefix(sniff, "text/") || sniff == "application/octet-stream") &&
		utf8.Valid(data) {
		return "doc", "text/markdown", ".md", nil
	}
	return "", "", "", fmt.Errorf("only png, jpeg, webp images and .md files can be attached")
}

// claimAttachments resolves the ids a send carries into owned, unspent
// attachments and links them to the turn. Anything missing, foreign, or
// already spent fails the whole send: a half-attached turn would show chips
// the model never saw.
func (h *PromptHandler) claimAttachments(w http.ResponseWriter, projectID, promptID int64, ids []int64) ([]store.Attachment, bool) {
	if len(ids) == 0 {
		return nil, true
	}
	if len(ids) > 10 {
		writeError(w, http.StatusBadRequest, "at most 10 attachments per turn")
		return nil, false
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			writeError(w, http.StatusBadRequest, "duplicate or invalid attachment")
			return nil, false
		}
		seen[id] = true
	}
	out := make([]store.Attachment, 0, len(ids))
	for _, id := range ids {
		att, err := h.store.GetAttachment(id)
		if err != nil || att.ProjectID != projectID {
			writeError(w, http.StatusNotFound, "attachment not found")
			return nil, false
		}
		out = append(out, *att)
	}
	n, err := h.store.ClaimAttachments(promptID, projectID, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not attach the files")
		return nil, false
	}
	if n != int64(len(ids)) {
		writeError(w, http.StatusConflict, "those files are already attached to another turn — re-upload them")
		return nil, false
	}
	return out, true
}

// buildUserTurn assembles the fresh user turn carrying attachments. Markdown
// docs inline as text sections — replayed everywhere prose replays, scanned
// by guardrails like any prompt text. Images ride as blocks the serializers
// turn into provider image parts in build mode; plan mode's flat path carries
// their names only (see planHistory). The turn is storage-clean: image blocks
// carry references, never bytes — resolveImages hydrates a copy for the live
// request. Blocks[0] is always the text block; turnHistory appends pending
// context to it.
func (h *PromptHandler) buildUserTurn(atts []store.Attachment, prompt string) ai.Message {
	var sb strings.Builder
	sb.WriteString(prompt)
	var imageNames []string
	blocks := make([]ai.Block, 0, len(atts)+1)
	for _, a := range atts {
		if a.Kind == "doc" {
			sb.WriteString("\n\n[Attached file " + a.Name + "]\n" + readAttachmentDoc(h, a.Path))
			// The doc rides a reference block alongside its inlined text:
			// display layers render the chip and collapse the text, while
			// the model replays the prose. Serializers drop doc blocks.
			blocks = append(blocks, ai.Block{Type: "doc", AttachmentID: a.ID, Name: a.Name, Mime: a.Mime})
		} else {
			imageNames = append(imageNames, a.Name)
			blocks = append(blocks, ai.Block{Type: "image", AttachmentID: a.ID, Name: a.Name, Mime: a.Mime})
		}
	}
	if len(imageNames) > 0 {
		sb.WriteString("\n\n[Attached images: " + strings.Join(imageNames, ", ") + "]")
	}
	return ai.Message{Role: "user", Blocks: append([]ai.Block{{Type: "text", Text: sb.String()}}, blocks...)}
}

// readAttachmentDoc loads a markdown doc for inlining, truncating past the
// cap with a note rather than silently cutting or blowing the context window.
func readAttachmentDoc(h *PromptHandler, ref string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data, err := h.readAttachment(ctx, ref)
	if err != nil {
		return "[unreadable — skipped]"
	}
	runes := []rune(string(data))
	if len(runes) > maxDocInlineChars {
		return string(runes[:maxDocInlineChars]) +
			fmt.Sprintf("\n…[truncated to %d of %d characters]", maxDocInlineChars, len(runes))
	}
	return string(runes)
}

// resolveImages hydrates image blocks with base64 bytes for the provider
// request. Stored and derived histories carry references only; this runs on
// the in-memory copy at send time, never on what gets persisted. A block
// whose file is gone degrades to a named text note rather than failing the
// turn — and the turn always carries prompt text, so it stays well-formed.
func (h *PromptHandler) resolveImages(projectID int64, msgs []ai.Message) []ai.Message {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out := make([]ai.Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		needs := false
		for _, b := range m.Blocks {
			if b.Type == "image" && b.AttachmentID != 0 && b.Data == "" {
				needs = true
				break
			}
		}
		if !needs {
			continue
		}
		blocks := make([]ai.Block, 0, len(m.Blocks))
		for _, b := range m.Blocks {
			if b.Type != "image" || b.AttachmentID == 0 || b.Data != "" {
				blocks = append(blocks, b)
				continue
			}
			att, err := h.store.GetAttachment(b.AttachmentID)
			if err != nil || att.ProjectID != projectID || att.Kind != "image" {
				blocks = append(blocks, ai.Block{Type: "text", Text: "[attached image " + b.Name + " is unavailable — the file is missing]"})
				continue
			}
			data, err := h.readAttachment(ctx, att.Path)
			if err != nil || len(data) == 0 || len(data) > 8<<20 {
				blocks = append(blocks, ai.Block{Type: "text", Text: "[attached image " + b.Name + " is unavailable — the file is missing]"})
				continue
			}
			nb := b
			nb.Data = base64.StdEncoding.EncodeToString(data)
			if nb.Mime == "" {
				nb.Mime = "image/png"
			}
			blocks = append(blocks, nb)
		}
		out[i].Blocks = blocks
	}
	return out
}
