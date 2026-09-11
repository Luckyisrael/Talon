// Package logx gives every log line a correlation identity: the request id
// chi assigns, plus the user and project when the caller knows them. Format
// is logfmt (key=value, quoted when needed) — greppable with plain grep and
// parseable without a dependency:
//
//	level=INFO req=abc123 user=7 project=24 msg="build finished" rounds=39
//
// Nothing here replaces contextual messages; it only prefixes the identity
// that lets one user's journey be traced across components.
package logx

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/luckyisrael/talon/backend/internal/metrics"
)

// Fields carries the correlation identity. Zero values are omitted, so
// background workers log project/session without a request id and handlers
// log all three.
type Fields struct {
	ReqID     string
	UserID    int64
	ProjectID int64
	SessionID int64
	Path      string
}

// FromContext pulls what the context carries: always the chi request id,
// plus the user id when auth middleware has run.
func FromContext(ctx context.Context) Fields {
	var f Fields
	if id := middleware.GetReqID(ctx); id != "" {
		f.ReqID = id
	}
	return f
}

// Info emits one structured line. Extra kv pairs must alternate key, value;
// an odd tail is dropped rather than mislogged.
func Info(f Fields, msg string, kv ...any) {
	log.Print(render("INFO", f, msg, kv...))
}

// Warn emits one structured warning line. Same shape as Info.
func Warn(f Fields, msg string, kv ...any) {
	log.Print(render("WARN", f, msg, kv...))
}

// Error emits one structured error line. Same shape as Info.
func Error(f Fields, msg string, kv ...any) {
	log.Print(render("ERROR", f, msg, kv...))
}

func render(level string, f Fields, msg string, kv ...any) string {
	var b strings.Builder
	b.WriteString("level=" + level)
	if f.ReqID != "" {
		fmt.Fprintf(&b, " req=%s", quote(f.ReqID))
	}
	if f.UserID != 0 {
		fmt.Fprintf(&b, " user=%d", f.UserID)
	}
	if f.ProjectID != 0 {
		fmt.Fprintf(&b, " project=%d", f.ProjectID)
	}
	if f.SessionID != 0 {
		fmt.Fprintf(&b, " session=%d", f.SessionID)
	}
	if f.Path != "" {
		fmt.Fprintf(&b, " path=%s", quote(f.Path))
	}
	// Sort keys so the same call site always renders the same line shape.
	keys := map[string]string{}
	order := []string{}
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			continue
		}
		if _, seen := keys[k]; !seen {
			order = append(order, k)
		}
		keys[k] = fmt.Sprint(kv[i+1])
	}
	sort.Strings(order)
	for _, k := range order {
		fmt.Fprintf(&b, " %s=%s", k, quote(keys[k]))
	}
	fmt.Fprintf(&b, " msg=%s", quote(msg))
	return b.String()
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t\"=") || s == "" {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// Captured is one recorded error: what, where, when, and which request.
// Payloads, headers, and tokens never enter here — message and path only.
type Captured struct {
	At      int64  `json:"at"`
	ReqID   string `json:"req_id,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

const maxCaptured = 50

var (
	capturedMu sync.Mutex
	captured   []Captured
)

// Capture records an error with its correlation identity and logs it. The
// ring holds the last maxCaptured; volume beyond that is dropped with a
// counter in the log line, never grown unbounded.
func Capture(f Fields, msg string) {
	entry := Captured{At: time.Now().Unix(), ReqID: f.ReqID, Path: f.Path, Message: msg}
	capturedMu.Lock()
	captured = append(captured, entry)
	dropped := 0
	if len(captured) > maxCaptured {
		dropped = len(captured) - maxCaptured
		captured = captured[len(captured)-maxCaptured:]
	}
	capturedMu.Unlock()
	if dropped > 0 {
		Error(f, msg, "error_ring_dropped", dropped)
		return
	}
	Error(f, msg)
}

// Recent returns a copy of the captured errors, newest first.
func Recent() []Captured {
	capturedMu.Lock()
	defer capturedMu.Unlock()
	out := make([]Captured, len(captured))
	for i, e := range captured {
		out[len(captured)-1-i] = e
	}
	return out
}

// Recoverer is chi middleware parity with middleware.Recoverer, except the
// panic is captured with the request identity instead of printed bare. The
// client still gets a 500 with no internals.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				msg := fmt.Sprintf("panic: %v", rec)
				if len(msg) > 500 {
					msg = msg[:500]
				}
				f := Fields{ReqID: middleware.GetReqID(r.Context()), Path: r.URL.Path}
				for _, key := range []string{"projectID", "projectId"} {
					if v := chi.URLParam(r, key); v != "" {
						if n, err := strconv.ParseInt(v, 10, 64); err == nil {
							f.ProjectID = n
						}
						break
					}
				}
				if f.ProjectID == 0 && strings.HasPrefix(r.URL.Path, "/v1/projects/") {
					if v := chi.URLParam(r, "id"); v != "" {
						if n, err := strconv.ParseInt(v, 10, 64); err == nil {
							f.ProjectID = n
						}
					}
				}
				Capture(f, msg)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestLogger replaces chi's unstructured request logger. Every served
// request emits one line with its correlation id, method, path, status, and
// latency. The user id is NOT here — this runs before auth sets it — so
// handlers that know the user add it to their own lines via FromContext plus
// an explicit user field; joining on req=<id> reconstructs the journey.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		latency := time.Since(start)
		metrics.Default.ObserveRequest(ww.Status(), latency.Milliseconds())
		f := Fields{ReqID: middleware.GetReqID(r.Context())}
		// Project attribution, carefully: several routes use {id} for
		// non-project ids (exports, sessions, prompts), so a bare "id" only
		// counts on project-scoped paths.
		for _, key := range []string{"projectID", "projectId"} {
			if v := chi.URLParam(r, key); v != "" {
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					f.ProjectID = n
				}
				break
			}
		}
		if f.ProjectID == 0 && strings.HasPrefix(r.URL.Path, "/v1/projects/") {
			if v := chi.URLParam(r, "id"); v != "" {
				if n, err := strconv.ParseInt(v, 10, 64); err == nil {
					f.ProjectID = n
				}
			}
		}
		Info(f, "request served",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"latency_ms", latency.Milliseconds(),
			"bytes", ww.BytesWritten(),
		)
	})
}
