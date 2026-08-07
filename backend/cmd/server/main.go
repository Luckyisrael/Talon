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
	"github.com/seekersstudio/backend/internal/adb"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/api"
	"github.com/seekersstudio/backend/internal/auth"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
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
		templateDir = filepath.Join(projectRoot, "backend", "template")
	}

	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir = filepath.Join(projectRoot, "workspaces")
	}

	log.Printf("template dir: %s", templateDir)
	log.Printf("workspace dir: %s", workspaceDir)

	// Initialize services
	log.Println("initializing store...")
	s, err := store.New(dbPath)
	if err != nil {
		log.Fatal("failed to initialize store:", err)
	}
	defer s.Close()
	log.Println("store initialized")

	authSvc := auth.New()
	aiClient := ai.New()
	adbClient := adb.New()
	orchestratorSvc := orchestrator.New(aiClient)

	// Ensure workspace dir exists
	os.MkdirAll(workspaceDir, 0755)

	// Initialize handlers
	authHandler := api.NewAuthHandler(s, authSvc)
	projectHandler := api.NewProjectHandler(s, templateDir, workspaceDir)
	promptHandler := api.NewPromptHandler(s, aiClient, workspaceDir, orchestratorSvc)
	sessionHandler := api.NewSessionHandler(s, adbClient, workspaceDir)
	usageHandler := api.NewUsageHandler(s)
	terminalHandler := ws.NewTerminalHandler()
	streamHandler := ws.NewStreamHandler()

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

	// Auth routes (no auth required)
	r.Post("/v1/auth/register", authHandler.Register)
	r.Post("/v1/auth/login", authHandler.Login)

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

		// Prompts
		r.Post("/v1/prompts", promptHandler.Create)
		r.Post("/v1/prompts/stream", promptHandler.Stream)
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

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
	if err != nil {
		return "."
	}
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
	// Fallback: assume we're running from backend/cmd/server
	return filepath.Join(".", "..", "..")
}
