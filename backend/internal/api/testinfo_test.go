package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/luckyisrael/talon/backend/internal/devclient"
	"github.com/luckyisrael/talon/backend/internal/relay"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// The phone surface is honest: available=false carries the reason, and the
// deep link is exactly the relay's public port.
func TestTestInfoHonesty(t *testing.T) {
	st, err := store.New(t.TempDir() + "/ti.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	u, err := st.CreateUser("ti@test", "x")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	proj, err := st.CreateProject(u.ID, "p", "v", "ti-slug", "a.b", "c.d", "e")
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	// No session: unavailable with the reason.
	info := fetchTestInfo(t, st, u, proj.ID)
	if info.Available {
		t.Error("fault: available with no session")
	}
	if info.Reason == "" {
		t.Error("fault: unavailable without a reason")
	}

	// A live session with a relay: the deep link names the relay port.
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	metro, err := relay.StartForSession(sess.ID, 17999, "127.0.0.1:8081")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	defer relay.StopSession(sess.ID)

	info = fetchTestInfo(t, st, u, proj.ID)
	if !info.Available {
		t.Fatalf("fault: live session + relay reported unavailable (%s)", info.Reason)
	}
	// §2.4: the deep link names the relay port AND carries the relay's
	// session token — a bare location is an open door.
	wantPrefix := "exp://" + publicHost() + ":17999?token="
	if !strings.HasPrefix(info.DeepLink, wantPrefix) || len(info.DeepLink) <= len(wantPrefix) {
		t.Errorf("fault: deep link %q, want %q<relay token>", info.DeepLink, wantPrefix)
	}
	if got := metro.Token(); got == "" || !strings.HasSuffix(info.DeepLink, got) {
		t.Errorf("fault: deep link token does not match the relay's (%q)", got)
	}
	_ = metro
}

func fetchTestInfo(t *testing.T, st *store.Store, u *store.User, projectID int64) testInfo {
	t.Helper()
	t.Setenv("PUBLIC_HOST", "localhost")
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), nil)
	req := authedRequest(u.ID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Info(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var info testInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return info
}

// ── L32 / FM-DEVa: the APK door, tested at every layer ──────────────

// templateWithAPK builds a dev-client manager over a temp template dir.
// apkBytes nil = "not built on this server".
func templateWithAPK(t *testing.T, apkBytes []byte) *devclient.Manager {
	t.Helper()
	dir := t.TempDir()
	const version = "0.0.0-test"
	if err := os.WriteFile(filepath.Join(dir, ".template-version"), []byte(version+"\n"), 0o644); err != nil {
		t.Fatalf("write .template-version: %v", err)
	}
	if apkBytes != nil {
		apkPath := devclient.DevClientAPKPath(dir, version)
		if err := os.MkdirAll(filepath.Dir(apkPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(apkPath, apkBytes, 0o644); err != nil {
			t.Fatalf("write apk: %v", err)
		}
	}
	mgr, err := devclient.NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

// routeRequest forges an owner-authed request for the given route, the way
// the chi router would deliver it.
func routeRequest(u *store.User, projectID int64) *http.Request {
	req := authedRequest(u.ID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(projectID, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// infoStore seeds a store with one user and one project.
func infoStore(t *testing.T) (*store.Store, *store.User, *store.Project) {
	t.Helper()
	st, err := store.New(t.TempDir() + "/apk.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	u, err := st.CreateUser("apk@test", "x")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	proj, err := st.CreateProject(u.ID, "p", "v", "apk-slug", "a.b", "c.d", "e")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return st, u, proj
}

// FM-DEVa(i): the download link is not the session's to grant. The APK is an
// owner-scoped static file; a first-time phone installs it BEFORE there is a
// session to pair with, so the no-session answer must carry the URL too.
func TestTestInfoAdvertisesTheAPKBuiltOnDiskWithoutSession(t *testing.T) {
	st, u, proj := infoStore(t)
	mgr := templateWithAPK(t, []byte("PK\x03\x04fake-apk"))
	t.Setenv("PUBLIC_HOST", "localhost")
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	rec := httptest.NewRecorder()
	h.Info(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var info testInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if info.Available {
		t.Error("fault: no session, yet available=true — the link stays honest about pairing")
	}
	if info.DevClientURL == "" {
		t.Fatal("fault: the APK is built on disk but test-info hides its download until a session runs — a first-time phone cannot install (FM-DEVa)")
	}
	want := "/v1/projects/" + strconv.FormatInt(proj.ID, 10) + "/dev-client.apk"
	if !strings.HasSuffix(info.DevClientURL, want) {
		t.Errorf("fault: dev_client_url %q, want suffix %q", info.DevClientURL, want)
	}
}

// The URL's presence means exactly "built on disk" — both clients key their
// fallback copy on that, so an absent file must keep it empty.
func TestTestInfoOmitsTheAPKWhenNotBuilt(t *testing.T) {
	st, u, proj := infoStore(t)
	mgr := templateWithAPK(t, nil)
	t.Setenv("PUBLIC_HOST", "localhost")
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	rec := httptest.NewRecorder()
	h.Info(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var info testInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.DevClientURL != "" {
		t.Errorf("fault: dev_client_url %q names a build that does not exist", info.DevClientURL)
	}
}

// The provision itself: owner gets the bytes with download headers.
func TestDevClientAPKServesTheBuild(t *testing.T) {
	st, u, proj := infoStore(t)
	apkBytes := []byte("PK\x03\x04the-whole-build")
	mgr := templateWithAPK(t, apkBytes)
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	rec := httptest.NewRecorder()
	h.DevClientAPK(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/vnd.android.package-archive" {
		t.Errorf("fault: Content-Type %q, want the APK type", got)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "talon-devclient.apk") {
		t.Errorf("fault: Content-Disposition %q, want attachment; talon-devclient.apk", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), apkBytes) {
		t.Errorf("fault: body %q, want the stored APK bytes", rec.Body.Bytes())
	}
}

// Not built here → an honest 404 naming the fix, never a 500 or empty body.
func TestDevClientAPKNotBuiltIs404(t *testing.T) {
	st, u, proj := infoStore(t)
	mgr := templateWithAPK(t, nil)
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	rec := httptest.NewRecorder()
	h.DevClientAPK(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not built") {
		t.Errorf("fault: 404 body %q does not name the fix", rec.Body.String())
	}
}

// Project-scoped auth: a stranger gets the same 404 as anyone guessing a URL —
// no existence leak, no 271MB pull.
func TestDevClientAPKRequiresOwnership(t *testing.T) {
	st, u, proj := infoStore(t)
	stranger, err := st.CreateUser("stranger@test", "x")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	mgr := templateWithAPK(t, []byte("PK\x03\x04fake-apk"))
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	rec := httptest.NewRecorder()
	h.DevClientAPK(rec, routeRequest(stranger, proj.ID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d for a non-owner, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "project not found") {
		t.Errorf("fault: body %q — a stranger must see the ordinary missing-project answer", rec.Body.String())
	}
	_ = u
}

// Unconfigured server (no devclient manager at all) is a 503, not a panic.
func TestDevClientAPKUnconfiguredIs503(t *testing.T) {
	st, u, proj := infoStore(t)
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), nil)

	rec := httptest.NewRecorder()
	h.DevClientAPK(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}

// The real provision: the build actually sitting on this server must serve
// over a Range request (what a phone's downloader sends). Skips where the
// operator has not built the dev client — TestDevClientAPKNotBuiltIs404
// covers that half.
func TestDevClientAPKRealBuildRangeProbe(t *testing.T) {
	tmpl := filepath.Join("..", "..", "template")
	verRaw, err := os.ReadFile(filepath.Join(tmpl, ".template-version"))
	if err != nil {
		t.Skipf("no template version at %s: %v", tmpl, err)
	}
	version := strings.TrimSpace(string(verRaw))
	apk := devclient.DevClientAPKPath(tmpl, version)
	if _, err := os.Stat(apk); err != nil {
		t.Skipf("dev-client APK not built (%s) — run build-dev-client.sh first", apk)
	}
	mgr, err := devclient.NewManager(tmpl)
	if err != nil {
		t.Fatalf("NewManager(%s): %v", tmpl, err)
	}
	st, u, proj := infoStore(t)
	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), mgr)

	req := routeRequest(u, proj.ID)
	req.Header.Set("Range", "bytes=0-3")
	rec := httptest.NewRecorder()
	h.DevClientAPK(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status %d for a Range probe, want 206", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("PK\x03\x04")) {
		t.Errorf("fault: first bytes %q, want the zip/APK magic PK\\x03\\x04", rec.Body.Bytes())
	}
}

// ── L32: the metro SSE endpoint, at every layer ─────────────────────

// No session: one honest frame, headers and all, then close — never an
// infinitely-pending stream.
func TestMetroSSEReportsNoSessionHonestly(t *testing.T) {
	st, u, _ := infoStore(t)
	h := NewMetroHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil))

	rec := httptest.NewRecorder()
	h.Serve(rec, routeRequest(u, 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("fault: Content-Type %q, want text/event-stream", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"event: metro", `"kind":"unavailable"`, "no device session"} {
		if !strings.Contains(body, want) {
			t.Errorf("fault: body %q missing %q", body, want)
		}
	}
}

// A stranger must not be handed a stream at all.
func TestMetroSSERejectsNonOwner(t *testing.T) {
	st, _, proj := infoStore(t)
	stranger, err := st.CreateUser("stranger@test", "x")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	h := NewMetroHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil))

	rec := httptest.NewRecorder()
	h.Serve(rec, routeRequest(stranger, proj.ID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d for a non-owner, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got == "text/event-stream" {
		t.Errorf("fault: a stream was opened for a stranger (Content-Type %q)", got)
	}
}

// Session exists, bundler never started: the other honest frame, not a hang
// and not "connected".
func TestMetroSSESaysSoWhenTheBundlerNeverStarted(t *testing.T) {
	st, u, proj := infoStore(t)
	if _, err := st.CreateSession(proj.ID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	h := NewMetroHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil))

	rec := httptest.NewRecorder()
	h.Serve(rec, routeRequest(u, proj.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "bundler is not running") {
		t.Errorf("fault: body %q does not report the bundler state", body)
	}
	if strings.Contains(body, "bundler connected") {
		t.Errorf("fault: reported connected with no bundler: %q", body)
	}
}
