// Command talon-headless is the H11 headless profile: the same composition
// as the server — store, provisioned workspace, composition file, workspace
// knowledge, and the real build session behind one PromptHandler — with no
// HTTP/WS. One prompt runs under a scripted provider (or, when
// TALONSTUDIO_HEADLESS_PROVIDER is set, a real one), the workspace is graded
// by the same eval yardstick the harness gates merges with, and the exit
// code comes from the critical checks.
//
// This is the honest end of "no fake paths in CI": CI runs the real boot
// path — the workspace provision symlinks the real template deps, the
// composition applies (profile "headless", capping the round budget),
// knowledge loads, the loop runs the real tool registry, and the
// verification ladder executes for real (tsc / eslint / expo export over
// the shared node_modules) — with only the model scripted. The scorecard
// it prints ends in evals.RenderScorecards, the same report the live eval
// runs write.
//
// Usage:
//
//	TALONSTUDIO_HEADLESS_PROMPT='Add a hello screen' talon-headless
//	TALON_HEADLESS_DB=/tmp/h.db talon-headless
//	go run ./cmd/talon-headless --profile headless
//
// Flags: --profile selects the composition overlay layer (default
// "headless": the shipped talon.composition.yaml caps this profile's round
// budget). Env: TALONSTUDIO_HEADLESS_PROVIDER, when set, points the boot at
// a real provider (AI_PROTOCOL / AI_API_KEY / AI_MODEL alongside it) and
// disables the script — CI never sets it, so CI stays secret-free.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/api"
	"github.com/luckyisrael/talon/backend/internal/composer"
	"github.com/luckyisrael/talon/backend/internal/evals"
	"github.com/luckyisrael/talon/backend/internal/harness/goals"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/workspace"
)

func main() {
	log.SetFlags(0)
	profile := flag.String("profile", "headless",
		"composition overlay profile (H12) — resolves base → profiles[this]")
	flag.Parse()
	scorecard, critical := run(*profile)
	fmt.Println(scorecard)
	if critical {
		os.Exit(1)
	}
}

