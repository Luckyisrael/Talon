package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
)

// Plan mode must never be able to write files. The guarantee is structural, not
// a matter of the model choosing to behave: the plan path (ai.Plan → chat)
// builds a model request that carries no tool definitions at all, so there is
// no write_file for the model to call. The build path (orchestrator →
// ChatWithTools) is the only one that attaches the file-mutating tools.
//
// This test pins that down at the wire. It drives a real plan turn through the
// handler against a stand-in model server and asserts that what actually reached
// the model carried the plan system prompt and no "tools" field. If someone
// later routes planning through a tool-capable path, or attaches tools to the
// plan request, the model could start writing files from plan mode — and this
// test fails first.
func TestPlanModeSendsNoToolsToModel(t *testing.T) {
	var mu sync.Mutex
	var captured []map[string]any

	// A stand-in model API: it records every request body, then returns a valid
	// Anthropic reply whose single text block is a parseable plan, so Plan()
	// succeeds on the first attempt and the handler runs its happy path.
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err == nil {
			mu.Lock()
			captured = append(captured, body)
			mu.Unlock()
		}
		planText := `{"message":"Here is a plan.","plan":{"screens":[{"name":"Home","description":"Landing"}],"features":[],"navigation":"stack","packages":[]}}`
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": planText}},
		})
	}))
	defer model.Close()

	// Point a real ai.Client at the stand-in model — ai.New reads these four.
	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", model.URL)
	t.Setenv("AI_API_KEY", "test-key")
	t.Setenv("AI_MODEL", "test-model")

	aiClient, err := ai.New()
	if err != nil {
		t.Fatalf("ai.New: %v", err)
	}

	st, err := store.New(filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("planner@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	project, err := st.CreateProject(user.ID, "Wallet", "v1", "wallet", "com.local.wallet", "com.local.wallet", "wallet")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	orch := orchestrator.New(aiClient, st, nil)
	// The workspace is a throwaway temp dir; a correct plan turn never writes to
	// it, and the assertions below hold regardless of what is on disk.
	h := NewPromptHandler(st, aiClient, t.TempDir(), orch, nil, nil)

	body := fmt.Sprintf(`{"project_id":%d,"prompt":"Plan a wallet app for the Seeker phone","mode":"plan"}`, project.ID)
	rec := httptest.NewRecorder()
	h.Create(rec, authedRequest(user.ID, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("plan turn: status %d, body %s", rec.Code, rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("the model was never called; the plan path did not run")
	}
	for i, reqBody := range captured {
		if _, ok := reqBody["tools"]; ok {
			t.Errorf("plan request %d carried a \"tools\" field; plan mode must be toolless so the model cannot write files", i)
		}
		// A belt-and-braces check that the turn genuinely went through the plan
		// path: the build path would carry a different system prompt (and tools).
		// The plan prompt is assembled at startup with the template's package list
		// folded in, so compare against the handler's own copy rather than a const.
		if sys, _ := reqBody["system"].(string); sys != h.planPrompt {
			t.Errorf("plan request %d did not use the plan system prompt; the turn did not go through the plan path", i)
		}
	}
}
