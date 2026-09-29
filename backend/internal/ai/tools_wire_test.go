package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FM-L30a / L30 — written first (TEST_RULES §3). The failure, measured:
//
// (1) chatWithToolsOpenAI compresses EVERY tool description to its first
// sentence ending at or after 160 chars — P1's Groq-limit fix
// (`7b2f5a4`), shipped beside the core filter whose threshold half was
// later cured (6500 → a 100K valve) while this half was not. It fires
// unconditionally, sized for a context limit the same file declares
// dead, with no test: run_shell loses its tier-2 approval flow and its
// never-ask-for-secrets rule before the model ever sees them.
// Descriptions are contracts with the model (FM-L30a (iii)).
//
// (2) The size valve (toolStripInputTokens → filterToCoreTools) exists
// only inside chatWithToolsOpenAI. The anthropic wire — the one the
// scorecard and every behavior run uses — sends all tools on any body,
// so the pathological-body safety valve is absent where it matters
// (FM-L30a (iv)).
//
// The law: one tools-layer contract, both wires — descriptions travel
// whole everywhere, and the valve fires at the same threshold with the
// same core set on both.

// descOver160 mirrors run_shell's real shape: a first sentence under
// 160, then the sentences P1's cut discards — the approval flow and the
// secret refusal.
const descOver160 = "Run a shell command in the workspace for inspection and debugging: " +
	"grep logs, probe localhost services with curl, drive adb, run node one-liners. " +
	"Read-only commands run freely; workspace-scoped changes run logged; destructive " +
	"commands and network egress beyond localhost need approval. " +
	"Never try to read secrets: .env files, tokens, keys, and the server database are blocked."

// TestOpenAIToolDescriptionsTravelWhole pins the whole contract onto the
// openai wire. Red (P1's uncured half): the description arrives cut at
// the first sentence end ≥160 — the approval and safety sentences gone.
func TestOpenAIToolDescriptionsTravelWhole(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3}}`))
	}))
	defer srv.Close()

	c := &Client{protocol: ProtocolOpenAI, baseURL: srv.URL, apiKey: "k",
		model: "test-model", httpClient: http.DefaultClient}

	tools := []ToolDefinition{{
		Name:        "run_shell",
		Description: descOver160,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}}
	if _, err := c.ChatWithTools(context.Background(), "system", nil, tools, nil); err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}

	var req struct {
		Tools []struct {
			Function struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("parse openai request: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("openai wire carried %d tools, want 1", len(req.Tools))
	}
	if got := req.Tools[0].Function.Description; got != descOver160 {
		t.Errorf("the openai wire cut the contract: got %d of %d bytes\n  want: %q\n  got:  %q",
			len(got), len(descOver160), descOver160, got)
	}
}

// valveToolSet is 14 tools (>12, the valve's admission guard): nine in
// coreToolSet and five specialists the design already classifies as
// strippable (try_edit, keep_edit, web_search, github_search,
// record_failure_pattern are all absent from coreToolSet).
func valveToolSet() []ToolDefinition {
	core := []string{"read_file", "write_file", "edit_file", "multi_edit", "delete_file",
		"list_files", "grep_codebase", "run_checks", "ask_user"}
	non := []string{"try_edit", "keep_edit", "web_search", "github_search", "record_failure_pattern"}
	tools := make([]ToolDefinition, 0, len(core)+len(non))
	for _, n := range append(core, non...) {
		tools = append(tools, ToolDefinition{Name: n, Description: "contract for " + n,
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`)})
	}
	return tools
}

// anthropicRound drives one buffered anthropic round and returns the
// tool names the wire actually carried.
func anthropicRound(t *testing.T, history []Message, tools []ToolDefinition) []string {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`))
	}))
	defer srv.Close()

	c := &Client{protocol: ProtocolAnthropic, baseURL: srv.URL, apiKey: "k",
		model: "test-model", httpClient: http.DefaultClient}
	if _, err := c.ChatWithTools(context.Background(), "sys", history, tools, nil); err != nil {
		t.Fatalf("ChatWithTools: %v", err)
	}

	var req struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(captured, &req); err != nil {
		t.Fatalf("parse anthropic request: %v", err)
	}
	names := make([]string, 0, len(req.Tools))
	for _, tl := range req.Tools {
		names = append(names, tl.Name)
	}
	return names
}

// TestAnthropicStripValveFiresOnOversizedBodies is the valve on the wire
// the scorecard runs. Red: est >100K with 14 tools sends all 14 — no
// valve exists on this protocol, while the openai wire strips the same
// non-core set at the same threshold. Green: core-only, contract-guarded
// by coreToolSet (TestPromptPromisedToolsSurviveCoreFilter keeps the
// prompt's promises on the surviving set).
func TestAnthropicStripValveFiresOnOversizedBodies(t *testing.T) {
	// est = (system + messages + tools)/4 > 100,000 → a giant history,
	// the shape the valve exists for (RESEARCH measured a 294KB body).
	history := []Message{UserText(strings.Repeat("x", 410_000))}
	names := anthropicRound(t, history, valveToolSet())

	present := map[string]bool{}
	for _, n := range names {
		present[n] = true
	}
	sawNonCore := false
	for _, n := range []string{"try_edit", "keep_edit", "web_search", "github_search", "record_failure_pattern"} {
		if present[n] {
			sawNonCore = true
			t.Errorf("the anthropic wire has no size valve: non-core %q survived an est>100K body (%d tools sent)", n, len(names))
		}
	}
	for _, n := range []string{"read_file", "write_file", "edit_file", "run_checks", "ask_user"} {
		if !present[n] {
			t.Errorf("core tool %q was stripped — the valve must never touch the contract set", n)
		}
	}
	if sawNonCore {
		t.Logf("non-core tools present — the valve never fired on this wire")
	}
}

// TestAnthropicToolsUnchangedOnHonestBodies pins the recipe: an honest
// est (far below 100K) carries every tool byte-identically before and
// after the fix — the valve is a safety valve, never a diet.
func TestAnthropicToolsUnchangedOnHonestBodies(t *testing.T) {
	tools := valveToolSet()
	names := anthropicRound(t, []Message{UserText("make the dashboard")}, tools)
	if len(names) != len(tools) {
		t.Fatalf("honest body lost tools: got %d of %d", len(names), len(tools))
	}
}

// TestAnthropicValveKeepsAllWhenCoreWouldGoUnder8 pins the shared guard:
// when filtering would leave fewer than 8 tools the filter returns the
// original set (a request with almost no tools is worse than a big one —
// filterToCoreTools' own rule, mirrored on the anthropic wire).
func TestAnthropicValveKeepsAllWhenCoreWouldGoUnder8(t *testing.T) {
	// 15 tools, exactly three in coreToolSet → filtered would be 3 < 8.
	tools := []ToolDefinition{}
	for _, n := range []string{"read_file", "write_file", "ask_user"} {
		tools = append(tools, ToolDefinition{Name: n, Description: "c",
			InputSchema: json.RawMessage(`{}`)})
	}
	for _, n := range []string{"try_edit", "keep_edit", "run_command", "run_gate", "write_adr",
		"write_memory", "record_failure_pattern", "read_memory", "web_search", "github_search",
		"capture_screenshot", "tail_logcat"} {
		tools = append(tools, ToolDefinition{Name: n, Description: "c",
			InputSchema: json.RawMessage(`{}`)})
	}
	names := anthropicRound(t, []Message{UserText(strings.Repeat("x", 410_000))}, tools)
	if len(names) != len(tools) {
		t.Errorf("core-under-8 guard did not hold: wire carried %d of %d tools", len(names), len(tools))
	}
}
