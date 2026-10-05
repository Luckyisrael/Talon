package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/devclient"
	"github.com/luckyisrael/talon/backend/internal/relay"
)

// TestInfoHandler serves the DV2 "test on your phone" surface: the deep
// link (exp:// host:port) a phone's installed dev client opens, the
// dev-client APK download for first-time phones, and enough session state
// for the frontend to render the panel honestly.
type TestInfoHandler struct {
	*PromptHandler
	devClient *devclient.Manager
}

func NewTestInfoHandler(base *PromptHandler, dc *devclient.Manager) *TestInfoHandler {
	return &TestInfoHandler{PromptHandler: base, devClient: dc}
}

// testInfo is the wire shape of GET /v1/projects/{projectID}/test-info.
type testInfo struct {
	// Available reports whether the phone path is live: a device session
	// with a bundler and a relay. False is honest — the frontend shows
	// "start the device first" instead of a dead QR.
	Available bool `json:"available"`
	// Reason explains unavailability in one line.
	Reason string `json:"reason,omitempty"`
	// DeepLink is the exp:// URL the installed dev client opens directly.
	DeepLink string `json:"deep_link,omitempty"`
	// DevLauncherLink wraps DeepLink in the shape expo-dev-launcher reads as
	// an instruction to load — `<scheme>://expo-development-client/?url=…`.
	// The raw exp:// form above is right for the QR and the dev client's
	// own scanner, which parse the string themselves, and wrong for an
	// ACTION_VIEW: no intent-filter in the shipped APK declares `exp`, so
	// Linking.openURL rejects it, the `void` discards the rejection, and
	// the tap does nothing — with nothing on the phone that could report
	// why. On one phone there is no second screen to scan, which makes that
	// dead button the entire pairing path (FM-DEVf(i)/(ii)).
	DevLauncherLink string `json:"dev_launcher_link,omitempty"`
	// MetroURL is the same bundler over http:// — the dev client's fallback
	// entry, and what a browser can probe.
	MetroURL string `json:"metro_url,omitempty"`
	// DevClientURL is the authenticated APK download for phones that do not
	// have the Talon dev client yet.
	DevClientURL string `json:"dev_client_url,omitempty"`
}

// Info serves GET /v1/projects/{projectID}/test-info.
func (h *TestInfoHandler) Info(w http.ResponseWriter, r *http.Request) {
	project, ok := h.ownedProject(w, r, projectIDFromRoute(r))
	if !ok {
		return
	}

	// FM-DEVa: the APK door does not depend on a session. A first-time
	// phone downloads the client before there is anything to pair with,
	// so the URL is derived once, up front, and attached to EVERY answer.
	// Its presence means exactly "built on disk" — that is the contract
	// both clients key their fallback copy on.
	devURL := h.devClientURL(r, project.ID)

	session, err := h.store.LatestLiveSession(project.ID)
	if err != nil || session == nil {
		writeJSON(w, http.StatusOK, testInfo{
			Available:    false,
			Reason:       "no device session running — start the device first",
			DevClientURL: devURL,
		})
		return
	}

	// The relay is the phone's door; its absence means metro never started.
	rl := relay.GetSession(session.ID)
	if rl == nil {
		writeJSON(w, http.StatusOK, testInfo{
			Available:    false,
			Reason:       "the phone relay is not running for this session (the bundler may have failed to start)",
			DevClientURL: devURL,
		})
		return
	}

	host := publicHost()
	// §2.4: the QR embeds authentication, not just a location. The relay
	// mints a session-scoped token at start; it rides the deep link as a
	// query param (the Expo dev client preserves it onto its bundle/HMR
	// requests, where the relay checks it) and dies with the relay.
	deepLink := "exp://" + host + ":" + strconv.Itoa(rl.Port())
	if tok := rl.Token(); tok != "" {
		deepLink += "?token=" + tok
	}
	info := testInfo{
		Available:       true,
		DeepLink:        deepLink,
		DevLauncherLink: devLauncherLink(deepLink),
		MetroURL:        "http://" + host + ":" + strconv.Itoa(rl.Port()),
		DevClientURL:    devURL,
	}
	writeJSON(w, http.StatusOK, info)
}

// devLauncherLink wraps a raw exp:// deep link in the shape
// expo-dev-launcher accepts as an ACTION_VIEW. The shape lives with the
// scheme it carries — devclient.LaunchLink — so the pairing link and
// run_boot's dev-client boot cannot drift apart; the reasoning (the Kotlin
// line numbers, why bare exp:// opens nothing) is there too.
func devLauncherLink(deepLink string) string {
	return devclient.LaunchLink(deepLink)
}

// devClientURL is the authenticated APK download, advertised whenever the
// build exists on disk — devClient configured AND the file present. Session
// state never enters the answer (FM-DEVa): an absent URL means exactly
// "not built here", which is the only honest reading for the clients.
func (h *TestInfoHandler) devClientURL(r *http.Request, projectID int64) string {
	if h.devClient == nil {
		return ""
	}
	apk := h.devClient.DevClientAPKPath()
	if apk == "" {
		return ""
	}
	if _, err := os.Stat(apk); err != nil {
		return ""
	}
	return publicAPIBase(r) + "/v1/projects/" + strconv.FormatInt(projectID, 10) + "/dev-client.apk"
}

// DevClientAPK serves the prebuilt dev-client APK (the free tier's test
// build). Auth: the project's owner only — the route is project-scoped,
// which also keeps random URL guessing from pulling a 271MB file.
func (h *TestInfoHandler) DevClientAPK(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.ownedProject(w, r, projectIDFromRoute(r)); !ok {
		return
	}
	if h.devClient == nil {
		writeError(w, http.StatusServiceUnavailable, "the dev client is not configured on this server")
		return
	}
	apk := h.devClient.DevClientAPKPath()
	if apk == "" {
		writeError(w, http.StatusNotFound, "no dev-client build on this server")
		return
	}
	if _, err := os.Stat(apk); err != nil {
		writeError(w, http.StatusNotFound, "the dev-client APK is not built — run build-dev-client.sh first")
		return
	}

	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="talon-devclient.apk"`)
	http.ServeFile(w, r, apk)
}

// publicHost is the externally reachable host for phone deep links. The
// server cannot guess this from a browser request (the phone is a
// different network) — the operator sets PUBLIC_HOST; localhost is the
// honest single-machine default.
func publicHost() string {
	if h := strings.TrimSpace(os.Getenv("PUBLIC_HOST")); h != "" {
		return h
	}
	return "localhost"
}

// publicAPIBase builds the API origin the browser reached us through —
// the APK download rides the same origin the frontend already trusts.
func publicAPIBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		scheme = fwd
	}
	return scheme + "://" + requestHost(r)
}

func requestHost(r *http.Request) string {
	if xh := r.Header.Get("X-Forwarded-Host"); xh != "" {
		return xh
	}
	if r.Host != "" {
		return r.Host
	}
	return "localhost"
}
