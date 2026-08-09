package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/devclient"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
	"github.com/seekersstudio/backend/internal/tools"
)

type PromptHandler struct {
	store       *store.Store
	ai          *ai.Client
	workspaceDir string
	orch        *orchestrator.Orchestrator
	toolReg     *tools.Registry
	devClient   *devclient.Manager
}

func NewPromptHandler(s *store.Store, a *ai.Client, workspaceDir string, orch *orchestrator.Orchestrator, dc *devclient.Manager) *PromptHandler {
	reg := tools.NewRegistry()
	reg.Register(&tools.ListFiles{})
	reg.Register(&tools.ReadFile{})
	reg.Register(&tools.RunCommand{})
	reg.Register(&tools.RunTypecheck{})
	reg.Register(&tools.RunLint{})
	reg.Register(&tools.InstallPackage{})
	reg.Register(&tools.BuildAPK{})

	// Phase 7: Register ADR and memory tools
	storeAdapter := &tools.StoreAdapter{
		CreateADRFn: func(projectID int64, decision, context, options, outcome string) error {
			_, err := s.CreateADR(projectID, decision, context, options, outcome)
			return err
		},
		AddMemoryFn: s.AddMemory,
	}
	reg.Register(tools.NewWriteADR(storeAdapter))
	reg.Register(tools.NewWriteMemory(storeAdapter))

	return &PromptHandler{
		store:       s,
		ai:          a,
		workspaceDir: workspaceDir,
		orch:        orch,
		toolReg:     reg,
		devClient:   dc,
	}
}

type promptRequest struct {
	ProjectID int64  `json:"project_id"`
	Prompt    string `json:"prompt"`
	Mode      string `json:"mode"`
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

	// Validate prompt length
	if len(req.Prompt) > 50000 {
		writeError(w, http.StatusBadRequest, "prompt too long (max 50000 characters)")
		return
	}

	// Validate project ownership (IDOR prevention)
	if req.ProjectID > 0 {
		project, err := h.store.GetProject(req.ProjectID)
		if err != nil || project.UserID != userID {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
	}

	if req.Mode == "plan" && isExecutionRequest(req.Prompt) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":   0,
			"mode": "plan",
			"response": map[string]interface{}{
				"message": "You're currently in Plan mode. To generate code, switch to Build mode.",
				"plan":    nil,
			},
		})
		return
	}

	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check usage")
		return
	}

	if usage.PromptsUsed >= 20 {
		writeError(w, http.StatusTooManyRequests, "free tier limit reached (20 prompts/month)")
		return
	}

	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	history := h.buildHistoryWithMemory(req.ProjectID, req.Prompt)

	var response string

	if req.Mode == "plan" {
		planResp, err := h.ai.Plan(context.Background(), history)
		if err != nil {
			log.Printf("AI plan error for user %d: %v", userID, err)
			writeError(w, http.StatusInternalServerError, "AI request failed")
			return
		}
		respBytes, _ := json.Marshal(planResp)
		response = string(respBytes)

		if planResp.Plan != nil {
			planJSON, _ := json.Marshal(planResp.Plan)
			h.store.UpdateProjectPlan(req.ProjectID, string(planJSON))
			h.store.AddMemory(req.ProjectID, "plan", planResp.Message, "")
			for _, screen := range planResp.Plan.Screens {
				h.store.AddMemory(req.ProjectID, "pending_screen", screen.Name+": "+screen.Description, "")
			}
			for _, feature := range planResp.Plan.Features {
				h.store.AddMemory(req.ProjectID, "pending_feature", feature, "")
			}
		}
	} else {
		workspacePath := h.getWorkspacePath(req.ProjectID)

		sess := h.orch.CreateSession(prompt.ID, req.ProjectID, workspacePath)
		sess.Staging.Init()

		buildTools := h.buildToolList(workspacePath, sess.StagingPath)
		toolExecutor := h.makeToolExecutor(workspacePath, sess.StagingPath)

		systemPrompt := h.buildSystemPrompt(workspacePath)
		err := h.orch.RunBuildWithTools(context.Background(), sess, req.Prompt, systemPrompt, history, buildTools, toolExecutor)
		if err != nil {
			log.Printf("build error for user %d project %d: %v", userID, req.ProjectID, err)
			writeError(w, http.StatusInternalServerError, "build failed")
			return
		}

		var buildResp ai.BuildResponse
		stagedFiles, _ := sess.Staging.ListFiles()
		for _, path := range stagedFiles {
			content, readErr := sess.Staging.ReadFile(path)
			if readErr == nil {
				buildResp.Files = append(buildResp.Files, ai.File{Path: path, Content: content})
			}
		}
		buildResp.Message = "Build completed successfully"

		// Check if build introduced new native dependencies requiring rebuild
		h.checkAndTriggerRebuild(context.Background(), req.ProjectID, workspacePath, stagedFiles, sess)

		respBytes, _ := json.Marshal(buildResp)
		response = string(respBytes)

		h.updateMemoryAfterBuild(req.ProjectID, buildResp.Files, req.Prompt)
		h.orch.RemoveSession(prompt.ID)
	}

	h.store.UpdatePromptResponse(prompt.ID, response)
	h.store.IncrementPrompts(userID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       prompt.ID,
		"mode":     req.Mode,
		"response": json.RawMessage(response),
	})
}

