package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/logx"
)

// ErrorsHandler serves captured errors and ingests client-side reports.
// Everything here is correlation metadata (request id, path, short message) —
// payloads, headers, and tokens never enter a captured report, from either
// side.
type ErrorsHandler struct{}

func NewErrorsHandler() *ErrorsHandler {
	return &ErrorsHandler{}
}

// List returns recent captured errors, newest first. Auth-gated like the
// rest of /v1: operational visibility, not a public feed.
func (h *ErrorsHandler) List(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"errors": logx.Recent()})
}

type clientErrorReport struct {
	Message string `json:"message"`
	Path    string `json:"path"`
	Stack   string `json:"stack"`
}

// Report ingests one frontend error: the global boundary and the IDE's fatal
// paths sendBeacon here. Lengths are capped — a client must not be able to
// stuff the ring with megabytes.
func (h *ErrorsHandler) Report(w http.ResponseWriter, r *http.Request) {
	var body clientErrorReport
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "could not read the report body")
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}
	if len(body.Message) > 500 {
		body.Message = body.Message[:500]
	}
	path := strings.TrimSpace(body.Path)
	if len(path) > 200 {
		path = path[:200]
	}
	stack := strings.TrimSpace(body.Stack)
	if len(stack) > 2000 {
		stack = stack[:2000]
	}
	f := logx.Fields{Path: path, UserID: GetUserID(r.Context())}
	msg := "client: " + body.Message
	if stack != "" {
		msg += " | " + stack
	}
	logx.Capture(f, msg)
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}