// run performs the headless boot and returns the scorecard text plus
// whether a critical check failed (boot failures and any eval card whose
// critical results are red).
func run(profile string) (string, bool) {
	checks := []string{}
	failed := func(name string) { checks = append(checks, "FAIL "+name) }
	passed := func(name, note string) {
		checks = append(checks, fmt.Sprintf("PASS %s (%s)", name, note))
	}

	// 1. Boot the real composition: store over a scratch database.
	dbPath := os.Getenv("TALON_HEADLESS_DB")
	if dbPath == "" {
		dbPath = filepath.Join(os.TempDir(), fmt.Sprintf("talon-headless-%d.db", time.Now().UnixNano()))
	}
	st, err := store.New(dbPath)
	if err != nil {
		failed("store-open")
		return render(checks), true
	}
	passed("store-open", dbPath)

	// 2. The shared template: provision source and knowledge base. The walk
	// resolves it from backend/ (what CI does) or the repo root (a laptop);
	// TEMPLATE_DIR overrides both, the same variable the server reads.
	templateDir := resolveTemplateDir()
	if templateDir == "" {
		failed("template")
		return render(checks), true
	}
	passed("template", templateDir)

	// 3. Workspace knowledge. The server treats absence as a WARNING, not a
	// boot stop — the loop runs without skills; this profile mirrors that.
	knowledgeReg, kerr := knowledge.Load(filepath.Join(templateDir, "package.json"))
	if kerr != nil {
		passed("knowledge", "WARNING unavailable: "+kerr.Error())
	} else {
		passed("knowledge", fmt.Sprintf("%d skills", len(knowledgeReg.SkillNames())))
	}

	// 4. The provider. Scripted by default — one tool call, then prose, a
	// deterministic shape CI can trust. TALONSTUDIO_HEADLESS_PROVIDER points
	// at a real one instead, with AI_PROTOCOL / AI_API_KEY / AI_MODEL read
	// from the env the way the server reads them. CI never sets the
	// override, so CI stays secret-free.
	var aiClient *ai.Client
	if override := strings.TrimSpace(os.Getenv("TALONSTUDIO_HEADLESS_PROVIDER")); override != "" {
		if err := os.Setenv("AI_BASE_URL", override); err != nil {
			failed("ai-boot")
			return render(checks), true
		}
		aiClient, err = ai.New()
		if err != nil {
			failed("ai-boot")
			return render(checks), true
		}
		passed("ai-boot", "real provider "+override)
	} else {
		prov := scriptedProvider()
		defer prov.Close()
		t.Setenv("AI_PROTOCOL", "anthropic")
		t.Setenv("AI_BASE_URL", prov.URL)
		t.Setenv("AI_API_KEY", "headless-key")
		t.Setenv("AI_MODEL", "headless-model")
		aiClient, err = ai.New()
		if err != nil {
			failed("ai-boot")
			return render(checks), true
		}
		passed("ai-boot", "scripted provider")
	}

	// 5. Identity rows: a real user and project, so the turn's messages and
	// the append-only log land on real foreign keys instead of dangling ids.
	user, err := st.CreateUser("headless@local", "x")
	if err != nil {
		failed("user-create")
		return render(checks), true
	}
	proj, err := st.CreateProject(user.ID, "headless", "", "headless", "", "", "")
	if err != nil {
		failed("project-create")
		return render(checks), true
	}
	passed("project-create", fmt.Sprintf("id %d", proj.ID))

	// 6. The workspace: the REAL provision — template configs and app dirs
	// copied, node_modules symlinked to the shared tree, git baseline — not
	// a bare MkdirAll. TALON_HEADLESS_WORKSPACE is the workspaces ROOT (each
	// project id gets its child), matching the server's WORKSPACE_DIR.
	wsParent := os.Getenv("TALON_HEADLESS_WORKSPACE")
	if wsParent == "" {
		wsParent = filepath.Join(os.TempDir(), "talon-headless-workspaces")
	}
	mgr, err := workspace.New(templateDir, wsParent)
	if err != nil {
		failed("workspace")
		log.Printf("workspace: %v", err)
		return render(checks), true
	}
	wsDir, err := mgr.Create(proj.ID)
	if err != nil {
		failed("workspace")
		return render(checks), true
	}
	passed("workspace", wsDir)

	// 7. The orchestrator over the real store and the workspace knowledge.
	orch := orchestrator.New(aiClient, st, knowledgeReg)

	// 8. The composition (H12): the file resolves through this profile's
	// layer and applies before the first turn — the same loader and the
	// same apply layer cmd/server uses. No file is a legal dark boot; a
	// malformed row fails the run (wiring you cannot trust must not run).
	comp, err := composer.Load(profile)
	if err != nil {
		failed("composition")
		log.Printf("composition: %v", err)
		return render(checks), true
	}
	if comp == nil {
		passed("composition", "dark boot (no talon.composition.yaml found)")
	} else {
		applied, err := composer.Apply(comp, orch)
		if err != nil {
			failed("composition")
			log.Printf("composition: %v", err)
			return render(checks), true
		}
		passed("composition", fmt.Sprintf("profile %q applied: %s",
			comp.Profile, strings.Join(applied, ", ")))
	}

	// 9. The goal: the durable objective survives the run — the H11
	// durability demonstration harness/goals is declared headless-only for.
	goalSvc := goals.NewService(goals.NewMemStore())
	goal, err := goalSvc.Start(1, headlessPrompt(), 5)
	if err != nil {
		failed("goal-start")
		return render(checks), true
	}
	passed("goal-start", fmt.Sprintf("rev %d", goal.Revision))

	// 10. One real turn through the real loop: the prompt handler wires the
	// build session (tool registry, executor, approval seam, hooks,
	// permission posture), the project-map grounding, the plan seed, and
	// the persisted user turn — the SAME NewAgentTurn the SSE route runs.
	// Only the provider is scripted.
	prompt := headlessPrompt()
	prow, err := st.CreatePrompt(proj.ID, "build", prompt)
	if err != nil {
		failed("prompt-row")
		return render(checks), true
	}
	handler := api.NewPromptHandler(st, aiClient, wsParent, templateDir, orch, nil, knowledgeReg, nil)

	sess := orch.CreateSession(1, proj.ID, wsDir)
	defer sess.Emitter.Close()
	// Drain the event stream: a full channel would block the loop mid-run —
	// the headless profile has no UI consumer. Close stops it via Done().
	go func() {
		for {
			select {
			case <-sess.Emitter.Events():
			case <-sess.Emitter.Done():
				return
			}
		}
	}()

	turn, err := handler.NewAgentTurn(sess, proj.ID, prow.ID, prompt,
		api.BuildOptions{PersistUserTurn: true, Mode: "build"}, os.Stderr)
	if err != nil {
		failed("agent-turn")
		log.Printf("agent-turn: %v", err)
		return render(checks), true
	}
	defer turn.Close()

	outcome, runErr := orch.RunAgentLoop(context.Background(), sess, turn.Options())
	if runErr != nil || outcome == nil {
		failed("agent-loop")
		log.Printf("agent-loop: %v", runErr)
		return render(checks), true
	}
	passed("agent-loop", fmt.Sprintf("%d rounds", outcome.Rounds))

	// 11. Goal accounting: the loop's rounds recorded, the state re-read
	// (simulated restart), the goal survives.
	if _, err := goalSvc.RecordRounds(1, outcome.Rounds); err != nil {
		failed("goal-record")
		return render(checks), true
	}
	cur, err := goalSvc.Current(1)
	if err != nil || cur.ID != goal.ID || cur.Rounds != outcome.Rounds {
		failed("goal-survives-restart")
		return render(checks), true
	}
	passed("goal-survives-restart", goals.ChipText(cur))

	// 12. What the ladder actually did — surfaced, never implied: the policy
	// that classified the sweep, the verified annotation, and every gate
	// that reported unavailable (the fail-open record W6 exists for).
	checks = append(checks, fmt.Sprintf(
		"INFO verify-ladder (policy=%s verified=%v bestEffort=%v skipped=%s)",
		outcome.VerifyPolicy, outcome.Verified, outcome.VerifiedBestEffort,
		strings.Join(outcome.SkippedGates, ",")))

	// 13. Grade with the same yardstick the harness gates merges with: the
	// whole suite over the provisioned-plus-built workspace, every card
	// printed. The exit code follows the CRITICAL checks only — a scripted
	// hello screen is allowed not to ship MWA (that expectation is
	// non-critical), while build-finished (the ladder passed), the browser
	// wallet ban, and the secret ban stay red-to-exit, as in production.
	files, err := evals.Snapshot(wsDir)
	if err != nil {
		failed("evals-snapshot")
		return render(checks), true
	}
	model := aiClient.Capabilities().Model
	if model == "" {
		model = "headless-model"
	}
	sample := evals.Sample{Files: files, Verified: outcome.Verified, Rounds: outcome.Rounds}
	var cards []evals.Scorecard
	for _, ev := range evals.Suite() {
		cards = append(cards, evals.Grade(model, sample, ev))
	}
	critical := false
	for _, c := range cards {
		if !c.Passed() {
			critical = true
		}
	}
	return render(checks) + "\n\n" + evals.RenderScorecards(cards), critical
}

