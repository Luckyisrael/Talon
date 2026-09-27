package ai

import (
	"regexp"
	"testing"
)

// S9 — the tool contract (ENGINEERING_STANDARDS.md §2/§3).
//
// Failure modes, written first (TEST_RULES §3):
//
//  1. The prompt names a tool the core filter strips. The model plans around a
//     tool it was never given — Run 1 (2026-09-26): SystemPromptBuild ordered
//     run_shell / install_package / git_* / run_bundle / run_gradle by name,
//     filterToCoreTools stripped all of them every round, the model concluded
//     "I have no shell", and an hour-long build produced 0 commits, 0 installs,
//     0 bundle gates.
//  2. A future prompt edit names a new tool without adding it to coreToolSet —
//     the same lie, discovered in production instead of CI.
//  3. Device-gated tools (metro_status, capture_screenshot, …) are named under
//     an explicit "their absence means no session is live" paragraph; the
//     prompt explains their absence, so they do not violate the contract.
//  4. A backticked non-tool token that merely looks like a tool name
//     (a JSON key, a field) — exempted explicitly below, never silently.
var deviceGatedTools = map[string]bool{
	"metro_status":       true,
	"capture_screenshot": true,
	"tail_logcat":        true,
	"run_e2e":            true,
	"run_vision":         true,
	"run_boot":           true,
	"run_interact":       true,
}

// promptNonTools are backticked lowercase_underscore tokens in the prompt that
// are not tools. Each needs a reason; an unreviewed entry here is a hole.
var promptNonTools = map[string]string{
	"div":          "DOM element the platform rules forbid — the opposite of a tool",
	"span":         "DOM element the platform rules forbid",
	"button":       "DOM element the platform rules forbid",
	"document":     "DOM global the platform rules forbid",
	"window":       "DOM global the platform rules forbid",
	"main":         "package.json entry field, not a tool",
	"any":          "TypeScript keyword in the type rules",
	"unknown":      "TypeScript keyword in the type rules",
	"node_modules": "directory reference in the package rules",
	"lodash":       "package named as an example of what NOT to add",
	"replace":      "navigation method, not a tool",
}

var backtickToken = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

func TestPromptPromisedToolsSurviveCoreFilter(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range backtickToken.FindAllStringSubmatch(SystemPromptBuild, -1) {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if _, nonTool := promptNonTools[name]; nonTool {
			continue
		}
		if deviceGatedTools[name] {
			continue
		}
		if !coreToolSet[name] {
			t.Errorf("S9 tool contract violated: SystemPromptBuild tells the model to use %q, "+
				"but filterToCoreTools strips it from every oversized request. "+
				"Add it to coreToolSet or stop naming it in the prompt.", name)
		}
	}
	if len(seen) < 10 {
		t.Fatalf("prompt scan found only %d backticked tokens — the extraction is broken, "+
			"not the contract", len(seen))
	}
}

// The filter itself must remain a size valve, not a capability valve: whatever
// it returns must still contain every core tool it was handed (no core tool
// drops out between filter passes), and must never grow.
func TestFilterKeepsCoreTools(t *testing.T) {
	var all []map[string]any
	for name := range coreToolSet {
		all = append(all, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name, "description": name, "parameters": map[string]any{}},
		})
	}
	out := filterToCoreTools(all)
	if len(out) > len(all) {
		t.Fatalf("filter grew the set: %d -> %d", len(all), len(out))
	}
	got := map[string]bool{}
	for _, tool := range out {
		fn, _ := tool["function"].(map[string]any)
		name, _ := fn["name"].(string)
		got[name] = true
	}
	for name := range coreToolSet {
		if !got[name] {
			t.Errorf("filterToCoreTools dropped core tool %q", name)
		}
	}
}
