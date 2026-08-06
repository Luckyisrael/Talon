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

	// Build conversation history with memory context
	history := h.buildHistoryWithMemory(req.ProjectID, req.Prompt)

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

			// Store plan in memory
			h.store.AddMemory(req.ProjectID, "plan", planResp.Message, "")

			// Store each screen as a pending item
			for _, screen := range planResp.Plan.Screens {
				h.store.AddMemory(req.ProjectID, "pending_screen", screen.Name+": "+screen.Description, "")
			}

			// Store features as pending
			for _, feature := range planResp.Plan.Features {
				h.store.AddMemory(req.ProjectID, "pending_feature", feature, "")
			}
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

		// Run build verification with auto-fix
		buildResult, fixed := h.verifyBuildWithAutoFix(workspacePath, buildResp.Commands, buildResp.Files)

		respBytes, _ := json.Marshal(buildResp)
		response = string(respBytes)

		// Append build verification result to response
		if buildResult != "" {
			responseMap := make(map[string]interface{})
			json.Unmarshal(respBytes, &responseMap)
			responseMap["build_result"] = buildResult
			if fixed {
				responseMap["auto_fixed"] = true
			}
			updatedBytes, _ := json.Marshal(responseMap)
			response = string(updatedBytes)
		}

		// Update memory with what was done
		h.updateMemoryAfterBuild(req.ProjectID, buildResp.Files, req.Prompt)
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

func (h *PromptHandler) GetMemory(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "projectID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	memories, err := h.store.GetAllMemory(projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get memory")
		return
	}

	// Build summary
	summary := h.buildMemorySummary(memories)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories": memories,
		"summary":  summary,
	})
}

func (h *PromptHandler) buildHistoryWithMemory(projectID int64, userPrompt string) []ai.PromptMessage {
	history := []ai.PromptMessage{}

	// Get all memory for this project
	memories, err := h.store.GetAllMemory(projectID)
	if err == nil && len(memories) > 0 {
		summary := h.buildMemorySummary(memories)
		if summary != "" {
			history = append(history, ai.PromptMessage{
				Role:    "user",
				Content: "PROJECT CONTEXT (what has been done so far):\n\n" + summary,
			})
			history = append(history, ai.PromptMessage{
				Role:    "assistant",
				Content: "I understand the project context. I'll review the existing code and continue from where we left off.",
			})
		}
	}

	// Add the actual user prompt
	history = append(history, ai.PromptMessage{
		Role:    "user",
		Content: userPrompt,
	})

	return history
}

func (h *PromptHandler) buildMemorySummary(memories []store.ProjectMemory) string {
	var completed []string
	var pending []string
	var notes []string

	// Limit to most recent entries to avoid context overflow
	maxEntries := 10
	startIdx := 0
	if len(memories) > maxEntries {
		startIdx = len(memories) - maxEntries
	}

	for _, m := range memories[startIdx:] {
		switch m.EntryType {
		case "completed_screen", "completed_feature":
			// Extract just the name, not the full description
			name := m.Content
			if idx := strings.Index(name, ":"); idx > 0 {
				name = name[:idx]
			}
			completed = append(completed, name)
		case "pending_screen", "pending_feature":
			// Extract just the name, not the full description
			name := m.Content
			if idx := strings.Index(name, ":"); idx > 0 {
				name = name[:idx]
			}
			pending = append(pending, name)
		case "note":
			// Truncate long notes
			note := m.Content
			if len(note) > 100 {
				note = note[:100] + "..."
			}
			notes = append(notes, note)
		}
	}

	var parts []string

	if len(completed) > 0 {
		parts = append(parts, "BUILT: "+strings.Join(completed, ", "))
	}

	if len(pending) > 0 {
		parts = append(parts, "TODO: "+strings.Join(pending, ", "))
	}

	if len(notes) > 0 {
		parts = append(parts, "NOTES: "+strings.Join(notes, " | "))
	}

	return strings.Join(parts, "\n")
}

