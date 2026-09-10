package api

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/relay"
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
	// MetroURL is the same bundler over http:// — the dev client's fallback
	// entry, and what a browser can probe.
	MetroURL string `json:"metro_url,omitempty"`
	// DevClientURL is the authenticated APK download for phones that do not
	// have the Seeker dev client yet.
	DevClientURL string `json:"dev_client_url,omitempty"`
}

// Info serves GET /v1/projects/{projectID}/test-info.
func (h *TestInfoHandler) Info(w http.ResponseWriter, r *http.Request) {
	project, ok := h.ownedProject(w, r, projectIDFromRoute(r))
	if !ok {
		return
	}

	session, err := h.store.LatestLiveSession(project.ID)
	if err != nil || session == nil {
		writeJSON(w, http.StatusOK, testInfo{
			Available: false,
			Reason:    "no device session running — start the device first",
		})
		return
	}

	// The relay is the phone's door; its absence means metro never started.
	rl := relay.GetSession(session.ID)
	if rl == nil {
		writeJSON(w, http.StatusOK, testInfo{
			Available: false,
			Reason:    "the phone relay is not running for this session (the bundler may have failed to start)",
		})
		return
	}

	host := publicHost()
	info := testInfo{
		Available: true,
		DeepLink:  "exp://" + host + ":" + strconv.Itoa(rl.Port()),
		MetroURL:  "http://" + host + ":" + strconv.Itoa(rl.Port()),
	}
	// The APK link is only offered when the build actually exists on this
	// server — a link that 404s on the phone is worse than none.
	if h.devClient != nil {
		if apk := h.devClient.DevClientAPKPath(); apk != "" {
			if _, err := os.Stat(apk); err == nil {
				info.DevClientURL = publicAPIBase(r) + "/v1/projects/" + strconv.FormatInt(project.ID, 10) + "/dev-client.apk"
			}
		}
	}
	writeJSON(w, http.StatusOK, info)
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
	w.Header().Set("Content-Disposition", `attachment; filename="seeker-devclient.apk"`)
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
