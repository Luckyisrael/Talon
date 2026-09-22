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

// gateExport enforces ownership AND tier, writing the response on failure.
// Tier is checked server-side on every call — hiding UI is not enforcement.
func (h *ExportHandler) gateExport(w http.ResponseWriter, r *http.Request, projectID int64) (*store.Project, bool) {
	project, err := h.store.GetProject(projectID)
	if err != nil || project.UserID != GetUserID(r.Context()) {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	user, err := h.store.GetUserByID(GetUserID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load your account")
		return nil, false
	}
	if !exportAllowed(user.Tier) {
		writeError(w, http.StatusForbidden,
			"Release export needs the Pro plan — upgrade to sign release builds and generate store bundles.")
		return nil, false
	}
	return project, true
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
	if out := approveUserAction(h.store, project.ID, scopeReleaseExport,
		"export release bundle for project "+project.Name); out != approval.AllowedOnce {
		writeError(w, http.StatusForbidden, "export approval is "+out.String())
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