// resolveTemplateDir locates the shared template: TEMPLATE_DIR wins (the
// same variable the server reads), else the walk up from the working
// directory tries ./template (launched from backend/, what CI does) and
// ./backend/template (launched from the repo root) at each level — the
// first candidate with a package.json wins. "" means not found.
func resolveTemplateDir() string {
	if v := strings.TrimSpace(os.Getenv("TEMPLATE_DIR")); v != "" {
		return v
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for i := 0; i < 4; i++ {
		for _, cand := range []string{
			filepath.Join(dir, "template"),
			filepath.Join(dir, "backend", "template"),
		} {
			if _, err := os.Stat(filepath.Join(cand, "package.json")); err == nil {
				return cand
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// headlessPrompt defaults when the env var is unset.
func headlessPrompt() string {
	if p := os.Getenv("TALONSTUDIO_HEADLESS_PROMPT"); p != "" {
		return p
	}
	return "Add a hello world screen at app/hello.tsx"
}

// helloScreen is what the scripted model writes: a compile-clean React
// Native screen, so the REAL verification ladder (tsc, eslint, expo
// export) can pass over it. The write is also what makes the sweep run at
// all — a turn that changes nothing is never classified, and
// build-finished (critical) would stay red.
const helloScreen = `import { useState } from "react";
import { StyleSheet, Text, TouchableOpacity, View } from "react-native";

export default function HelloScreen() {
  const [count, setCount] = useState(0);
  return (
    <View style={styles.root}>
      <Text style={styles.title}>Hello from Talon</Text>
      <TouchableOpacity
        accessibilityRole="button"
        onPress={() => setCount(count + 1)}
      >
        <Text style={styles.button}>Tap me</Text>
      </TouchableOpacity>
      <Text style={styles.count}>Taps: {count}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1, alignItems: "center", justifyContent: "center" },
  title: { fontSize: 24, fontWeight: "700" },
  button: { fontSize: 18, marginTop: 16, color: "#2563eb" },
  count: { fontSize: 16, marginTop: 12 },
});
`

// scriptedProvider serves the deterministic two-response script: round 1
// writes the screen (the change the ladder verifies), round 2 finishes
// with text.
func scriptedProvider() *httptest.Server {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{
					{"type": "tool_use", "id": "h1", "name": "write_file",
						"input": map[string]any{"path": "app/hello.tsx", "content": helloScreen}},
				},
				"stop_reason": "tool_use",
				"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":     []map[string]any{{"type": "text", "text": "Headless build done."}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 8},
		})
	}))
}

func render(checks []string) string {
	var b strings.Builder
	b.WriteString("== Talon headless scorecard ==\n")
	for _, c := range checks {
		b.WriteString("  ")
		b.WriteString(c)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// t is the env-setting shim: headless runs outside go test, so Setenv is
// plain os.Setenv. The name keeps the test-looking call sites readable.
var t = envShim{}

type envShim struct{}

func (envShim) Setenv(k, v string) { _ = os.Setenv(k, v) }
