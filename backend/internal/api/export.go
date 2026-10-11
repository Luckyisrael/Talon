package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/harness/approval"
	"github.com/luckyisrael/talon/backend/internal/metrics"
	"github.com/luckyisrael/talon/backend/internal/release"
	"github.com/luckyisrael/talon/backend/internal/secrets"
	"github.com/luckyisrael/talon/backend/internal/skr"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// ExportHandler serves the release-export flow: tier-gated APK export,
// dApp Store submission bundles, and validated store assets. Export is async
// (a release gradle build takes tens of minutes) and fully isolated from the
// dev loop. It never touches a publisher wallet — submission stays the user's.
type ExportHandler struct {
	store   *store.Store
	keys    *release.Keystore
	builder *release.Builder
}

func NewExportHandler(s *store.Store, cipher *secrets.Cipher, workspaceDir, templateDir, exportsDir string) *ExportHandler {
	keys := &release.Keystore{Store: s, Cipher: cipher}
	return &ExportHandler{
		store: s,
		keys:  keys,
		builder: &release.Builder{
			Store:        s,
			Keys:         keys,
			WorkspaceDir: workspaceDir,
			TemplateDir:  templateDir,
			ExportsDir:   exportsDir,
		},
	}
}

// exportTiers are the tiers allowed to export. Free is out — export is the
// Pro feature Phase 10 gated.
func exportAllowed(tier string) bool {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "pro", "team", "enterprise", "unlimited":
		return true
	default:
		return false
	}
}

