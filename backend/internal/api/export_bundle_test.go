package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/release"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// bundleRequest puts a job id on the route the way chi would.
func bundleRequest(userID, jobID int64) *http.Request {
	req := authedRequest(userID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", jobID))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// bundleFixture finishes one export the way production does — the real
// WriteBundle writes real files beside a real (stub) artifact, and the job
// records their paths — then hands back a second, non-owning user for the
// gate. CostsNote is deliberately left empty: what has to come back is
// WriteBundle's own default, the honest SOL disclosure, not a value this
// test supplied.
func bundleFixture(t *testing.T) (st *store.Store, ownerID, strangerID, projectID, jobID int64, submitPath string) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "bundle.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	owner, err := st.CreateUser("bundle-owner@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.CreateUser("bundle-stranger@test.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(owner.ID, "bundle", "1.0.0", "bundle-slug", "com.talon.l38demo", "", "")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	apk := filepath.Join(dir, "release.apk")
	if err := os.WriteFile(apk, []byte("PK stub artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
	submitPath = filepath.Join(dir, "SUBMIT.md")
	manifestPath := filepath.Join(dir, "dapp-store.json")
	if _, err := release.WriteBundle(dir, release.SubmissionBundle{
		ProjectID:      p.ID,
		ProjectName:    "bundle",
		AndroidPackage: p.AndroidPackage,
		VersionCode:    7,
		VersionName:    "1.0.0",
		SignerSHA256:   strings.Repeat("ab", 32),
		APK:            apk,
		WhatsNew:       "first release",
	}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	job, err := st.CreateExportJob(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateExportJob(job.ID, "done", apk, manifestPath, ""); err != nil {
		t.Fatal(err)
	}
	return st, owner.ID, stranger.ID, p.ID, job.ID, submitPath
}

// FM-SUBa(i)/(iv)/(v) — the bundle is written and never handed over.
//
// The assertions are made against files the REAL WriteBundle produced and
// then read back through the route: the response has to be those bytes, not
// a reconstruction, or a client renders a step the server never wrote. The
// manifest is read as plain JSON on purpose — the contract is the file, not
// whichever Go struct happens to hold it.
func TestExportBundleHandsOverTheReadmeAndManifest(t *testing.T) {
	st, ownerID, strangerID, projectID, jobID, submitPath := bundleFixture(t)
	h := &ExportHandler{store: st}

	rec := httptest.NewRecorder()
	h.Bundle(rec, bundleRequest(ownerID, jobID))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner of a finished export: status %d, want %d (body %s)",
			rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		SubmitMD string          `json:"submit_md"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the bundle must be machine-readable: %v (body %s)", err, rec.Body.String())
	}
	if len(body.Manifest) == 0 {
		t.Fatal("the manifest is missing from the response — the client would have to invent it")
	}

	onDisk, err := os.ReadFile(submitPath)
	if err != nil {
		t.Fatal(err)
	}
	if body.SubmitMD != string(onDisk) {
		t.Errorf("submit_md is not SUBMIT.md verbatim:\n got %.160q\nwant %.160q",
			body.SubmitMD, onDisk)
	}
	if !strings.Contains(body.SubmitMD, "Option A — Publisher Portal") {
		t.Errorf("submit_md does not carry the portal steps: %.200q", body.SubmitMD)
	}

	var manifest map[string]any
	if err := json.Unmarshal(body.Manifest, &manifest); err != nil {
		t.Fatalf("manifest must be the file that was written: %v", err)
	}
	// FM-SUBa(ii): the documented portal address, served rather than guessed.
	if got, _ := manifest["portal_url"].(string); got != "https://publish.solanamobile.com" {
		t.Errorf("portal_url = %q, want the documented https://publish.solanamobile.com", got)
	}
	// The package the portal matches by — and the one the README quotes.
	if got, _ := manifest["android_package"].(string); got != "com.talon.l38demo" {
		t.Errorf("android_package = %q, want com.talon.l38demo", got)
	}
	if !strings.Contains(body.SubmitMD, "com.talon.l38demo") {
		t.Error("SUBMIT.md never names the package the portal matches by")
	}
	// FM-SUBa(iii): the disclosure is the server's to state. The fixture
	// supplies no CostsNote, so anything but the shipped default is a lie.
	if got, _ := manifest["costs_note"].(string); !strings.Contains(got, "0.2 SOL") {
		t.Errorf("costs_note = %q, want the shipped ~0.2 SOL disclosure", got)
	}

	// FM-SUBa(v): a guessed job id must not read another publisher's
	// package, signer and release notes.
	rec2 := httptest.NewRecorder()
	h.Bundle(rec2, bundleRequest(strangerID, jobID))
	if rec2.Code != http.StatusNotFound {
		t.Errorf("non-owner: status %d, want 404", rec2.Code)
	}

	// FM-SUBa(iv): a build that has not finished has no bundle to hand over,
	// and saying so beats an empty answer a client would render as done.
	running, err := st.CreateExportJob(projectID)
	if err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	h.Bundle(rec3, bundleRequest(ownerID, running.ID))
	if rec3.Code != http.StatusConflict {
		t.Errorf("queued job: status %d, want 409 (body %s)", rec3.Code, rec3.Body.String())
	}
	if !strings.Contains(rec3.Body.String(), "queued") {
		t.Errorf("the refusal must name the job's actual state, got %s", rec3.Body.String())
	}
}

// FM-SUBa(ii) — the portal address has exactly one source. The README and
// the served manifest are two places a client copies it from; two literals is
// how a panel ends up pointing at somewhere the docs no longer recognise.
func TestPortalURLIsOneConstantNotAClientLiteral(t *testing.T) {
	dir := t.TempDir()
	if _, err := release.WriteBundle(dir, release.SubmissionBundle{
		ProjectID:      1,
		ProjectName:    "one",
		AndroidPackage: "com.talon.one",
		VersionCode:    1,
		VersionName:    "1.0.0",
		APK:            filepath.Join(dir, "one.apk"),
		WhatsNew:       "n",
	}); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(dir, "SUBMIT.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), release.PortalURL) {
		t.Errorf("SUBMIT.md does not link the portal constant %q", release.PortalURL)
	}

	data, err := os.ReadFile(filepath.Join(dir, "dapp-store.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written release.SubmissionBundle
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatalf("decode dapp-store.json: %v", err)
	}
	if written.PortalURL != release.PortalURL {
		t.Errorf("dapp-store.json portal_url = %q, want the same constant %q", written.PortalURL, release.PortalURL)
	}
}
