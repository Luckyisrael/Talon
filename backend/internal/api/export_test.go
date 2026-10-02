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
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/release"
	"github.com/luckyisrael/talon/backend/internal/store"
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

	// An account that does not own the project is refused at the gate, not at
	// the list. This is an ownership assertion, not a tier one: asset
	// preparation is free for every owner (TestStoreChecklistIsNotPaywalled),
	// so what stops this caller is that the project is not theirs.
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

// exportOwner spins a fresh store holding a free-tier owner and one project —
// the state every paywall case starts from. The fixture refuses to build if
// the account it hands back would already pass the tier gate, so a refactor
// of the default tier cannot quietly turn a red into a green.
func exportOwner(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "paywall.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	u, err := st.CreateUser("free-owner@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if tier := mustTier(t, st, u.ID); exportAllowed(tier) {
		t.Fatalf("fixture must start unpaid, tier %q may export for free", tier)
	}
	p, err := st.CreateProject(u.ID, "paywall", "1.0.0", "paywall-slug", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return st, u.ID, p.ID
}

// exportHandler builds a handler for a project that carries no android package
// identity: release.Builder.Build refuses on its very first check in that case,
// so the background job the 202 spawns fails deterministically in milliseconds
// instead of reaching for a keystore, keytool or this machine's Android SDK.
// The paywall is what is under test here — not gradle.
func exportHandler(st *store.Store) *ExportHandler {
	return &ExportHandler{store: st, builder: &release.Builder{Store: st}}
}

// drainExportJob waits for the build the 202 spawned to leave the queue, so the
// test never closes the store out from under its own background goroutine.
func drainExportJob(t *testing.T, st *store.Store, jobID int64) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := st.GetExportJob(jobID)
		if err != nil {
			t.Fatalf("GetExportJob(%d): %v", jobID, err)
		}
		if job.Status != "queued" && job.Status != "running" {
			return job.Status
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "still-running"
}

// exportPOST drives Create exactly as a client does: POST with a route id and
// a release-notes body.
func exportPOST(t *testing.T, h *ExportHandler, userID, projectID int64) *httptest.ResponseRecorder {
	t.Helper()
	req := authedRequest(userID, `{"whats_new":"first release","override":true}`)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", projectID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

// FM-CRDa(i)/(ii) — an owner with nothing to spend must be refused the way a
// payment failure is refused: a 402 whose body carries the offer, so a client
// can render "100 SKR unlocks 5 exports" straight from the refusal. Today the
// tier gate answers 403 with a Pro upsell sentence, which says "forbidden,
// ever" and hands the app no machine-readable reason to unlock against.
func TestExportWithoutSpareCreditsIsPaymentRequired(t *testing.T) {
	st, userID, projectID := exportOwner(t)

	rec := exportPOST(t, &ExportHandler{store: st}, userID, projectID)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("zero-balance free owner: status %d, want %d (body %s)",
			rec.Code, http.StatusPaymentRequired, rec.Body.String())
	}
	var body struct {
		Code             string `json:"code"`
		SpareCredits     int    `json:"spare_credits"`
		SKRPerPack       int    `json:"skr_amount_per_pack"`
		CreditsPerPack   int    `json:"credits_per_pack"`
		CreditsPerExport int    `json:"credits_per_export"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the refusal must be machine-readable, not prose: %v (body %s)", err, rec.Body.String())
	}
	if body.Code != "export_credits_required" {
		t.Errorf("code = %q, want export_credits_required", body.Code)
	}
	if body.SpareCredits != 0 {
		t.Errorf("spare_credits = %d, want 0", body.SpareCredits)
	}
	if body.SKRPerPack <= 0 || body.CreditsPerPack <= 0 || body.CreditsPerExport <= 0 {
		t.Errorf("the refusal must carry the offer itself: skr=%d pack=%d per=%d",
			body.SKRPerPack, body.CreditsPerPack, body.CreditsPerExport)
	}
}

// FM-CRDa(i) — preparation is free. The store checklist (ListAssets /
// UploadAsset) is not shipping, and ExportPanel calls it the moment it opens:
// if reaching it at a balance of zero is already a payment failure, the sheet
// that sells the credit 402s before it can ever render the offer.
func TestStoreChecklistIsNotPaywalled(t *testing.T) {
	st, userID, projectID := exportOwner(t)
	if _, err := st.AddAsset(projectID, "icon", "/tmp/icon.png", 512, 512); err != nil {
		t.Fatal(err)
	}

	h := &ExportHandler{store: st}
	req := authedRequest(userID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", projectID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.ListAssets(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("free owner with no credits: list status %d, want 200 (body %s)",
			rec.Code, rec.Body.String())
	}
	var assets []store.Asset
	if err := json.Unmarshal(rec.Body.Bytes(), &assets); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(assets) != 1 || assets[0].Kind != "icon" {
		t.Fatalf("expected the icon back, got %+v", assets)
	}
}

// FM-CRDa(vi) — an unpaid owner who has bought a credit must actually get the
// export, and must be charged for it exactly once. Both halves live in one
// test because the failure mode is the coupling: reaching 202 without spending
// is a free build, and spending without reaching 202 is a stolen credit.
func TestFreeOwnerWithSpareCreditsStartsTheExportAndSpendsOne(t *testing.T) {
	st, userID, projectID := exportOwner(t)

	granted, err := st.GrantCredits(userID, 1, "skr", "sig-funded-1")
	if err != nil {
		t.Fatalf("GrantCredits: %v", err)
	}
	if !granted {
		t.Fatal("the first grant must write a ledger row")
	}
	if before, err := st.SpareCredits(userID); err != nil || before != 1 {
		t.Fatalf("balance after grant = %d (err %v), want 1", before, err)
	}

	rec := exportPOST(t, exportHandler(st), userID, projectID)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("funded free owner: status %d, want %d (body %s)",
			rec.Code, http.StatusAccepted, rec.Body.String())
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || started.JobID == 0 {
		t.Fatalf("202 must name the job it started: %v (body %s)", err, rec.Body.String())
	}

	after, err := st.SpareCredits(userID)
	if err != nil {
		t.Fatalf("SpareCredits: %v", err)
	}
	if after != 0 {
		t.Errorf("balance after export = %d, want 0 — the build must be paid for", after)
	}
	if status := drainExportJob(t, st, started.JobID); status == "queued" || status == "running" {
		t.Errorf("background job never settled: %s", status)
	}
}

// FM-CRDa(iii) — a balance of 1 admits exactly one export. The spend has to be
// one conditional UPDATE: a balance read followed by a separate decrement
// leaves a window in which several requests all see the last credit and all
// proceed, which is exactly how a zero-balance owner launches parallel builds.
// The correct implementation is deterministic under any scheduling — the
// assertion does not depend on the racers actually overlapping.
func TestConsumeCreditAdmitsExactlyOneOfConcurrentExports(t *testing.T) {
	st, userID, _ := exportOwner(t)
	if granted, err := st.GrantCredits(userID, 1, "skr", "sig-concurrent"); err != nil || !granted {
		t.Fatalf("GrantCredits = %v, %v", granted, err)
	}

	const racers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		errs []error
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := st.ConsumeCredit(userID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if ok {
				won++
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("consume errors: %v", errs)
	}
	if won != 1 {
		t.Errorf("balance of 1 admitted %d of %d racers — a credit was duplicated or lost", won, racers)
	}
	if spare, err := st.SpareCredits(userID); err != nil || spare != 0 {
		t.Errorf("balance after the race = %d (err %v), want 0", spare, err)
	}
}

// FM-CRDa(v) — exportAllowed keeps passing, exactly as it did before the
// ledger existed. A Pro owner holds no ledger rows, must never see a 402 they
// cannot clear (they would have no way to buy their way out of it), and must
// not have a balance invented for them. Charging everyone is the failure this
// pins: an unpaid owner is the only one who should ever be asked.
func TestPaidTierIsNeverCharged(t *testing.T) {
	st, userID, projectID := exportOwner(t)
	if err := st.SetTier(userID, "pro"); err != nil {
		t.Fatal(err)
	}

	rec := exportPOST(t, exportHandler(st), userID, projectID)
	if rec.Code == http.StatusPaymentRequired {
		t.Fatalf("a paid tier was asked to pay: %s", rec.Body.String())
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pro export: status %d, want %d (body %s)",
			rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if spare, err := st.SpareCredits(userID); err != nil || spare != 0 {
		t.Errorf("a paid owner was handed a balance it never bought: %d (err %v)", spare, err)
	}
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err == nil && started.JobID != 0 {
		drainExportJob(t, st, started.JobID)
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
