package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/api"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/redroid"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/workspace"
	"github.com/seekersstudio/backend/internal/ws"

	_ "modernc.org/sqlite"
)

// contextKey is used for request context values.

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./seekersstudio.db"
	}

	// Resolve paths relative to the project root (where go.mod lives)
	projectRoot := findProjectRoot()

	templateDir := os.Getenv("TEMPLATE_DIR")
	if templateDir == "" {
		candidate := filepath.Join(projectRoot, "template")
		if _, err := os.Stat(candidate); err == nil {
			templateDir = candidate
		} else {
			templateDir = filepath.Join(projectRoot, "backend", "template")
		}
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		candidate := filepath.Join(projectRoot, "workspaces")
		if _, err := os.Stat(candidate); err == nil {
			workspaceDir = candidate
		} else {
			workspaceDir = filepath.Join(projectRoot, "workspaces")
		}
	}

	log.Printf("template dir: %s", templateDir)
	log.Printf("workspace dir: %s", workspaceDir)

	// Initialize store
	log.Println("initializing store...")
	s, err := store.New(dbPath)
	if err != nil {
		log.Fatal("failed to initialize store:", err)
	}
	defer s.Close()
	log.Println("store initialized")

	authSvc := auth.New()
	aiClient := ai.New()
	orchestratorSvc := orchestrator.New(aiClient)

	// Initialize port allocator and container manager
	portAlloc := redroid.NewPortAllocator(15555, 16555)
	maxContainers := 5

	containerMgr, err := redroid.NewManager(portAlloc, maxContainers)
	if err != nil {
		log.Printf("WARNING: container manager init failed: %v (sessions will not have devices)", err)
	} else {
		log.Println("container manager initialized")

		// Reconcile on startup — recover tracking of running containers, remove orphans
		if err := containerMgr.Reconcile(s); err != nil {
			log.Printf("WARNING: reconciliation failed: %v", err)
		}

		// Start periodic orphan cleanup (every 5 minutes)
		containerMgr.StartCleanupLoop(s, 5*time.Minute)
	}

	// Initialize workspace manager
	wsMgr, err := workspace.New(templateDir, workspaceDir)
	if err != nil {
		log.Printf("WARNING: workspace manager init failed: %v (projects will use fallback)", err)
	}

	// Initialize dev-client manager
	devClientMgr, err := devclient.NewManager(templateDir)
	if err != nil {
		log.Printf("WARNING: dev-client manager init failed: %v (dev-client install will be skipped)", err)
	} else {
		log.Printf("dev-client manager initialized (template v%s)", devClientMgr.Allowlist().TemplateVersion)
	}

	// Ensure workspace dir exists
	os.MkdirAll(workspaceDir, 0755)

	// Initialize handlers
	authHandler := api.NewAuthHandler(s, authSvc)
	projectHandler := api.NewProjectHandler(s, wsMgr, workspaceDir)
	promptHandler := api.NewPromptHandler(s, aiClient, workspaceDir, orchestratorSvc, devClientMgr)
	sessionHandler := api.NewSessionHandler(s, containerMgr, portAlloc, workspaceDir, devClientMgr)
	usageHandler := api.NewUsageHandler(s)
	terminalHandler := ws.NewTerminalHandler(authSvc, s)
	streamHandler := ws.NewStreamHandler(authSvc, s)
	inputHandler := ws.NewInputHandler(authSvc, s)

	// Router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(300 * time.Second))
	r.Use(corsMiddleware)

	// Health
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Rate limiters
	authLimiter := api.AuthRateLimiter()
	promptLimiter := api.PromptRateLimiter()

	// Auth routes (rate limited)
	r.Group(func(r chi.Router) {
		r.Use(authLimiter.Middleware)
		r.Post("/v1/auth/register", authHandler.Register)
		r.Post("/v1/auth/login", authHandler.Login)
	})

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware(authSvc))

		// Projects
		r.Get("/v1/projects", projectHandler.List)
		r.Post("/v1/projects", projectHandler.Create)
		r.Get("/v1/projects/{id}", projectHandler.Get)
		r.Get("/v1/projects/{id}/files", projectHandler.GetFiles)
		r.Put("/v1/projects/{id}/files", projectHandler.WriteFiles)
		r.Delete("/v1/projects/{id}/files", projectHandler.DeleteFile)
		r.Put("/v1/projects/{id}", projectHandler.UpdatePlan)
		r.Delete("/v1/projects/{id}", projectHandler.Delete)

		// Prompts (rate limited — expensive AI calls)
		r.Group(func(r chi.Router) {
			r.Use(promptLimiter.Middleware)
			r.Post("/v1/prompts", promptHandler.Create)
			r.Post("/v1/prompts/stream", promptHandler.Stream)
		})
		r.Get("/v1/prompts/{id}", promptHandler.Get)

		// Project Memory
		r.Get("/v1/projects/{projectID}/memory", promptHandler.GetMemory)

		// Sessions
		r.Post("/v1/sessions", sessionHandler.Create)
		r.Get("/v1/sessions/{id}", sessionHandler.Get)
		r.Post("/v1/sessions/{id}/pause", sessionHandler.Pause)
		r.Post("/v1/sessions/{id}/resume", sessionHandler.Resume)
		r.Delete("/v1/sessions/{id}", sessionHandler.Destroy)

		// Usage
		r.Get("/v1/usage", usageHandler.Get)
	})

	// WebSocket endpoints (auth via query param — browsers can't set WS headers)
	r.Get("/v1/sessions/{id}/terminal", terminalHandler.Handle)
	r.Get("/v1/sessions/{id}/stream", streamHandler.Handle)
	r.Get("/v1/sessions/{id}/input", inputHandler.Handle)

	log.Printf("seekersstudio backend listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Printf("server error: %v", err)
		log.Fatal(err)
	}
}

func authMiddleware(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(header, "Bearer ")
			if token == header {
				http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
				return
			}

			claims, err := svc.ValidateToken(token)
			if err != nil {
				http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), api.UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	allowedOrigins := map[string]bool{
		"http://localhost:3000":  true,
		"http://localhost:3001":  true,
		"http://127.0.0.1:3000": true,
	}
	if origin := os.Getenv("CORS_ALLOWED_ORIGIN"); origin != "" {
		allowedOrigins[origin] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// findProjectRoot walks up from the executable's directory looking for go.mod
func findProjectRoot() string {
	exe, err := os.Executable()
	if err == nil {
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