func (h *PromptHandler) Stream(w http.ResponseWriter, r *http.Request) {
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

	if req.Mode == "plan" && isExecutionRequest(req.Prompt) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}
		msg := "You're currently in Plan mode. To generate code, switch to Build mode."
		writeSSE(w, "text.start", nil)
		writeSSE(w, "text.delta", ai.StreamTextDelta{Text: msg})
		writeSSE(w, "text.end", nil)
		writeSSE(w, "done", nil)
		flusher.Flush()
		return
	}

	usage, err := h.store.GetUsage(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check usage")
		return
	}

	if usage.PromptsUsed >= 20 {
		writeError(w, http.StatusTooManyRequests, "free tier limit reached (20 prompts/month)")
		return
	}

	prompt, err := h.store.CreatePrompt(req.ProjectID, req.Mode, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create prompt")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	history := h.buildHistoryWithMemory(req.ProjectID, req.Prompt)
	ctx := r.Context()

	if req.Mode == "plan" {
		h.streamPlanMode(ctx, w, flusher, prompt.ID, req.ProjectID, history)
		h.store.IncrementPrompts(userID)
		return
	}

	workspacePath := h.getWorkspacePath(req.ProjectID)
	sess := h.orch.CreateSession(prompt.ID, req.ProjectID, workspacePath)
	sess.Staging.Init()

	systemPrompt := h.buildSystemPrompt(workspacePath)
	buildTools := h.buildToolList(workspacePath, sess.StagingPath)
	toolExecutor := h.makeToolExecutor(workspacePath, sess.StagingPath)

	var mu sync.Mutex
	var fullText strings.Builder

	evtChan := make(chan events.Event, 64)
	go func() {
		defer close(evtChan)
		for {
			select {
			case <-ctx.Done():
				return
			case evt, ok := <-sess.Emitter.Events():
				if !ok {
					return
				}
				evtChan <- evt
			}
		}
	}()

	go func() {
		err := h.orch.RunBuildWithTools(ctx, sess, req.Prompt, systemPrompt, history, buildTools, toolExecutor)
		if err != nil {
			log.Printf("stream build error for user %d: %v", userID, err)
			mu.Lock()
			fullText.WriteString("\n\nBuild failed. Check server logs for details.")
			mu.Unlock()
			// Send error event to client
			select {
			case evtChan <- events.Event{
				Type:   events.ErrorEvent,
				Status: "error",
				Error:  err.Error(),
			}:
			default:
			}
		}
	}()

	writeSSE(w, "text.start", nil)
	flusher.Flush()

done:
	for {
		select {
		case <-ctx.Done():
			break done
		case evt, ok := <-evtChan:
			if !ok {
				break done
			}
			h.writeEventAsSSE(w, evt)
			flusher.Flush()
		}
	}

	for evt := range sess.Emitter.Events() {
		h.writeEventAsSSE(w, evt)
		flusher.Flush()
	}

	responseText := fullText.String()
	if responseText == "" {
		responseText = "Build completed"
	}

	h.store.UpdatePromptResponse(prompt.ID, responseText)
	h.store.IncrementPrompts(userID)

	h.orch.RemoveSession(prompt.ID)
	writeSSE(w, "done", nil)
	flusher.Flush()
}

