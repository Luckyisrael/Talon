package ws

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/store"
)

// upgrader is shared by every WebSocket endpoint.
//
// CheckOrigin used to return true unconditionally. A WebSocket handshake is not
// subject to CORS, so that let any page on the internet open a socket to this
// server using the visitor's own credentials — and these sockets carry an adb
// shell on the user's device. The origin is now checked against the same
// allowlist the HTTP API uses.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	CheckOrigin:     originAllowed,
}

// allowedOrigins mirrors the HTTP CORS allowlist.
var allowedOrigins = func() map[string]bool {
	origins := map[string]bool{
		"http://localhost:3000": true,
		"http://localhost:3001": true,
		"http://127.0.0.1:3000": true,
		"http://127.0.0.1:3001": true,
	}
	for _, key := range []string{"CORS_ALLOWED_ORIGIN", "CORS_ALLOWED_ORIGINS"} {
		for _, origin := range strings.Split(os.Getenv(key), ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				origins[origin] = true
			}
		}
	}
	return origins
}()

func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin means the request did not come from a browser page, so there
		// is no cross-site request to forge. Command-line clients land here.
		return true
	}
	if allowedOrigins[origin] {
		return true
	}
	// Same-origin is always fine, whatever host the server is reached on.
	if u, err := url.Parse(origin); err == nil && u.Host == r.Host {
		return true
	}
	log.Printf("websocket: refused an upgrade from origin %q", origin)
	return false
}

// deviceSession is an authorised attachment to one project's device.
type deviceSession struct {
	ID        int64
	ProjectID int64
	ADBTarget string
}

// authorize is the gate every device socket goes through: it validates the token
// from the query string, resolves the session, and confirms the caller owns the
// project that session belongs to.
//
// That last step is the one that was missing. Any signed-in user could pass any
// session id and get a shell, a screen stream, or touch control on somebody
// else's device.
func authorize(w http.ResponseWriter, r *http.Request, authSvc *auth.Service, s *store.Store) (*deviceSession, bool) {
	// Browsers cannot set headers on a WebSocket handshake, so the token comes
	// through the query string.
	token := r.URL.Query().Get("token")
	if token == "" {
		httpError(w, http.StatusUnauthorized, "you need to sign in")
		return nil, false
	}

	claims, err := authSvc.ValidateToken(token)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "your session has expired — sign in again")
		return nil, false
	}

	sessionID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || sessionID <= 0 {
		httpError(w, http.StatusBadRequest, "invalid session id")
		return nil, false
	}

	session, err := s.GetSession(sessionID)
	if err != nil {
		httpError(w, http.StatusNotFound, "session not found")
		return nil, false
	}

	project, err := s.GetProject(session.ProjectID)
	if err != nil || project.UserID != claims.UserID {
		// Same response as a missing session: do not confirm that the id exists.
		log.Printf("websocket: user %d was refused session %d", claims.UserID, sessionID)
		httpError(w, http.StatusNotFound, "session not found")
		return nil, false
	}

	if session.ADBPort == 0 {
		httpError(w, http.StatusConflict, "this session has no device attached yet")
		return nil, false
	}

	return &deviceSession{
		ID:        session.ID,
		ProjectID: session.ProjectID,
		ADBTarget: "localhost:" + strconv.Itoa(session.ADBPort),
	}, true
}

func httpError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The message is server-authored, so a plain quoted string is safe here.
	w.Write([]byte(`{"error":"` + message + `"}`))
}