func (h *PromptHandler) updateMemoryAfterBuild(projectID int64, files []ai.File, userPrompt string) {
	// Mark completed screens/features based on files generated
	for _, file := range files {
		// Extract screen/feature name from path
		if strings.Contains(file.Path, "screens/") {
			name := filepath.Base(file.Path)
			name = strings.TrimSuffix(name, filepath.Ext(name))
			h.store.AddMemory(projectID, "completed_screen", "Screen: "+name+" (in "+file.Path+")", "")
			// Remove from pending if exists
			h.removePendingByName(projectID, name)
		} else if strings.Contains(file.Path, "components/") {
			name := filepath.Base(file.Path)
			name = strings.TrimSuffix(name, filepath.Ext(name))
			h.store.AddMemory(projectID, "completed_feature", "Component: "+name+" (in "+file.Path+")", "")
		}
	}

	// Add note about what was requested
	h.store.AddMemory(projectID, "note", "User requested: "+userPrompt, "")
}

func (h *PromptHandler) removePendingByName(projectID int64, name string) {
	memories, err := h.store.GetMemoryByType(projectID, "pending_screen")
	if err != nil {
		return
	}
	for _, m := range memories {
		if strings.Contains(strings.ToLower(m.Content), strings.ToLower(name)) {
			h.store.DeleteMemory(m.ID)
		}
	}
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
		cleanPath := filepath.Clean(file.Path)
		if strings.Contains(cleanPath, "..") {
			continue
		}

		fullPath := filepath.Join(workspacePath, cleanPath)
		dir := filepath.Dir(fullPath)
		os.MkdirAll(dir, 0755)
		os.WriteFile(fullPath, []byte(file.Content), 0644)
	}
}

func (h *PromptHandler) verifyBuildWithAutoFix(workspacePath string, commands []string, files []ai.File) (string, bool) {
	if workspacePath == "" || len(commands) == 0 {
		return "", false
	}

	var results []string
	fixed := false

	for _, cmd := range commands {
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}

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

		ctx := context.Background()
		var execCmd *exec.Cmd

		if parts[0] == "cd" && len(parts) >= 3 && parts[2] == "&&" {
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
			// Check if it's a lint/type error that can be auto-fixed
			if h.isAutoFixableError(outputStr) {
				fixResult := h.autoFixErrors(workspacePath, outputStr, files)
				if fixResult != "" {
					results = append(results, "AUTO-FIXED: "+cmd+"\n"+fixResult)
					fixed = true
					continue
				}
			}
			results = append(results, "FAILED: "+cmd+"\n"+outputStr+"\nError: "+err.Error())
		} else {
			results = append(results, "OK: "+cmd)
		}
	}

	return strings.Join(results, "\n"), fixed
}

func (h *PromptHandler) isAutoFixableError(output string) bool {
	fixablePatterns := []string{
		"error TS",           // TypeScript errors
		"error:",             // General errors
		"Cannot find module", // Missing imports
		"Property .* does not exist",
		"Type .* is not assignable",
		"Argument of type",
		"Syntax error",
	}

	for _, pattern := range fixablePatterns {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

func (h *PromptHandler) autoFixErrors(workspacePath string, errorOutput string, files []ai.File) string {
	// Build context with the errors
	history := []ai.PromptMessage{
		{Role: "user", Content: "The build produced these errors. Fix them by providing corrected file contents:\n\n" + errorOutput},
	}

	// Include the files that were generated
	if len(files) > 0 {
		fileContext := "Files that were generated:\n"
		for _, f := range files {
			fileContext += "\n=== " + f.Path + " ===\n" + f.Content
		}
		history = append([]ai.PromptMessage{{Role: "user", Content: fileContext}}, history...)
	}

	// Add existing project files for context
	existingFiles := h.ai.ReadProjectFiles(workspacePath)
	if existingFiles != "" {
		history = append([]ai.PromptMessage{{Role: "user", Content: "Existing project files:\n\n" + existingFiles}}, history...)
	}

	fixResp, err := h.ai.Build(context.Background(), history, workspacePath)
	if err != nil {
		return ""
	}

	// Write the fixed files
	if workspacePath != "" {
		h.writeGeneratedFiles(workspacePath, fixResp.Files)
	}

	return "Fixed " + strconv.Itoa(len(fixResp.Files)) + " files"
}