func (h *PromptHandler) streamPlanMode(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, promptID, projectID int64, history []ai.PromptMessage) {
	streamEvents := h.ai.ChatStream(ctx, "", history)

	var fullText strings.Builder

	for event := range streamEvents {
		if ctx.Err() != nil {
			break
		}

		switch event.Type {
		case "thinking.start", "thinking.delta", "thinking.end":
			writeSSE(w, event.Type, event.Data)
		case "text.start":
			writeSSE(w, "text.start", nil)
		case "text.delta":
			if td, ok := event.Data.(ai.StreamTextDelta); ok {
				fullText.WriteString(td.Text)
			}
			writeSSE(w, "text.delta", event.Data)
		case "text.end":
			writeSSE(w, "text.end", nil)
		case "done":
			responseText := fullText.String()
			responseText = ai.StripCodeBlocks(responseText)

			var planResp ai.PlanResponse
			if json.Unmarshal([]byte(responseText), &planResp) == nil {
				if planResp.Plan != nil {
					planJSON, _ := json.Marshal(planResp.Plan)
					h.store.UpdateProjectPlan(projectID, string(planJSON))
					h.store.AddMemory(projectID, "plan", planResp.Message, "")
					for _, screen := range planResp.Plan.Screens {
						writeSSE(w, "tool.start", ai.StreamToolStart{
							Name:  "plan_screen",
							Input: map[string]string{"name": screen.Name, "description": screen.Description},
						})
						h.store.AddMemory(projectID, "pending_screen", screen.Name+": "+screen.Description, "")
						writeSSE(w, "tool.end", ai.StreamToolEnd{Name: "plan_screen", Result: "planned"})
					}
				}
				h.store.UpdatePromptResponse(promptID, responseText)
			} else {
				h.store.UpdatePromptResponse(promptID, responseText)
			}

			writeSSE(w, "done", nil)
		case "error":
			writeSSE(w, "error", map[string]string{"message": event.Error})
			h.store.UpdatePromptResponse(promptID, "Error: "+event.Error)
		}

		flusher.Flush()
	}
}

func (h *PromptHandler) writeEventAsSSE(w http.ResponseWriter, evt events.Event) {
	switch evt.Type {
	case events.PhaseEvent:
		writeSSE(w, "phase", map[string]interface{}{
			"phase":  evt.Phase,
			"status": evt.Status,
		})
	case events.ToolCallEvent:
		writeSSE(w, "tool_call", map[string]interface{}{
			"tool":   evt.Tool,
			"path":   evt.Path,
			"name":   evt.Name,
			"status": evt.Status,
		})
	case events.VerifyEvent:
		writeSSE(w, "verify", map[string]interface{}{
			"stage":  evt.Tool,
			"status": evt.Status,
			"error":  evt.Error,
		})
	case events.GuardrailEvent:
		writeSSE(w, "guardrail_reject", map[string]interface{}{
			"tool":   evt.Tool,
			"status": evt.Status,
			"error":  evt.Error,
		})
	case events.ErrorEvent:
		writeSSE(w, "error", map[string]string{"message": evt.Error})
	case events.DoneEvent:
		writeSSE(w, "done", map[string]string{"status": evt.Status})
	}
}