// gateExport enforces ownership, writing the response on failure. Tier is
// deliberately NOT checked here: this helper is shared by Create, UploadAsset
// and ListAssets, and the store checklist is preparation rather than
// shipping — an owner must be able to read and add their own screenshots at a
// balance of zero, which is also the only way the paywall sheet can render the
// offer it exists to sell (ExportPanel calls ListAssets the moment it opens).
// Paying for the build belongs to chargeExportCredit, which only Create calls:
// hiding UI is not enforcement, but neither is charging for the screen that
// explains the charge.
func (h *ExportHandler) gateExport(w http.ResponseWriter, r *http.Request, projectID int64) (*store.Project, bool) {
	project, err := h.store.GetProject(projectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// The offer the refusal quotes back, read through the skr package rather than
// re-parsed here: the price the paywall quotes and the price the chain is
// asked to confirm must be one number with one default. quota.go's comment
// about three disagreeing copies of a limit is exactly why this is not a
// second getenv with a second 100/5 sitting next to it.
func packAmount() int  { return skr.Price() }
func packCredits() int { return skr.Credits() }

// chargeExportCredit is the paywall, and the release build is the only surface
// it guards. It runs in two stages so that neither half can be wrong in the
// way the other one protects against:
//
//	mayExport        — refuses early, spends nothing. A zero-balance owner is
//	                   told what is missing before any verification or approval
//	                   work runs, and a project that later fails verification
//	                   has cost them nothing.
//	takeExportCredit — spends, and sits immediately before the job exists, so
//	                   the invariant is exact: one credit bought one export job.
//
// The early read is never a reservation. The spend is its own conditional
// UPDATE, so a balance of 1 still admits exactly one of two exports that reach
// takeExportCredit together — mayExport only ever decides *when* to refuse.
func (h *ExportHandler) exportUser(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	user, err := h.store.GetUserByID(GetUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return nil, false
	}
	return user, true
}

// mayExport is the fast paywall. Paid tiers always pass: exportAllowed has
// always been their entitlement, they hold no ledger rows, and inventing a
// 402 they cannot clear would take away something they already have.
func (h *ExportHandler) mayExport(w http.ResponseWriter, user *store.User) bool {
	if exportAllowed(user.Tier) {
		return true
	}
	spare, err := h.store.SpareCredits(user.ID)
	if err != nil {
		log.Printf("api: export pre-check credits read failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read your export credits")
		return false
	}
	if spare <= 0 {
		h.writeCreditsRequired(w, spare)
		return false
	}
	return true
}

// takeExportCredit takes the payment for the job it is about to create.
func (h *ExportHandler) takeExportCredit(w http.ResponseWriter, user *store.User) bool {
	if exportAllowed(user.Tier) {
		return true
	}
	spent, err := h.store.ConsumeCredit(user.ID)
	if err != nil {
		log.Printf("api: export credit spend failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read your export credits")
		return false
	}
	if !spent {
		h.writeCreditsRequired(w, 0)
		return false
	}
	return true
}

// writeCreditsRequired answers 402 Payment Required with the offer inline, so
// the unlock sheet renders from the refusal alone — no second round trip and
// no prose-parsing. A 403 would say "forbidden, ever" and hand the client
// nothing to unlock against.
func (h *ExportHandler) writeCreditsRequired(w http.ResponseWriter, spare int) {
	body := map[string]interface{}{
		"error":               "a release build spends one credit — you have none left",
		"code":                "export_credits_required",
		"spare_credits":       spare,
		"credits_per_export":  1,
		"skr_amount_per_pack": packAmount(),
		"credits_per_pack":    packCredits(),
	}
	// On the devnet-SOL test rail the refusal quotes the SOL price too, so
	// the sheet renders from the refusal alone (FM-SKRd(iii)) — same law,
	// second rail.
	if skr.DevnetSolActive() {
		body["sol_lamports_per_pack"] = skr.SolPackLamports()
	}
	writeJSON(w, http.StatusPaymentRequired, body)
}

type exportRequest struct {
	WhatsNew string `json:"whats_new"`
	// Override is the W6 named human key: when verification policy refuses
	// the release (required gate skipped/failing, best-effort state, or no
	// record at all), override:true ships anyway and the reason lands in
	// audit_log under verify_override. Omitted = fail closed.
	Override bool `json:"override"`
}

// Create starts an async export: POST /v1/projects/{id}/export
// Body: {"whats_new": "release notes shown in the manifest"}. Returns 202.
func (h *ExportHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	project, ok := h.gateExport(w, r, projectID)
	if !ok {
		return
	}
	user, ok := h.exportUser(w, r)
	if !ok {
		return
	}

	var req exportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return
	}
	req.WhatsNew = strings.TrimSpace(req.WhatsNew)
	if req.WhatsNew == "" {
		writeError(w, http.StatusBadRequest, "whats_new is required — it goes into the submission bundle as the release notes")
		return
	}

	// The paywall answers before any verification or approval work runs, and
	// it spends nothing: a zero-balance owner is told what is missing without
	// paying for a release that was never going to start.
	if !h.mayExport(w, user) {
		return
	}

	// W6: policy gates the release before any approval work — a refused,
	// best-effort, or never-verified build does not reach the seam. An
	// omitted override fails closed (laws 6/15).
	if !h.enforceVerifyPolicy(w, r, project, req.Override) {
		return
	}

	// H4/W1.6: the export mints and bundles the project's signing keystores —
	// one Request through the same seam tier-2 shell uses, one audit event to
	// the session log. The authenticated POST is the consent; a seam that
	// cannot answer refuses (fail closed, never unseamed).
	var ws string
	if h.builder != nil {
		ws = filepath.Join(h.builder.WorkspaceDir, strconv.FormatInt(project.ID, 10))
	}
	if out := approveUserAction(h.store, project.ID, ws, scopeReleaseExport,
		"export release bundle for project "+project.Name); out != approval.AllowedOnce {
		writeError(w, http.StatusForbidden, "export approval is "+out.String())
		return
	}

	// The credit is taken here — after every refusal that costs no compute,
	// immediately before the job it pays for — so an unpaid owner is never
	// charged for a release that policy or the approval seam turned down.
	if !h.takeExportCredit(w, user) {
		return
	}

	job, err := h.store.CreateExportJob(project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the export")
		return
	}

	// The build outlives this request by design — tens of minutes — so it gets
	// its own context, like the native rebuild path. The dev loop is untouched.
	go h.runExport(job.ID, project.ID, req.WhatsNew)

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"job_id":     job.ID,
		"project_id": project.ID,
		"status":     "queued",
	})
}

