package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// ── L34 / FM-DEVf: one phone hands itself the project ───────────────

// livePhone brings up a store with a live session AND a running relay — the
// only state in which any deep link exists at all. Port 17997 keeps it clear
// of TestTestInfoHonesty's 17999.
func livePhone(t *testing.T) (*store.Store, *store.User, *store.Project) {
	t.Helper()

	st, err := store.New(t.TempDir() + "/fmdevf.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	u, err := st.CreateUser("fmdevf@test", "x")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	proj, err := st.CreateProject(u.ID, "p", "v", "fmdevf-slug", "a.b", "c.d", "e")
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	sess, err := st.CreateSession(proj.ID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := relay.StartForSession(sess.ID, 17997, "127.0.0.1:8081"); err != nil {
		t.Fatalf("relay: %v", err)
	}
	t.Cleanup(func() { relay.StopSession(sess.ID) })
	return st, u, proj
}

// fetchTestInfoWire answers GET test-info and hands back the RAW JSON — the
// wire, not the struct. FM-DEVf's reds have to be behavioral: reaching for a
// struct field that does not exist yet is a compile error, and a compile
// error says nothing about the link a phone actually receives.
func fetchTestInfoWire(t *testing.T, st *store.Store, u *store.User, projectID int64) map[string]any {
	t.Helper()
	t.Setenv("PUBLIC_HOST", "localhost")

	h := NewTestInfoHandler(NewPromptHandler(st, nil, t.TempDir(), "", nil, nil, nil, nil), nil)
	req := authedRequest(u.ID, "")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", strconv.FormatInt(projectID, 10))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Info(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var wire map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return wire
}

// FM-DEVf(ii) — written before the fix (TEST_RULES §3). expo-dev-launcher
// reads only `<scheme>://expo-development-client/?url=…`: the host test is
// DevLauncherURLHelper.kt:7, the url parameter :9-11, and
// DevLauncherController.kt:245 bails to handleExternalIntent before loadApp
// ever runs otherwise. The scheme must be the one the template builds — read
// from backend/template/app.json, not a second literal — and the wrapped link
// must come back whole, its `?token=` still data rather than a second query.
//
// Red before L34 (observed): the wire carries no `dev_launcher_link` key at
// all, so a single phone's only ACTION_VIEW is a bare exp:// no intent-filter
// in the shipped APK declares.
func TestDevLauncherLinkWrapsTheDeepLinkForTheLauncher(t *testing.T) {
	st, usr, proj := livePhone(t)
	wire := fetchTestInfoWire(t, st, usr, proj.ID)

	raw, _ := wire["dev_launcher_link"].(string)
	if raw == "" {
		t.Fatalf("FM-DEVf(ii): no dev_launcher_link on the wire (deep_link=%v) — the single phone has no ACTION_VIEW the installed client can resolve", wire["deep_link"])
	}
	deep, _ := wire["deep_link"].(string)
	if deep == "" {
		t.Fatal("fault: the wire carries no deep_link either")
	}

	launcher, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse launcher link %q: %v", raw, err)
	}
	if launcher.Host != "expo-development-client" {
		t.Errorf("FM-DEVf(ii): launcher link host %q, want \"expo-development-client\" — DevLauncherURLHelper.kt:7 rejects any other host before loadApp runs", launcher.Host)
	}
	if got := launcher.Query().Get("url"); got != deep {
		t.Errorf("FM-DEVf(ii): launcher link wraps %q, want the deep link whole (%q) — the relay token rides inside it", got, deep)
	}

	// The scheme is the template's own, read from the file: one authority for
	// the dev client's identity, the way DevClientPackage is pinned.
	rawApp, err := os.ReadFile(filepath.Join("..", "..", "template", "app.json"))
	if err != nil {
		t.Fatalf("read template app.json: %v", err)
	}
	var cfg struct {
		Expo struct {
			Scheme string `json:"scheme"`
		} `json:"expo"`
	}
	if err := json.Unmarshal(rawApp, &cfg); err != nil {
		t.Fatalf("parse template app.json: %v", err)
	}
	if cfg.Expo.Scheme == "" {
		t.Fatal("template app.json declares no expo.scheme")
	}
	if launcher.Scheme != cfg.Expo.Scheme {
		t.Errorf("FM-DEVf(ii): launcher link scheme %q, want the template's %q (backend/template/app.json expo.scheme) — a second literal is how these two drift apart", launcher.Scheme, cfg.Expo.Scheme)
	}
}

// FM-DEVf(i) — written before the fix (TEST_RULES §3). On one phone there is
// nothing to scan, so pairing collapses to a single ACTION_VIEW the installed
// client must be able to resolve. The authority for "can it resolve" is the
// artifact we actually ship, not a source file — the same lesson L33's
// package test taught: source and artifact had already drifted once. The
// shipped dev client declares `talonapp`, `exp+talon-app` and
// `expo-dev-launcher`; `exp`, which deep_link carries, is not among them.
//
// Red before L34: compile failure — `devclient.ManifestDeclares undefined`,
// the shape L17/L23/L24 and L33's `DevClientPackage` shipped theirs as (the
// artifact reader does not exist yet).
func TestInfoHandsThePhoneALinkTheShippedClientCanResolve(t *testing.T) {
	st, usr, proj := livePhone(t)
	wire := fetchTestInfoWire(t, st, usr, proj.ID)

	// Every *_link value on the wire is a candidate handoff for the phone.
	links := map[string]string{}
	for key, val := range wire {
		if !strings.HasSuffix(key, "_link") {
			continue
		}
		s, _ := val.(string)
		if s != "" {
			links[key] = s
		}
	}
	if len(links) == 0 {
		t.Fatal("fault: the wire advertises no link at all")
	}

	tmpl := filepath.Join("..", "..", "template")
	rawVersion, err := os.ReadFile(filepath.Join(tmpl, ".template-version"))
	if err != nil {
		t.Skipf("no template version at %s: %v", tmpl, err)
	}
	apk := devclient.DevClientAPKPath(tmpl, strings.TrimSpace(string(rawVersion)))

	for key, raw := range links {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Errorf("parse %s %q: %v", key, raw, err)
			continue
		}
		declared, err := devclient.ManifestDeclares(apk, parsed.Scheme)
		if err != nil {
			t.Skipf("dev-client APK unreadable (%s): %v", apk, err)
		}
		if declared {
			return
		}
		t.Logf("%s = %s → the shipped APK declares no %q scheme", key, raw, parsed.Scheme)
	}

	t.Fatalf("FM-DEVf(i): every link the wire advertises is unresolvable by the shipped dev client — %v; its manifest declares talonapp / exp+talon-app / expo-dev-launcher, never exp, so Linking.openURL rejects and the tap does nothing", links)
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