func (h *PromptHandler) buildToolList(workspacePath, stagingPath string) []ai.ToolDefinition {
	h.toolReg = tools.NewRegistry()

	fileTools := &tools.ListFiles{WorkspacePath: workspacePath}
	h.toolReg.Register(fileTools)

	readTool := &tools.ReadFile{WorkspacePath: workspacePath}
	h.toolReg.Register(readTool)

	writeTool := &tools.WriteFileStaged{StagingPath: stagingPath}
	h.toolReg.Register(writeTool)

	deleteTool := &tools.DeleteFileStaged{StagingPath: stagingPath}
	h.toolReg.Register(deleteTool)

	grepTool := &tools.GrepCodebase{WorkspacePath: workspacePath}
	h.toolReg.Register(grepTool)

	cmdTool := &tools.RunCommand{WorkspacePath: workspacePath}
	h.toolReg.Register(cmdTool)

	typecheckTool := &tools.RunTypecheck{WorkspacePath: workspacePath}
	h.toolReg.Register(typecheckTool)

	lintTool := &tools.RunLint{WorkspacePath: workspacePath}
	h.toolReg.Register(lintTool)

	// InstallPackage uses the dev-client allowlist for native detection
	var allowlist *devclient.Allowlist
	if h.devClient != nil {
		allowlist = h.devClient.Allowlist()
	}
	installTool := &tools.InstallPackage{WorkspacePath: workspacePath, Allowlist: allowlist}
	h.toolReg.Register(installTool)

	buildTool := &tools.BuildAPK{WorkspacePath: workspacePath, Allowlist: allowlist}
	h.toolReg.Register(buildTool)

	defs := h.toolReg.List()
	result := make([]ai.ToolDefinition, len(defs))
	for i, d := range defs {
		result[i] = ai.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		}
	}
	return result
}

func (h *PromptHandler) makeToolExecutor(workspacePath, stagingPath string) func(ctx context.Context, name string, input json.RawMessage) (string, error) {
	return func(ctx context.Context, name string, input json.RawMessage) (string, error) {
		result, err := h.toolReg.Execute(ctx, name, input)
		if err != nil {
			return result.Content, err
		}
		return result.Content, nil
	}
}

func (h *PromptHandler) buildSystemPrompt(workspacePath string) string {
	existingFiles := h.ai.ReadProjectFiles(workspacePath)
	contextPart := ""
	if existingFiles != "" {
		contextPart = "\n\nExisting project files:\n\n" + existingFiles
	}

	// Inject native-module allowlist into AI context
	allowlistPart := ""
	if h.devClient != nil {
		al := h.devClient.Allowlist()
		if al != nil {
			allowlistPart = "\n\n" + al.FormatForPrompt()
		}
	}

	return systemPromptBuild + allowlistPart + contextPart
}