func (h *ExportHandler) runExport(jobID, projectID int64, whatsNew string) {
	fail := func(err error) {
		log.Printf("export job %d (project %d) failed: %v", jobID, projectID, err)
		metrics.Default.ObserveExport(true)
		if uerr := h.store.UpdateExportJob(jobID, "failed", "", "", err.Error()); uerr != nil {
			log.Printf("export job %d: could not record failure: %v", jobID, uerr)
		}
	}

	if err := h.store.UpdateExportJob(jobID, "running", "", "", ""); err != nil {
		log.Printf("export job %d: could not mark running: %v", jobID, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	artifact, err := h.builder.Build(ctx, projectID)
	if err != nil {
		fail(err)
		return
	}

	if err := h.writeBundle(projectID, artifact, whatsNew); err != nil {
		fail(err)
		return
	}
	manifest := manifestPathFor(artifact)
	if err := h.store.UpdateExportJob(jobID, "done", artifact, manifest, ""); err != nil {
		log.Printf("export job %d: could not record success: %v", jobID, err)
	}
	metrics.Default.ObserveExport(false)
}

func manifestPathFor(artifact string) string {
	return filepath.Join(filepath.Dir(artifact), "dapp-store.json")
}

// writeBundle builds the submission bundle from the verified artifact: versions
// read from the APK itself, signer from the verified certificate, identity
// from the project row, whats-new from the export request. No placeholders.
func (h *ExportHandler) writeBundle(projectID int64, artifact, whatsNew string) error {
	project, err := h.store.GetProject(projectID)
	if err != nil {
		return err
	}
	ks, err := h.store.GetKeystore(projectID)
	if err != nil {
		return fmt.Errorf("no keystore for project: %w", err)
	}
	shots, icon, banner, err := h.assetPaths(projectID)
	if err != nil {
		return err
	}
	bundle := release.SubmissionBundle{
		ProjectID:      projectID,
		ProjectName:    project.Name,
		AndroidPackage: project.AndroidPackage,
		SignerSHA256:   ks.Fingerprint,
		APK:            artifact,
		WhatsNew:       whatsNew,
		Assets: release.AssetSet{
			Screenshots: shots,
			Icon:        icon,
			Banner:      banner,
		},
	}
	versions, err := release.APKVersions(artifact)
	if err != nil {
		return err
	}
	bundle.VersionCode = versions.Code
	bundle.VersionName = versions.Name
	_, err = release.WriteBundle(filepath.Dir(artifact), bundle)
	return err
}

// Get returns one export job: GET /v1/exports/{id}. A job is readable only
// through the project that owns it.
func (h *ExportHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid export id")
		return
	}
	job, err := h.store.GetExportJob(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	project, err := h.store.GetProject(job.ProjectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// Download serves the signed APK: GET /v1/exports/{id}/download. Only when
// the job is done — a failed or running job has nothing to serve.
func (h *ExportHandler) Download(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid export id")
		return
	}
	job, err := h.store.GetExportJob(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	project, err := h.store.GetProject(job.ProjectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	if job.Status != "done" || job.ArtifactPath == "" {
		writeError(w, http.StatusConflict, fmt.Sprintf("export is %s — nothing to download yet", job.Status))
		return
	}
	http.ServeFile(w, r, job.ArtifactPath)
}

// submissionBundleResponse hands over the two files WriteBundle wrote beside
// the APK. The files ARE the contract: they are served as bytes rather than
// re-derived from a struct, so a surface renders exactly the steps, package,
// costs and portal address the server committed to (FM-SUBa(i)/(iii)).
type submissionBundleResponse struct {
	SubmitMD string          `json:"submit_md"`
	Manifest json.RawMessage `json:"manifest"`
}

// Bundle serves the submission bundle: GET /v1/exports/{id}/bundle.
//
// It exists because WriteBundle's output is only ever written to the
// server's disk — manifest_path is an absolute path no phone or browser can
// reach — so today a user who paid for an export gets an APK and no idea what
// to do with it. The gate is the one Get and Download already take: the
// owning project, then a build that has actually finished (a queued job has
// no bundle, and saying which state the job is in beats an empty answer a
// client would render as done).
func (h *ExportHandler) Bundle(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid export id")
		return
	}
	job, err := h.store.GetExportJob(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	project, err := h.store.GetProject(job.ProjectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "export not found")
		return
	}
	if job.Status != "done" {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("export is %s — the submission bundle is written when the build finishes", job.Status))
		return
	}
	if job.ManifestPath == "" {
		writeError(w, http.StatusNotFound, "submission bundle not found")
		return
	}
	manifest, err := os.ReadFile(job.ManifestPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "submission bundle not found")
		return
	}
	// SUBMIT.md is written beside the manifest, in the artifact's directory.
	submit, err := os.ReadFile(filepath.Join(filepath.Dir(job.ManifestPath), "SUBMIT.md"))
	if err != nil {
		writeError(w, http.StatusNotFound, "submission bundle not found")
		return
	}
	writeJSON(w, http.StatusOK, submissionBundleResponse{
		SubmitMD: string(submit),
		Manifest: json.RawMessage(manifest),
	})
}

// UploadAsset validates and stores one store asset:
// POST /v1/projects/{id}/assets (multipart: kind=screenshot|icon|banner,
// file=<png/jpeg>). Validation failures are 400s with the reason — a bad
// asset never enters the collection silently.
func (h *ExportHandler) UploadAsset(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.gateExport(w, r, projectID); !ok {
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the upload (max 32MB)")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.FormValue("kind")))
	if kind != "screenshot" && kind != "icon" && kind != "banner" {
		writeError(w, http.StatusBadRequest, "kind must be screenshot, icon, or banner — they are distinct asset types")
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "could not read the file")
		return
	}

	var width, height int
	switch kind {
	case "screenshot":
		o, verr := release.ValidateScreenshot(data)
		if verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
		_ = o
		width, height, _ = release.Dimensions(data)
	case "icon":
		if verr := release.ValidateIcon(data); verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
		width, height, _ = release.Dimensions(data)
	case "banner":
		if verr := release.ValidateBanner(data); verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
		width, height, _ = release.Dimensions(data)
	}

	dir := filepath.Join(h.builder.ExportsDir, "assets", strconv.FormatInt(projectID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the asset")
		return
	}
	ext := ".png"
	// Nanosecond timestamp: two uploads in the same second must not share a
	// filename and silently overwrite each other.
	name := fmt.Sprintf("%s-%d%s", kind, time.Now().UnixNano(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store the asset")
		return
	}
	asset, err := h.store.AddAsset(projectID, kind, path, width, height)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not record the asset")
		return
	}

	// Screenshots are validated as a set on read, not per upload: orientation
	// consistency is a property of the collection.
	writeJSON(w, http.StatusCreated, asset)
}

// GET /v1/projects/{id}/assets — the store checklist: validated screenshots,
// icon, and banner recorded for this project. The ExportPanel reads this to
// show what the submission bundle will contain before the user starts a
// (long, Pro-gated) release build.
func (h *ExportHandler) ListAssets(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	if _, ok := h.gateExport(w, r, projectID); !ok {
		return
	}
	assets, err := h.store.ListAssets(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list assets")
		return
	}
	if assets == nil {
		assets = []store.Asset{}
	}
	writeJSON(w, http.StatusOK, assets)
}

// assetPaths collects validated assets for the manifest: screenshots in
// upload order, the latest icon and banner. Screenshot-set orientation is
// re-checked here so a set that drifted since upload cannot slip through.
func (h *ExportHandler) assetPaths(projectID int64) (shots []string, icon, banner string, err error) {
	assets, err := h.store.ListAssets(projectID)
	if err != nil {
		return nil, "", "", err
	}
	var shotData [][]byte
	for _, a := range assets {
		switch a.Kind {
		case "screenshot":
			shots = append(shots, a.Path)
			if data, rerr := os.ReadFile(a.Path); rerr == nil {
				shotData = append(shotData, data)
			}
		case "icon":
			icon = a.Path
		case "banner":
			banner = a.Path
		}
	}
	if len(shotData) > 0 {
		if err := release.ValidateScreenshotSet(shotData); err != nil {
			return nil, "", "", fmt.Errorf("screenshot set invalid: %w", err)
		}
	}
	return shots, icon, banner, nil
}

// LoadReleaseCipher builds the keystore password cipher from the environment,
// following the git-credential pattern: strict in production, loud dev fallback.
func LoadReleaseCipher() *secrets.Cipher {
	key := strings.TrimSpace(os.Getenv("RELEASE_KEYSTORE_KEY"))
	if key == "" {
		key = "insecure-development-keystore-key-do-not-ship"
		log.Println("release: WARNING — RELEASE_KEYSTORE_KEY unset; using an insecure development key. Set a 32+ char key before production.")
	}
	return secrets.FromPassphrase(key)
}
