package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/store"
)

func testAttachmentStore(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "att.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	u, err := st.CreateUser("att@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "att", "1.0.0", "att-slug", "com.test.att", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, u.ID, p.ID
}

func testPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testUploadRequest(t *testing.T, userID, projectID int64, filename string, data []byte) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", projectID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return httptest.NewRecorder(), req
}

func TestUploadAttachmentImage(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	recRec, recReq := testUploadRequest(t, userID, projectID, "shot.png", testPNGBytes(t, 8, 8))
	h.UploadAttachment(recRec, recReq)
	if recRec.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body %s", recRec.Code, recRec.Body.String())
	}
	var att store.Attachment
	if err := json.Unmarshal(recRec.Body.Bytes(), &att); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if att.Kind != "image" || att.Mime != "image/png" || att.Name != "shot.png" {
		t.Errorf("attachment = %+v, want image/png shot.png", att)
	}
	if att.PromptID != nil {
		t.Errorf("fresh upload must be unclaimed, got prompt %v", att.PromptID)
	}
	stored, err := st.GetAttachment(att.ID)
	if err != nil {
		t.Fatalf("GetAttachment: %v", err)
	}
	if _, err := os.Stat(stored.Path); err != nil {
		t.Errorf("bytes missing on disk at %s: %v", stored.Path, err)
	}
}

func TestUploadAttachmentMarkdown(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	recRec, recReq := testUploadRequest(t, userID, projectID, "NOTES.md", []byte("# notes\n\nfollow these tokens\n"))
	h.UploadAttachment(recRec, recReq)
	if recRec.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body %s", recRec.Code, recRec.Body.String())
	}
	var att store.Attachment
	if err := json.Unmarshal(recRec.Body.Bytes(), &att); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if att.Kind != "doc" || att.Mime != "text/markdown" {
		t.Errorf("attachment = %+v, want doc text/markdown", att)
	}
}

