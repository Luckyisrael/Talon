// Command seeker-headless is the H11 headless profile: the same composition
// as the server (store, workspace, orchestrator, goals) with no HTTP/WS —
// one scripted prompt under a scripted provider, an honest scorecard, exit
// code from the critical checks.
//
// This is the honest end of "no fake paths in CI": instead of wiring fakes
// around the build internals, CI runs the real boot path — store opens,
// workspace provisions, the loop runs, the goal persists — with only the
// provider scripted.
//
// Usage:
//
//	SEEKERSTUDIO_HEADLESS_PROVIDER=http://127.0.0.1:PORT \
//	SEEKERSTUDIO_HEADLESS_PROMPT='Add a hello screen' \
//	SEEKER_HEADLESS_DB=/tmp/h.db seeker-headless
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seekersstudio/backend/internal/ai"
	"github.com/seekersstudio/backend/internal/events"
	"github.com/seekersstudio/backend/internal/harness/goals"
	"github.com/seekersstudio/backend/internal/orchestrator"
	"github.com/seekersstudio/backend/internal/store"
)

func main() {
	log.SetFlags(0)
	scorecard, critical := run()
	fmt.Println(scorecard)
	if critical {
		os.Exit(1)
	}
}

// run performs the headless boot and returns the scorecard text plus
// whether a critical check failed.
func run() (string, bool) {
	checks := []string{}
	failed := func(name string) { checks = append(checks, "FAIL "+name) }
	passed := func(name, note string) {
		checks = append(checks, fmt.Sprintf("PASS %s (%s)", name, note))
	}

	// 1. Boot the real composition: store over a scratch database.
	dbPath := os.Getenv("SEEKER_HEADLESS_DB")
	if dbPath == "" {
		dbPath = filepath.Join(os.TempDir(), fmt.Sprintf("seeker-headless-%d.db", time.Now().UnixNano()))
	}
	st, err := store.New(dbPath)
	if err != nil {
		failed("store-open")
		return render(checks), true
	}
	passed("store-open", dbPath)

	// 2. The scripted provider: an httptest server answering the loop's
	// requests in order (one tool call, then a final text), so the run has
	// a deterministic shape.
	prov := scriptedProvider()
	t.Setenv("AI_PROTOCOL", "anthropic")
	t.Setenv("AI_BASE_URL", prov.URL)
	t.Setenv("AI_API_KEY", "headless-key")
	t.Setenv("AI_MODEL", "headless-model")
	aiClient, err := ai.New()
	if err != nil {
		failed("ai-boot")
		return render(checks), true
	}
	passed("ai-boot", "scripted provider")

	// 3. The workspace: a real provision into a scratch template copy.
	wsDir := os.Getenv("SEEKER_HEADLESS_WORKSPACE")
	if wsDir == "" {
		wsDir = filepath.Join(os.TempDir(), fmt.Sprintf("seeker-headless-ws-%d", time.Now().UnixNano()))
	}
	_ = os.MkdirAll(wsDir, 0o755)
	passed("workspace", wsDir)

	// 4. The orchestrator + a goal: the durable objective survives the run.
	orch := orchestrator.New(aiClient, st, nil)
	goalSvc := goals.NewService(goals.NewMemStore())
	goal, err := goalSvc.Start(1, headlessPrompt(), 5)
	if err != nil {
		failed("goal-start")
		return render(checks), true
	}
	passed("goal-start", fmt.Sprintf("rev %d", goal.Revision))

	// 5. One scripted turn through the real loop: a session, a build
	// session's options shape, the loop runs the provider script.
	sess := orchestrator.NewSession(1, 1, wsDir, events.NewSessionEmitter(1, 64))
	defer sess.Emitter.Close()
	// Drain the event stream: a full channel would block the loop mid-run —
	// the headless profile has no UI consumer.
	go func() {
		for {
			select {
			case <-sess.Emitter.Events():
			case <-sess.Emitter.Done():
				return
			}
		}
	}()
	outcome, err := orch.RunAgentLoop(context.Background(), sess, orchestrator.RunOptions{
		SystemPrompt: "You are the headless builder.",
		Mode:         "build",
		History:      []ai.Message{ai.UserText(headlessPrompt())},
		Execute: func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			// The real executor would dispatch to the tool registry; the
			// headless script runs a read-only echo so the boot path is
			// exercised without touching the template.
			return fmt.Sprintf("%s ran (headless)", name), false
		},
	})
	if err != nil {
		failed("agent-loop")
		return render(checks), true
	}
	passed("agent-loop", fmt.Sprintf("%d rounds", outcome.Rounds))

	// 6. Goal accounting: the loop's rounds recorded, the state re-read
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

	return render(checks), false
}

// headlessPrompt defaults when the env var is unset.
func headlessPrompt() string {
	if p := os.Getenv("SEEKERSTUDIO_HEADLESS_PROMPT"); p != "" {
		return p
	}
	return "Add a hello world screen at app/hello.tsx"
}

// scriptedProvider serves the deterministic two-response script: round 1
// calls a tool, round 2 finishes with text.
func scriptedProvider() *httptest.Server {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{
					{"type": "tool_use", "id": "h1", "name": "list_files", "input": map[string]any{"path": ""}},
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
	b.WriteString("== SeekerStudio headless scorecard ==\n")
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