const systemPromptBuild = `CRITICAL RULES — VIOLATION IS FAILURE:
1. You are building a REACT NATIVE / EXPO mobile app for ANDROID (Seeker device).
2. You must NEVER generate HTML, CSS, vanilla JavaScript, index.html, or any web code.
3. You must NEVER create website files. This is NOT a web app.
4. ONLY use React Native components: View, Text, StyleSheet, TouchableOpacity, ScrollView, FlatList, TextInput, Switch, SafeAreaView, etc.
5. ONLY use Expo libraries and expo-router for navigation.

You are a React Native engineer specializing in Solana Mobile apps for the Seeker device.

=== CRITICAL GUARDRAILS ===

1. REACT NATIVE ONLY: Use View, Text, StyleSheet, TouchableOpacity, ScrollView, FlatList, TextInput, Switch, SafeAreaView. NEVER use div, span, p, button, input, or any HTML elements.

2. READ BEFORE EDIT: The existing project files are provided in context. Read them before generating code.

3. NEVER DELETE: Do not remove existing functionality unless the user explicitly says "delete" or "remove".

4. PRESERVE PATTERNS: Match the existing code style exactly.

5. VALIDATE PATHS: File paths must be relative to the project root (e.g., "src/screens/HomeScreen.tsx").

6. TYPE SAFETY: All components must have proper TypeScript types.

7. WALLET PATTERNS: All Solana wallet interactions MUST use MWA protocol. Use transact() from mobile-wallet-adapter-protocol-web3js for signing.

8. SEED VAULT LIMITATION: Seed Vault requires real Seeker hardware and CANNOT be tested in this environment. All wallet implementations must use MWA flows. Any Seed Vault-specific code you produce will be UNVERIFIED on this platform. Default to MWA for anything that needs to be tested here.

9. SINGLE RESPONSIBILITY: Each file should do one thing.

The project already has these packages installed:
- expo, react-native
- @solana/web3.js, @solana/spl-token
- @solana-mobile/mobile-wallet-adapter-protocol-web3js
- @solana-mobile/mobile-wallet-adapter-protocol
- react-native-gesture-handler, react-native-reanimated
- react-native-safe-area-context, react-native-screens
- react-native-quick-crypto, js-base64
- @react-native-async-storage/async-storage
- expo-router, expo-constants, expo-linking, expo-status-bar
- react-native-svg, react-native-webview

Use the tools provided to:
- write_file: Create or overwrite files in the staging area
- read_file: Read existing files to understand the codebase
- list_files: List directory contents
- grep_codebase: Search for patterns in the code
- run_command: Execute build commands (npm, npx, tsc, eslint, expo)
- run_typecheck: Run TypeScript type checking
- run_lint: Run ESLint
- install_package: Install additional npm packages (MWA, Seed Vault, Expo modules)
- build_apk: Build Android APK and install on device (runs expo prebuild + gradle assembleDebug)

Write files using write_file tool. The files will be staged and promoted after verification.`

func writeSSE(w http.ResponseWriter, eventType string, data interface{}) {
	var dataBytes []byte
	if data != nil {
		dataBytes, _ = json.Marshal(data)
	} else {
		dataBytes = []byte("{}")
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(dataBytes))
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

	summary := h.buildMemorySummary(memories)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"memories": memories,
		"summary":  summary,
	})
}