func TestUploadAttachmentRejects(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	// An executable with an image name is sniffed, not trusted.
	recRec, recReq := testUploadRequest(t, userID, projectID, "shot.png", []byte("\x7fELF fake binary"))
	h.UploadAttachment(recRec, recReq)
	if recRec.Code != http.StatusBadRequest {
		t.Errorf("exe-as-png: status %d, want 400", recRec.Code)
	}

	// Oversize is refused before it lands anywhere.
	big := make([]byte, (5<<20)+1)
	rec2Rec, rec2Req := testUploadRequest(t, userID, projectID, "big.png", big)
	h.UploadAttachment(rec2Rec, rec2Req)
	if rec2Rec.Code != http.StatusBadRequest {
		t.Errorf("oversize: status %d, want 400", rec2Rec.Code)
	}

	// Another user's project reads as missing, never forbidden.
	other, err := st.CreateUser("other@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	rec3Rec, rec3Req := testUploadRequest(t, other.ID, projectID, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(rec3Rec, rec3Req)
	if rec3Rec.Code != http.StatusNotFound {
		t.Errorf("foreign project: status %d, want 404", rec3Rec.Code)
	}
}

func TestDownloadAttachmentRoundTrip(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	png := testPNGBytes(t, 6, 6)
	upRec, upReq := testUploadRequest(t, userID, projectID, "shot.png", png)
	h.UploadAttachment(upRec, upReq)
	if upRec.Code != http.StatusCreated {
		t.Fatalf("upload: status %d", upRec.Code)
	}
	var att store.Attachment
	if err := json.Unmarshal(upRec.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", projectID))
	rctx.URLParams.Add("attID", fmt.Sprintf("%d", att.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.DownloadAttachment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download: status %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), png) {
		t.Error("downloaded bytes differ from the upload")
	}
}

func TestClaimAttachmentsSpendsOnce(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	upRec, upReq := testUploadRequest(t, userID, projectID, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(upRec, upReq)
	var att store.Attachment
	if err := json.Unmarshal(upRec.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	got, ok := h.claimAttachments(rec, projectID, 101, []int64{att.ID})
	if !ok || len(got) != 1 {
		t.Fatalf("claim: ok=%v, got %+v (status %d)", ok, got, rec.Code)
	}
	// A second turn spending the same file fails the whole send.
	rec2 := httptest.NewRecorder()
	if _, ok := h.claimAttachments(rec2, projectID, 102, []int64{att.ID}); ok {
		t.Error("double-spend of an attachment must fail")
	}
	if rec2.Code != http.StatusConflict {
		t.Errorf("double-spend: status %d, want 409", rec2.Code)
	}
}

func TestBuildUserTurnInlinesDocsAndBlocksImages(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	upRec, upReq := testUploadRequest(t, userID, projectID, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(upRec, upReq)
	var img store.Attachment
	if err := json.Unmarshal(upRec.Body.Bytes(), &img); err != nil {
		t.Fatal(err)
	}
	up2Rec, up2Req := testUploadRequest(t, userID, projectID, "notes.md", []byte("# tokens\n\nlime only\n"))
	h.UploadAttachment(up2Rec, up2Req)
	var doc store.Attachment
	if err := json.Unmarshal(up2Rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}

	atts, ok := h.claimAttachments(httptest.NewRecorder(), projectID, 201, []int64{img.ID, doc.ID})
	if !ok {
		t.Fatal("claim failed")
	}
	turn := h.buildUserTurn(atts, "wire the button")
	if turn.Role != "user" || len(turn.Blocks) != 3 {
		t.Fatalf("turn = %+v, want text + image + doc blocks", turn)
	}
	text := turn.Blocks[0].Text
	if !strings.Contains(text, "wire the button") ||
		!strings.Contains(text, "[Attached file notes.md]") ||
		!strings.Contains(text, "lime only") ||
		!strings.Contains(text, "[Attached images: shot.png]") {
		t.Errorf("turn text missing prompt, doc, or image marker:\n%s", text)
	}
	ib := turn.Blocks[1]
	if ib.Type != "image" || ib.AttachmentID != img.ID || ib.Name != "shot.png" || ib.Mime != "image/png" {
		t.Errorf("image block = %+v, want the reference", ib)
	}
	if ib.Data != "" {
		t.Error("stored turns must carry references, never pixel bytes")
	}
	db := turn.Blocks[2]
	if db.Type != "doc" || db.AttachmentID != doc.ID || db.Name != "notes.md" {
		t.Errorf("doc block = %+v, want the reference", db)
	}
}

func TestResolveImagesHydratesAndDegrades(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}

	upRec, upReq := testUploadRequest(t, userID, projectID, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(upRec, upReq)
	var img store.Attachment
	if err := json.Unmarshal(upRec.Body.Bytes(), &img); err != nil {
		t.Fatal(err)
	}

	turn := ai.Message{Role: "user", Blocks: []ai.Block{
		{Type: "text", Text: "look"},
		{Type: "image", AttachmentID: img.ID, Name: img.Name, Mime: img.Mime},
		{Type: "image", AttachmentID: 999001, Name: "gone.png", Mime: "image/png"},
	}}
	got := h.resolveImages(projectID, []ai.Message{turn})
	blocks := got[0].Blocks
	if len(blocks) != 3 {
		t.Fatalf("resolved blocks = %+v", blocks)
	}
	if blocks[1].Data == "" {
		t.Error("live image block was not hydrated with bytes")
	}
	if blocks[2].Type != "text" || !strings.Contains(blocks[2].Text, "gone.png") {
		t.Errorf("missing file must degrade to a named note, got %+v", blocks[2])
	}
	// The stored copy is untouched: resolution never writes back bytes.
	if turn.Blocks[1].Data != "" {
		t.Error("resolveImages mutated the caller's stored turn")
	}
}

// fakeBlobStore stands in for R2: bytes by key, no network.
type fakeBlobStore struct {
	files map[string][]byte
}

func (f *fakeBlobStore) Put(_ context.Context, key string, body []byte, _ string) error {
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[key] = bytes.Clone(body)
	return nil
}

func (f *fakeBlobStore) Get(_ context.Context, key string) ([]byte, error) {
	b, ok := f.files[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	return b, nil
}

func (f *fakeBlobStore) Delete(_ context.Context, key string) error {
	delete(f.files, key)
	return nil
}

// Images land in object storage when it is configured: the DB row keeps an
// r2: reference, the bytes never touch local disk, and download + resolve
// both serve from the store.
func TestUploadImageUsesObjectStorageWhenConfigured(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}
	fake := &fakeBlobStore{}
	h.SetR2Store(fake)

	png := testPNGBytes(t, 6, 6)
	rec, req := testUploadRequest(t, userID, projectID, "shot.png", png)
	h.UploadAttachment(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: status %d, body %s", rec.Code, rec.Body.String())
	}
	var att store.Attachment
	if err := json.Unmarshal(rec.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}
	stored, err := st.GetAttachment(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.Path, "r2:") {
		t.Fatalf("reference = %q, want the r2: prefix", stored.Path)
	}
	att = *stored
	if len(fake.files) != 1 {
		t.Fatalf("%d objects stored, want 1", len(fake.files))
	}

	// Download streams the stored bytes back.
	dreq := httptest.NewRequest(http.MethodGet, "/", nil)
	dreq = dreq.WithContext(context.WithValue(dreq.Context(), UserIDKey, userID))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", projectID))
	rctx.URLParams.Add("attID", fmt.Sprintf("%d", att.ID))
	dreq = dreq.WithContext(context.WithValue(dreq.Context(), chi.RouteCtxKey, rctx))
	drec := httptest.NewRecorder()
	h.DownloadAttachment(drec, dreq)
	if drec.Code != http.StatusOK || !bytes.Equal(drec.Body.Bytes(), png) {
		t.Fatalf("download: status %d, bytes match=%v", drec.Code, bytes.Equal(drec.Body.Bytes(), png))
	}

	// Resolution hydrates from the store too.
	turn := ai.Message{Role: "user", Blocks: []ai.Block{
		{Type: "text", Text: "look"},
		{Type: "image", AttachmentID: att.ID, Name: att.Name, Mime: att.Mime},
	}}
	got := h.resolveImages(projectID, []ai.Message{turn})
	if got[0].Blocks[1].Data == "" {
		t.Error("image block was not hydrated from object storage")
	}

	// Markdown docs stay local even with object storage configured.
	mrec, mreq := testUploadRequest(t, userID, projectID, "n.md", []byte("# hi\n"))
	h.UploadAttachment(mrec, mreq)
	var doc store.Attachment
	if err := json.Unmarshal(mrec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	storedDoc, err := st.GetAttachment(doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(storedDoc.Path, "r2:") {
		t.Errorf("doc reference = %q, want a local path", storedDoc.Path)
	}
	if len(fake.files) != 1 {
		t.Errorf("%d objects stored, want 1 — docs must not reach object storage", len(fake.files))
	}
}

// Orphans are uploads nobody sent: unclaimed rows qualify, claimed rows and
// fresh uploads do not. The purge deletes bytes first, rows second, and a
// store it cannot reach keeps its rows for the next pass.
func TestOrphanPurgeDeletesBytesThenRows(t *testing.T) {
	st, userID, projectID := testAttachmentStore(t)
	h := &PromptHandler{store: st, workspaceDir: filepath.Join(t.TempDir(), "ws")}
	fake := &fakeBlobStore{files: map[string][]byte{}}
	h.SetR2Store(fake)

	// One image through the object-storage path, left unclaimed.
	upRec, upReq := testUploadRequest(t, userID, projectID, "shot.png", testPNGBytes(t, 4, 4))
	h.UploadAttachment(upRec, upReq)
	var img store.Attachment
	if err := json.Unmarshal(upRec.Body.Bytes(), &img); err != nil {
		t.Fatal(err)
	}
	// One doc that gets claimed by a turn: never an orphan.
	up2Rec, up2Req := testUploadRequest(t, userID, projectID, "n.md", []byte("# hi\n"))
	h.UploadAttachment(up2Rec, up2Req)
	var doc store.Attachment
	if err := json.Unmarshal(up2Rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.claimAttachments(httptest.NewRecorder(), projectID, 301, []int64{doc.ID}); !ok {
		t.Fatal("claim failed")
	}

	// Fresh uploads are staging, not garbage: nothing qualifies yet.
	if orphans, err := st.ListOrphanAttachments(projectID, 24*time.Hour); err != nil || len(orphans) != 0 {
		t.Fatalf("fresh uploads listed as orphans: %v %v", orphans, err)
	}
	// With a generous cutoff the unclaimed image qualifies, the claimed doc
	// does not.
	orphans, err := st.ListOrphanAttachments(projectID, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].ID != img.ID {
		t.Fatalf("orphans = %+v, want only the unclaimed image", orphans)
	}

	// The purge only sweeps day-old rows, so age the image row first.
	if err := st.BackdateAttachmentCreatedAt(img.ID, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.purgeOrphanAttachments(projectID)
	if len(fake.files) != 0 {
		t.Errorf("%d objects left in storage, want 0", len(fake.files))
	}
	if _, err := st.GetAttachment(img.ID); err == nil {
		t.Error("orphan row survived its bytes")
	}
	if _, err := st.GetAttachment(doc.ID); err != nil {
		t.Errorf("claimed doc wrongly purged: %v", err)
	}
}
