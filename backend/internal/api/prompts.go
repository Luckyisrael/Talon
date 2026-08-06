package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/store"
)

type PromptHandler struct {
	store *store.Store
	ai    *ai.Client
}

func NewPromptHandler(s *store.Store, a *ai.Client) *PromptHandler {
	return &PromptHandler{store: s, ai: a}
}

type promptRequest struct {
	ProjectID int64  `json:"project_id"`
	Prompt    string `json:"prompt"`
	Mode      string `json:"mode"` // "plan" or "build"
}

func (h *PromptHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req promptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Prompt == "" || req.Mode == "" {
		writeError(w, http.StatusBadRequest, "prompt and mode required")
		return
	}

	if req.Mode != "plan" && req.Mode != "build" {
		writeError(w, http.StatusBadRequest, "mode must be 'plan' or 'build'")
		return
	}

	// Check usage limits
	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check usage")
		return
	}

	if usage.PromptsUsed >= 20 {
		writeError(w, http.StatusTooManyRequests, "free tier limit reached (20 prompts/month)")
		return
	}

	// Create prompt record
	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	// Build conversation history
	history := []ai.PromptMessage{
		{Role: "user", Content: req.Prompt},
	}

	var response string

	if req.Mode == "plan" {
		planResp, err := h.ai.Plan(context.Background(), history)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "AI request failed: "+err.Error())
			return
		}
		respBytes, _ := json.Marshal(planResp)
		response = string(respBytes)

		// Save plan to project if plan is returned
		if planResp.Plan != nil {
			planJSON, _ := json.Marshal(planResp.Plan)
			h.store.UpdateProjectPlan(req.ProjectID, string(planJSON))
		}
	} else {
		// Get workspace path for this project
		workspacePath := h.getWorkspacePath(req.ProjectID)

		buildResp, err := h.ai.Build(context.Background(), history, workspacePath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "AI request failed: "+err.Error())
			return
		}

		// Write generated files to workspace
		if workspacePath != "" {
			h.writeGeneratedFiles(workspacePath, buildResp.Files)
		}

		// Run build verification
		buildResult := h.verifyBuild(workspacePath, buildResp.Commands)

		respBytes, _ := json.Marshal(buildResp)
		response = string(respBytes)

		// Append build verification result to response
		if buildResult != "" {
			responseMap := make(map[string]interface{})
			json.Unmarshal(respBytes, &responseMap)
			responseMap["build_result"] = buildResult
			updatedBytes, _ := json.Marshal(responseMap)
			response = string(updatedBytes)
		}
	}

	// Update prompt with response
	h.store.UpdatePromptResponse(prompt.ID, response)
	h.store.IncrementPrompts(userID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       prompt.ID,
		"mode":     req.Mode,
		"response": json.RawMessage(response),
	})
}

func (h *PromptHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid prompt id")
		return
	}

	prompt, err := h.store.GetPrompt(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "prompt not found")
		return
	}

	writeJSON(w, http.StatusOK, prompt)
}

func (h *PromptHandler) getWorkspacePath(projectID int64) string {
	workspaceDir := os.Getenv("WORKSPACE_DIR")
	if workspaceDir == "" {
		return ""
	}

	// Find workspace directory for this project
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			path := filepath.Join(workspaceDir, entry.Name(), "App.tsx")
			if _, err := os.Stat(path); err == nil {
				// This looks like a project workspace
				return filepath.Join(workspaceDir, entry.Name())
			}
		}
	}

	// Create new workspace if not found
	workspacePath := filepath.Join(workspaceDir, "project-"+strconv.FormatInt(projectID, 10))
	os.MkdirAll(workspacePath, 0755)
	return workspacePath
}

func (h *PromptHandler) writeGeneratedFiles(workspacePath string, files []ai.File) {
	for _, file := range files {
		// Sanitize path - prevent directory traversal
		cleanPath := filepath.Clean(file.Path)
		if strings.Contains(cleanPath, "..") {
			continue
		}

		fullPath := filepath.Join(workspacePath, cleanPath)

		// Create directory if needed
		dir := filepath.Dir(fullPath)
		os.MkdirAll(dir, 0755)

		// Write file
		os.WriteFile(fullPath, []byte(file.Content), 0644)
	}
}

func (h *PromptHandler) verifyBuild(workspacePath string, commands []string) string {
	if workspacePath == "" || len(commands) == 0 {
		return ""
	}

	var results []string

	for _, cmd := range commands {
		// Parse command
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}

		// Only allow safe commands
		allowedCommands := map[string]bool{
			"npm":      true,
			"npx":      true,
			"yarn":     true,
			"tsc":      true,
			"eslint":   true,
			"prettier": true,
		}

		if !allowedCommands[parts[0]] {
			results = append(results, "SKIPPED: "+cmd+" (command not allowed)")
			continue
		}

		// Execute command
		ctx := context.Background()
		var execCmd *exec.Cmd

		if parts[0] == "cd" && len(parts) >= 3 && parts[2] == "&&" {
			// Handle "cd dir && command" pattern
			dir := parts[1]
			remaining := strings.Join(parts[3:], " ")
			execCmd = exec.CommandContext(ctx, "sh", "-c", "cd "+filepath.Join(workspacePath, dir)+" && "+remaining)
		} else {
			execCmd = exec.CommandContext(ctx, parts[0], parts[1:]...)
			execCmd.Dir = workspacePath
		}

		output, err := execCmd.CombinedOutput()
		outputStr := string(output)

		if err != nil {
			results = append(results, "FAILED: "+cmd+"\n"+outputStr+"\nError: "+err.Error())
		} else {
			results = append(results, "OK: "+cmd)
		}
	}

	return strings.Join(results, "\n")
}
