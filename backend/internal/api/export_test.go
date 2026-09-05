package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/release"
	"github.com/seekersstudio/backend/internal/store"
)

func testSolidPNG(f *os.File, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{10, 20, 30, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return err
	}
	_, err := f.Write(buf.Bytes())
	return err
}

func testExportStore(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	u, err := st.CreateUser("exp@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "exp", "1.0.0", "exp-slug", "com.test.exp", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, p.ID
}

func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	// Reuse the release validator's own fixtures indirectly: build a solid
	// PNG here with the same encoding the unit tests use.
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := testSolidPNG(f, w, h); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestListAssetsEndpoint(t *testing.T) {
	st, projectID := testExportStore(t)
	u, err := st.CreateUser("assets@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTier(u.ID, "pro"); err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(u.ID, "assets", "1.0.0", "assets-slug", "com.test.assets", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = projectID
	if _, err := st.AddAsset(p.ID, "icon", "/tmp/icon.png", 512, 512); err != nil {
		t.Fatal(err)
	}

	h := &ExportHandler{store: st}
	req := authedRequest(u.ID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", p.ID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ListAssets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d, body %s", rec.Code, rec.Body.String())
	}
	var assets []store.Asset
	if err := json.Unmarshal(rec.Body.Bytes(), &assets); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(assets) != 1 || assets[0].Kind != "icon" {
		t.Fatalf("expected 1 icon asset, got %+v", assets)
	}

	// A free-tier user is refused at the gate, not at the list.
	free, err := st.CreateUser("free@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	freeReq := authedRequest(free.ID, "")
	freeReq = freeReq.WithContext(context.WithValue(freeReq.Context(), chi.RouteCtxKey, rctx))
	freeRec := httptest.NewRecorder()
	h.ListAssets(freeRec, freeReq)
	if freeRec.Code != http.StatusNotFound && freeRec.Code != http.StatusForbidden {
		t.Fatalf("expected gate refusal, got %d", freeRec.Code)
	}
}

func TestAssetPathsRejectsMixedSet(t *testing.T) {
	st, pid := testExportStore(t)
	dir := t.TempDir()
	h := &ExportHandler{store: st, builder: &release.Builder{}}

	portrait := writePNG(t, dir, "p.png", 1080, 2400)
	landscape := writePNG(t, dir, "l.png", 1920, 1080)
	if _, err := st.AddAsset(pid, "screenshot", portrait, 1080, 2400); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAsset(pid, "screenshot", landscape, 1920, 1080); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := h.assetPaths(pid); err == nil {
		t.Error("mixed-orientation set should fail at manifest time")
	}
}

func TestAssetPathsConsistentSet(t *testing.T) {
	st, pid := testExportStore(t)
	dir := t.TempDir()
	h := &ExportHandler{store: st, builder: &release.Builder{}}

	a := writePNG(t, dir, "a.png", 1080, 2400)
	b := writePNG(t, dir, "b.png", 1080, 2400)
	if _, err := st.AddAsset(pid, "screenshot", a, 1080, 2400); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAsset(pid, "screenshot", b, 1080, 2400); err != nil {
		t.Fatal(err)
	}
	shots, _, _, err := h.assetPaths(pid)
	if err != nil {
		t.Fatalf("consistent set should pass: %v", err)
	}
	if len(shots) != 2 {
		t.Errorf("expected 2 screenshots, got %d", len(shots))
	}
}