func (h *PromptHandler) buildHistoryWithMemory(projectID int64, userPrompt string) []ai.PromptMessage {
	history := []ai.PromptMessage{}

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

	maxEntries := 10
	startIdx := 0
	if len(memories) > maxEntries {
		startIdx = len(memories) - maxEntries
	}

	for _, m := range memories[startIdx:] {
		switch m.EntryType {
		case "completed_screen", "completed_feature":
			name := m.Content
			if idx := strings.Index(name, ":"); idx > 0 {
				name = name[:idx]
			}
			completed = append(completed, name)
		case "pending_screen", "pending_feature":
			name := m.Content
			if idx := strings.Index(name, ":"); idx > 0 {
				name = name[:idx]
			}
			pending = append(pending, name)
		case "note":
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
	for _, file := range files {
		if strings.Contains(file.Path, "screens/") {
			name := filepath.Base(file.Path)
			name = strings.TrimSuffix(name, filepath.Ext(name))
			h.store.AddMemory(projectID, "completed_screen", "Screen: "+name+" (in "+file.Path+")", "")
			h.removePendingByName(projectID, name)
		} else if strings.Contains(file.Path, "components/") {
			name := filepath.Base(file.Path)
			name = strings.TrimSuffix(name, filepath.Ext(name))
			h.store.AddMemory(projectID, "completed_feature", "Component: "+name+" (in "+file.Path+")", "")
		}
	}
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
	if h.workspaceDir == "" {
		return ""
	}

	projectDir := filepath.Join(h.workspaceDir, strconv.FormatInt(projectID, 10))
	os.MkdirAll(projectDir, 0755)
	return projectDir
}

func isExecutionRequest(prompt string) bool {
	lower := strings.ToLower(prompt)
	triggers := []string{
		"implement", "execute", "build this", "write the code",
		"write code", "generate the code", "generate code",
		"create the", "start coding", "let's code", "code this",
		"write this", "make this", "build it", "start building",
		"create component", "create screen", "add the feature",
		"code it", "develop this", "make it work",
		"create me", "make me", "build me", "i want a", "i need a",
		"i want to", "i need to", "can you create", "can you build",
		"can you make", "can you write", "can you code",
		"simple app", "simple application", "basic app",
		"add two numbers", "calculator", "todo app", "weather app",
		"build a", "make a", "create a",
	}
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// checkAndTriggerRebuild detects if the build introduced new native dependencies
// and triggers an async rebuild if needed. The current dev client remains installed
// and usable while the rebuild runs in the background.
func (h *PromptHandler) checkAndTriggerRebuild(ctx context.Context, projectID int64, workspacePath string, stagedFiles []string, sess *orchestrator.SessionState) {
	if h.devClient == nil {
		return
	}

	// Detect package.json changes in staged files
	pkgChange, err := devclient.DetectPackageChanges(workspacePath, stagedFiles)
	if err != nil || pkgChange == nil {
		return
	}

	if len(pkgChange.Added) == 0 {
		return
	}

	// Check if any added packages need a native rebuild
	needsRebuild, reason := h.devClient.CheckRebuildNeeded(workspacePath, pkgChange.Added)
	if !needsRebuild {
		return
	}

	log.Printf("project %d: native rebuild needed: %s", projectID, reason)
	sess.Emitter.EmitRebuild("needed", reason)

	// Get session's ADB target for rebuild install
	project, err := h.store.GetProject(projectID)
	if err != nil {
		log.Printf("project %d: cannot get project for rebuild: %v", projectID, err)
		return
	}

	// Find active session for this project to get ADB target
	sessions, err := h.store.GetActiveSessions()
	if err != nil {
		return
	}

	var adbTarget string
	for _, s := range sessions {
		if s.ProjectID == project.ID && s.ADBPort > 0 {
			adbTarget = fmt.Sprintf("localhost:%d", s.ADBPort)
			break
		}
	}

	if adbTarget == "" {
		log.Printf("project %d: no active session with device for rebuild", projectID)
		return
	}

	// Start async rebuild — current dev client stays installed
	h.devClient.StartRebuild(ctx, projectID, workspacePath, adbTarget)
	sess.Emitter.EmitRebuild("started", fmt.Sprintf("background rebuild started for: %s", reason))

	// Monitor rebuild in background and surface completion/failure
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				state := h.devClient.GetRebuildState(projectID)
				if state == nil {
					return
				}

				switch state.Status {
				case "success":
					sess.Emitter.EmitRebuild("success", fmt.Sprintf("new dev-client installed: %s", state.NewAPK))
					log.Printf("project %d: background rebuild succeeded", projectID)
					h.devClient.ClearRebuild(projectID)
					return
				case "failed":
					// Fallback: reinstall last-known-good APK
					sess.Emitter.EmitRebuild("failed", fmt.Sprintf("rebuild failed: %s — falling back to last-known-good dev client", state.Error))
					log.Printf("project %d: rebuild failed: %s — attempting fallback reinstall", projectID, state.Error)

					if state.LastGoodAPK != "" {
						if err := h.devClient.InstallDevClient(ctx, adbTarget); err != nil {
							sess.Emitter.EmitRebuild("fallback_failed", fmt.Sprintf("fallback install also failed: %v", err))
							log.Printf("project %d: fallback reinstall failed: %v", projectID, err)
						} else {
							sess.Emitter.EmitRebuild("fallback_success", "restored last-known-good dev client")
							log.Printf("project %d: fallback reinstall succeeded", projectID)
						}
					}

					h.devClient.ClearRebuild(projectID)
					return
				}

				time.Sleep(1 * time.Second)
			}
		}
	}()
}
