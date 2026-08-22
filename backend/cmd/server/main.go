package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/seekersstudio/backend/internal/agent"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/api"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/billing"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/redroid"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/workspace"
	"github.com/seekersstudio/backend/internal/ws"

	_ "modernc.org/sqlite"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./seekersstudio.db"
	}

	projectRoot := findProjectRoot()
	templateDir := resolveDir("TEMPLATE_DIR",
		filepath.Join(projectRoot, "template"),
		filepath.Join(projectRoot, "backend", "template"))
	workspaceDir := resolveDir("WORKSPACE_DIR",
		filepath.Join(projectRoot, "workspaces"),
		filepath.Join(projectRoot, "workspaces"))

	log.Printf("template dir:  %s", templateDir)
	log.Printf("workspace dir: %s", workspaceDir)

	s, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("could not open the database at %s: %v", dbPath, err)
	}
	defer s.Close()

	authSvc := auth.New()

	// A misconfigured AI provider is fatal: the whole product is the agent, and
	// starting up only to fail on the first prompt hides the real problem.
	aiClient, err := ai.New()
	if err != nil {
		log.Fatalf("AI provider is not configured: %v", err)
	}
	log.Printf("AI provider ready (model %s)", aiClient.Model())

	// The knowledge layer (always-on rules + load-on-demand skills) is embedded in
	// the binary, so Load only fails on malformed authored content — which a unit
	// test guards against. We still degrade rather than die: a build that boots and
	// serves without rules beats one that refuses to start, and the log makes the
	// gap loud.
	knowledge, err := agent.Load(filepath.Join(templateDir, "package.json"))
	if err != nil {
		log.Printf("WARNING: agent knowledge layer unavailable: %v — builds will run without rules and skills", err)
	} else {
		log.Printf("agent knowledge ready (%d skills)", len(knowledge.SkillNames()))
	}

	orchestratorSvc := orchestrator.New(aiClient, s, knowledge)

	portAlloc := redroid.NewPortAllocator(15555, 16555)
	maxContainers := envInt("MAX_CONTAINERS", 5)

	containerMgr, err := redroid.NewManager(portAlloc, maxContainers)
	if err != nil {
		log.Printf("WARNING: container manager unavailable: %v — sessions will run without a device", err)
	} else {
		if err := containerMgr.Reconcile(s); err != nil {
			log.Printf("WARNING: could not reconcile running containers: %v", err)
		}
		containerMgr.StartCleanupLoop(s, 5*time.Minute)
		log.Printf("container manager ready (max %d devices)", maxContainers)
	}

	wsMgr, err := workspace.New(templateDir, workspaceDir)
	if err != nil {
		log.Printf("WARNING: workspace manager unavailable: %v — new projects will start empty", err)
	}

	devClientMgr, err := devclient.NewManager(templateDir)
	if err != nil {
		log.Printf("WARNING: dev-client manager unavailable: %v — the dev client will not be installed", err)
	} else {
		log.Printf("dev-client manager ready (template v%s)", devClientMgr.Allowlist().TemplateVersion)
	}

	walletMgr, err := devclient.NewWalletManager(templateDir)
	if err != nil {
		log.Printf("WARNING: wallet manager unavailable: %v — the mock wallet will not be installed", err)
	} else if !walletMgr.IsInstalled() {
		log.Printf("WARNING: the mock wallet APK is missing — run build-mock-wallet.sh to enable wallet testing")
	}

	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		log.Fatalf("could not create the workspace directory %s: %v", workspaceDir, err)
	}

	authHandler := api.NewAuthHandler(s, authSvc)
	projectHandler := api.NewProjectHandler(s, wsMgr, workspaceDir)
	promptHandler := api.NewPromptHandler(s, aiClient, workspaceDir, orchestratorSvc, devClientMgr, knowledge)
	sessionHandler := api.NewSessionHandler(s, containerMgr, portAlloc, workspaceDir, devClientMgr, walletMgr)
	usageHandler := api.NewUsageHandler(s)
	terminalHandler := ws.NewTerminalHandler(authSvc, s)
	streamHandler := ws.NewStreamHandler(authSvc, s)
	inputHandler := ws.NewInputHandler(authSvc, s)

	// Payment/billing (Phase 9), test-mode only. The mock provider stands in for a
	// real processor behind the billing.Provider seam; going live is a deliberate
	// swap here — constructing a real provider — not a runtime toggle (spec 2.5,
	// 2.8). The webhook secret is a test credential kept out of the repo (spec 2.9).
	appBaseURL := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	if appBaseURL == "" {
		appBaseURL = "http://localhost:3000"
	}
	webhookSecret := strings.TrimSpace(os.Getenv("BILLING_WEBHOOK_SECRET"))
	if webhookSecret == "" {
		webhookSecret = "dev-test-webhook-secret"
		log.Printf("WARNING: BILLING_WEBHOOK_SECRET unset — using a development secret (test-mode billing only)")
	}
	billingProvider := billing.NewMock(webhookSecret, appBaseURL+"/checkout")
	billingHandler := api.NewBillingHandler(s, billingProvider, appBaseURL)
	meHandler := api.NewMeHandler(s)
	log.Printf("billing ready (provider %s, test mode)", billingProvider.Name())

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// RealIP rewrites RemoteAddr from X-Forwarded-For, which any client can set.
	// It is only correct behind a proxy that overwrites that header, so it is
	// enabled by the same switch the rate limiter uses.
	if api.TrustingProxyHeaders() {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigins()))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, map[string]string{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// readyz reports whether the server can actually serve work, which is what a
	// load balancer needs to know. healthz only says the process is alive.
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{"status": "ready", "database": "ok", "ai": "ok"}
		code := http.StatusOK
		if err := s.Ping(r.Context()); err != nil {
			body["status"], body["database"] = "degraded", err.Error()
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		writeStatus(w, body)
	})

	authLimiter := api.AuthRateLimiter()
	promptLimiter := api.PromptRateLimiter()

	r.Group(func(r chi.Router) {
		r.Use(authLimiter.Middleware)
		r.Post("/v1/auth/register", authHandler.Register)
		r.Post("/v1/auth/login", authHandler.Login)
	})

	r.Group(func(r chi.Router) {
		r.Use(authMiddleware(authSvc))

		r.Get("/v1/projects", projectHandler.List)
		r.Post("/v1/projects", projectHandler.Create)
		r.Get("/v1/projects/{id}", projectHandler.Get)
		r.Put("/v1/projects/{id}", projectHandler.UpdatePlan)
		r.Delete("/v1/projects/{id}", projectHandler.Delete)
		r.Get("/v1/projects/{id}/files", projectHandler.GetFiles)
		r.Put("/v1/projects/{id}/files", projectHandler.WriteFiles)
		r.Delete("/v1/projects/{id}/files", projectHandler.DeleteFile)

		// Prompts. Rate limited separately: each one costs real money.
		r.Group(func(r chi.Router) {
			r.Use(promptLimiter.Middleware)
			r.Post("/v1/prompts", promptHandler.Create)
			r.Post("/v1/prompts/stream", promptHandler.Stream)
			// Resume runs the agent loop again, so it belongs with the metered
			// routes even though a resumed turn is not charged a second prompt.
			r.Post("/v1/prompts/{id}/resume", promptHandler.Resume)
		})
		r.Get("/v1/prompts/{id}", promptHandler.Get)
		// Discard only flips a status marker — no loop, no metering.
		r.Post("/v1/prompts/{id}/discard", promptHandler.Discard)

		// Project knowledge and history.
		r.Get("/v1/projects/{projectID}/memory", promptHandler.GetMemory)
		r.Get("/v1/projects/{projectID}/conversation", promptHandler.GetConversation)
		r.Delete("/v1/projects/{projectID}/conversation", promptHandler.ClearConversation)

		// Checkpoints — undo for anything the agent changed.
		r.Get("/v1/projects/{projectID}/checkpoints", promptHandler.ListCheckpoints)
		r.Post("/v1/projects/{projectID}/checkpoints/{checkpointID}/restore", promptHandler.RestoreCheckpoint)

		r.Post("/v1/sessions", sessionHandler.Create)
		r.Get("/v1/sessions/{id}", sessionHandler.Get)
		r.Post("/v1/sessions/{id}/pause", sessionHandler.Pause)
		r.Post("/v1/sessions/{id}/resume", sessionHandler.Resume)
		r.Delete("/v1/sessions/{id}", sessionHandler.Destroy)

		r.Get("/v1/usage", usageHandler.Get)

		// Profile + billing (Phase 9). Reading and changing the profile, and
		// starting/cancelling a subscription, all require the caller's identity, so
		// they sit inside the auth group. The webhook does not (see below).
		r.Get("/v1/me", meHandler.Get)
		r.Put("/v1/me", meHandler.Update)
		r.Post("/v1/billing/upgrade", billingHandler.StartUpgrade)
		r.Post("/v1/billing/cancel", billingHandler.Cancel)
		// Test-mode only: simulates finishing checkout. Under a real provider this
		// route 404s (the handler feature-detects the simulator), so it cannot be
		// used to grant a tier in production.
		r.Post("/v1/billing/test/complete", billingHandler.TestComplete)
	})

	// Billing webhook: unauthenticated by necessity — the payment provider has no
	// JWT — and trusted solely by its signature, which the handler verifies before
	// reading the body. It sits outside the auth group for the same reason the
	// WebSocket routes do: it cannot use header-based auth.
	r.Post("/v1/billing/webhook", billingHandler.Webhook)

	// WebSocket endpoints authenticate from the query string, because a browser
	// cannot set headers on a WebSocket handshake. Each handler validates the
	// token and confirms the caller owns the session before attaching to a
	// device, so these sit outside the header-based auth group by necessity, not
	// by omission.
	r.Get("/v1/sessions/{id}/terminal", terminalHandler.Handle)
	r.Get("/v1/sessions/{id}/stream", streamHandler.Handle)
	r.Get("/v1/sessions/{id}/input", inputHandler.Handle)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
		// A slow client must not be able to hold a connection open forever with a
		// partial request. WriteTimeout is deliberately unset: SSE builds and
		// WebSocket terminals are long-lived by design and any write deadline
		// would sever them mid-stream.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("seekersstudio backend listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server stopped: %v", err)
		}
	}()

	<-shutdown
	log.Println("shutting down — letting in-flight builds finish (up to 60s)")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("some connections were closed before they finished: %v", err)
	}
	log.Println("stopped")
}

func authMiddleware(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				writeAuthError(w, "you need to sign in")
				return
			}

			token := strings.TrimPrefix(header, "Bearer ")
			if token == header {
				writeAuthError(w, "the Authorization header must be 'Bearer <token>'")
				return
			}

			claims, err := svc.ValidateToken(token)
			if err != nil {
				writeAuthError(w, "your session has expired — sign in again")
				return
			}

			ctx := context.WithValue(r.Context(), api.UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeStatus(w http.ResponseWriter, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// allowedOrigins is the CORS allowlist. Development hosts are built in;
// CORS_ALLOWED_ORIGINS adds a comma-separated list for anywhere else.
func allowedOrigins() map[string]bool {
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
}

func corsMiddleware(allowed map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// resolveDir picks a directory from an env var, falling back to the first
// candidate that exists and then to the last one.
func resolveDir(envKey string, candidates ...string) string {
	if dir := strings.TrimSpace(os.Getenv(envKey)); dir != "" {
		return dir
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return "."
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

// findProjectRoot walks up from the executable, then the working directory,
// looking for go.mod.
func findProjectRoot() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for _, rel := range []string{".", "..", filepath.Join("..", "..")} {
		candidate := filepath.Join(cwd, rel)
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
	}
	return cwd
}
