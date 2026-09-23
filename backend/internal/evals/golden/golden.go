// Package golden is W13's recorded-provider replay harness — the Cursor
// law: agent behavior becomes a number (and a diff) that gates changes.
//
// A Task is a scripted prompt: fixture history, seeded workspace, the
// production system prompt (ai.SystemPromptBuild / ai.PlanSystemPrompt),
// and a recorded script of provider responses. Replaying it drives the
// REAL orchestrator loop — real registry, real guard stage, real hooks
// seam, real FS — against a local scripted provider, free and offline.
//
// One replay yields a Transcript: the invariants of what happened
// (outcome kind, rounds, tool sequence, files touched) plus the sha256
// digest of every request's (system, messages, tools) triple as the
// provider saw it. The committed golden pins those — deliberately NOT
// the prose: criteria are expressed over invariants, so a transcript
// diff in a PR is reviewable behavior, and the three channels the plan
// names each do their own job:
//
//   - the log: tool sequence + rounds + outcome, compared to Expect;
//   - the workspace diff: files touched and required/forbidden content
//     markers over the final snapshot;
//   - the grade scorecard: evals.Grade over the produced files, for the
//     tasks that produce code (plan-existing-feature writes nothing by
//     design, so Grade's build-finished check would honestly fail it —
//     that task's criteria are the log and the diff).
//
// Digests are what make prompt/tool/guardrail edits require golden
// review: change ai.SystemPromptBuild, a tool schema, a guard refusal,
// or the loop's request shape and the digests stop matching — the suite
// goes red until the goldens are re-recorded (TALONSTUDIO_UPDATE_GOLDEN=1)
// and the resulting transcript diff is reviewed in the PR.
package golden

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/evals"
	"github.com/luckyisrael/talon/backend/internal/events"
	"github.com/luckyisrael/talon/backend/internal/hooks"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/tools"
)

// Turn is one recorded provider response — exactly what the fake
// provider said when the loop asked. Tool turns name the call and carry
// its arguments; Finish is the terminal prose turn.
type Turn struct {
	Tool   string          `json:"tool,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Finish string          `json:"finish,omitempty"`
}

func (t Turn) validate(taskID string) error {
	switch {
	case t.Tool != "" && t.Finish != "":
		return fmt.Errorf("task %s: turn sets both tool and finish", taskID)
	case t.Tool == "" && t.Finish == "":
		return fmt.Errorf("task %s: turn sets neither tool nor finish", taskID)
	case t.Tool != "" && len(t.Args) > 0 && !json.Valid(t.Args):
		return fmt.Errorf("task %s: turn for %s has invalid args", taskID, t.Tool)
	}
	return nil
}

// Msg is one fixture history turn.
type Msg struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// EvalCheck is the grade channel: pass criteria expressed over the
// produced files through the same grader the build evals use.
type EvalCheck struct {
	ID                  string   `json:"id"`
	Prompt              string   `json:"prompt,omitempty"`
	UsesMWA             bool     `json:"uses_mwa,omitempty"`
	ImportsAnyOf        []string `json:"imports_any_of,omitempty"`
	CreatesFileMatching []string `json:"creates_file_matching,omitempty"`
}

func (c EvalCheck) eval() evals.Eval {
	return evals.Eval{
		ID:     c.ID,
		Prompt: c.Prompt,
		Expect: evals.Expectations{
			UsesMWA:             c.UsesMWA,
			ImportsAnyOf:        c.ImportsAnyOf,
			CreatesFileMatching: c.CreatesFileMatching,
		},
	}
}

// Expect is the pass criteria: outcome kind, a rounds budget, the tool
// sequence, files touched, and content markers over the final workspace.
// A content marker prefixed with "!" must NOT appear (forbidden), which
// is how a steer task proves it dropped the thing it was steered away
// from.
type Expect struct {
	Outcome      string              `json:"outcome"`
	MaxRounds    int                 `json:"max_rounds,omitempty"`
	Tools        []string            `json:"tools"`
	FilesTouched []string            `json:"files_touched"`
	Content      map[string][]string `json:"content,omitempty"`
	Eval         *EvalCheck          `json:"eval,omitempty"`
}

// Task is one golden task: fixture inputs, the recorded script, and the
// criteria a replay must satisfy.
type Task struct {
	ID      string            `json:"id"`
	Mode    string            `json:"mode"`   // build | plan
	System  string            `json:"system"` // build | plan → production prompt
	History []Msg             `json:"history"`
	Seed    map[string]string `json:"seed"`
	Script  []Turn            `json:"script"`
	Expect  Expect            `json:"expect"`
}

// SystemPrompt resolves the task's production system prompt. The two
// selectors are the two prompts production actually sends — pinning them
// is what couples this suite to prompt edits.
func (t Task) SystemPrompt() (string, error) {
	switch t.System {
	case "build":
		return ai.SystemPromptBuild, nil
	case "plan":
		return ai.PlanSystemPrompt(nil), nil
	default:
		return "", fmt.Errorf("task %s: unknown system %q (want build|plan)", t.ID, t.System)
	}
}

// Validate rejects a task that could never replay faithfully.
func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("task needs an id")
	}
	if t.Mode != "build" && t.Mode != "plan" {
		return fmt.Errorf("task %s: mode must be build or plan, got %q", t.ID, t.Mode)
	}
	if _, err := t.SystemPrompt(); err != nil {
		return err
	}
	if len(t.History) == 0 {
		return fmt.Errorf("task %s: empty history", t.ID)
	}
	for i, m := range t.History {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("task %s: history[%d] role %q", t.ID, i, m.Role)
		}
	}
	if len(t.Script) == 0 {
		return fmt.Errorf("task %s: empty script", t.ID)
	}
	for i, turn := range t.Script {
		if err := turn.validate(t.ID); err != nil {
			return fmt.Errorf("script[%d]: %w", i, err)
		}
	}
	if last := t.Script[len(t.Script)-1]; last.Finish == "" {
		return fmt.Errorf("task %s: script must end with a finish turn (otherwise every replay overruns the recording)", t.ID)
	}
	if t.Expect.Outcome == "" {
		return fmt.Errorf("task %s: expect.outcome is the log criterion — it must be set", t.ID)
	}
	for path := range t.Seed {
		if path == "" || strings.Contains(path, "..") || filepath.IsAbs(path) {
			return fmt.Errorf("task %s: seed path %q must be workspace-relative", t.ID, path)
		}
	}
	return nil
}

// Transcript is the golden: every invariant of one replay, and nothing
// about prose. Field order is fixed and slices keep execution order, so
// the JSON is byte-stable and diffs cleanly in review.
type Transcript struct {
	Task         string   `json:"task"`
	Outcome      string   `json:"outcome"`
	Rounds       int      `json:"rounds"`
	Tools        []string `json:"tools"`
	FilesTouched []string `json:"files_touched"`
	// Digests is the recording proper: sha256 over each request's raw
	// body (the (system, messages, tools) triple) with the temp workspace
	// root normalized out, in request order.
	Digests []string `json:"request_digests"`
}

// Case is one replay: the task, the system prompt to drive it with
// (empty = the task's production prompt), and the recorded golden to
// verify request digests against (nil = record mode).
type Case struct {
	Task         Task
	SystemPrompt string
	Expect       *Transcript
}

// goldenVerifyConfig seeds an explicit empty ladder: the fixture has no
// toolchain, and W6's law is that a gate which never ran is never
// claimed. All stages off = zero skips, strict classifies the sweep as
// verified (classifyVerification: nothing skipped), and no gate ever
// reaches a checker that does not exist. A task that ships its own
// .talon/config.json in Seed keeps it instead.
const goldenVerifyConfig = `{"verify_policy":"strict","verify_gates":{"lsp":"off","typecheck":"off","lint":"off","bundle":"off","gradle":"off","boot":"off","vision":"off","interact":"off"}}`

func seedWorkspace(t *testing.T, task Task) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(task.Seed)+1)
	for path := range task.Seed {
		paths = append(paths, path)
	}
	// Sorted so the seeding itself adds no map-order nondeterminism.
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[j] < paths[i] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	for _, path := range paths {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
		if err := os.WriteFile(abs, []byte(task.Seed[path]), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	if _, ok := task.Seed[".talon/config.json"]; !ok {
		if err := os.MkdirAll(filepath.Join(root, ".talon"), 0o755); err != nil {
			t.Fatalf("seed .talon: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, ".talon", "config.json"), []byte(goldenVerifyConfig), 0o644); err != nil {
			t.Fatalf("seed verify config: %v", err)
		}
	}
	return root
}

// provider is the recorded fake: it answers request N with script turn
// N and digests every request as it arrives. A request beyond the
// script, or a digest that no longer matches the golden, is recorded as
// drift and fails the replay loudly.
type provider struct {
	mu      sync.Mutex
	script  []Turn
	expect  []string // golden digests; nil = record mode
	root    string
	digests []string
	drift   []string
}

func (p *provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The temp workspace root appears in context sections; normalize it
	// out so digests are stable across runs and machines.
	norm := strings.ReplaceAll(string(raw), p.root, "<workspace>")
	sum := sha256.Sum256([]byte(norm))
	digest := fmt.Sprintf("%x", sum[:])

	p.mu.Lock()
	idx := len(p.digests)
	p.digests = append(p.digests, digest)
	var turn Turn
	switch {
	case idx >= len(p.script):
		p.drift = append(p.drift, fmt.Sprintf(
			"request %d: beyond the recorded script (%d turn(s))", idx+1, len(p.script)))
		turn = Turn{Finish: "replay script exhausted"}
	case p.expect != nil && idx >= len(p.expect):
		p.drift = append(p.drift, fmt.Sprintf(
			"request %d: the golden recorded only %d request(s)", idx+1, len(p.expect)))
		turn = p.script[idx]
	case p.expect != nil && p.expect[idx] != digest:
		p.drift = append(p.drift, fmt.Sprintf(
			"request %d: (system, messages, tools) drifted — recorded %s, got %s",
			idx+1, p.expect[idx], digest))
		turn = p.script[idx]
	default:
		turn = p.script[idx]
	}
	p.mu.Unlock()

	writeResponse(w, turn, idx)
}

// writeResponse renders one OpenAI-protocol completion: a tool call or
// the terminal prose turn. Tool ids are request-unique so the growing
// conversation never reuses an id.
func writeResponse(w http.ResponseWriter, turn Turn, idx int) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
	}
	if turn.Tool != "" {
		args := turn.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		resp["choices"] = []map[string]any{{
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"content": "",
				"tool_calls": []map[string]any{{
					"id":   fmt.Sprintf("call-%d", idx+1),
					"type": "function",
					"function": map[string]any{
						"name":      turn.Tool,
						"arguments": string(args),
					},
				}},
			},
		}}
	} else {
		resp["choices"] = []map[string]any{{
			"finish_reason": "stop",
			"message":       map[string]any{"content": turn.Finish},
		}}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// Replay runs one task through the real orchestrator loop against the
// recorded script. It returns the invariant transcript and the final
// workspace snapshot. A non-nil error means the replay was unfaithful
// (script overrun, digest drift) or the loop itself failed.
func Replay(t *testing.T, c Case) (Transcript, map[string]string, error) {
	t.Helper()
	sys := c.SystemPrompt
	if sys == "" {
		var err error
		if sys, err = c.Task.SystemPrompt(); err != nil {
			return Transcript{}, nil, err
		}
	}
	root := seedWorkspace(t, c.Task)

	prov := &provider{script: c.Task.Script, root: root}
	if c.Expect != nil {
		prov.expect = c.Expect.Digests
	}
	srv := httptest.NewServer(prov)
	t.Cleanup(srv.Close)

	t.Setenv("AI_PROTOCOL", "openai")
	t.Setenv("AI_BASE_URL", srv.URL)
	t.Setenv("AI_API_KEY", "golden-key")
	t.Setenv("AI_MODEL", "golden-model")
	client, err := ai.New()
	if err != nil {
		return Transcript{}, nil, fmt.Errorf("ai.New: %w", err)
	}

	fs := tools.NewFS(root)
	reg := tools.NewRegistry()
	reg.Register(&tools.ReadFile{FS: fs})
	reg.Register(&tools.WriteFile{FS: fs})
	reg.Register(&tools.EditFile{FS: fs})
	reg.Register(&tools.ListFiles{FS: fs})

	history := make([]ai.Message, 0, len(c.Task.History))
	for _, m := range c.Task.History {
		switch m.Role {
		case "user":
			history = append(history, ai.UserText(m.Content))
		case "assistant":
			history = append(history, ai.AssistantText(m.Content))
		}
	}

	o := orchestrator.New(client, nil, nil)
	sess := orchestrator.NewSession(0, 0, root, events.NewSessionEmitter(0, 64))
	// Drain the emitter so a full buffer never blocks the loop, and close
	// it when the replay ends. The drain is deliberately not joined:
	// Emitter.Close marks the stream done without closing the events
	// channel (the production child runner drains the same way, fire and
	// forget), so waiting on the drain would deadlock the replay.
	defer sess.Emitter.Close()
	go func() {
		for range sess.Emitter.Events() {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	outcome, runErr := o.RunAgentLoop(ctx, sess, orchestrator.RunOptions{
		SystemPrompt: sys,
		Mode:         c.Task.Mode,
		History:      history,
		Tools:        toolDefs(reg),
		Execute: func(ctx context.Context, name string, input json.RawMessage) (string, bool) {
			tr, err := reg.Execute(ctx, name, input)
			if err != nil {
				return err.Error(), true
			}
			return tr.Content, tr.IsError
		},
		ReadOnlyTool: func(name string) bool {
			tool, ok := reg.Get(name)
			if !ok {
				return false
			}
			ro, ok := tool.(tools.ReadOnly)
			return ok && ro.ReadOnly()
		},
		FilesTouched: fs.Touched,
		Exists:       fs.Exists,
		Hooks:        hooks.Load(root),
	})

	files, snapErr := evals.Snapshot(root)
	if snapErr != nil {
		return Transcript{}, nil, snapErr
	}
	digests, drift := prov.snapshot()
	tr := Transcript{
		Task:         c.Task.ID,
		Outcome:      classify(outcome, runErr),
		Rounds:       rounds(outcome),
		Tools:        toolSequence(outcome),
		FilesTouched: touched(outcome),
		Digests:      nonNil(digests),
	}
	if len(drift) > 0 {
		return tr, files, fmt.Errorf("replay drifted from the recording: %s", strings.Join(drift, "; "))
	}
	return tr, files, runErr
}

func (p *provider) snapshot() (digests, drift []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.digests...), append([]string(nil), p.drift...)
}

func toolDefs(reg *tools.Registry) []ai.ToolDefinition {
	list := reg.List()
	defs := make([]ai.ToolDefinition, 0, len(list))
	for _, d := range list {
		defs = append(defs, ai.ToolDefinition{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
	}
	return defs
}

// classify names the outcome kind — the invariant the criteria key on.
func classify(o *orchestrator.BuildOutcome, runErr error) string {
	if runErr != nil || o == nil {
		return "error"
	}
	switch {
	case o.Stalled:
		return "stalled"
	case o.Paused:
		return "paused"
	case o.Exhausted:
		return "exhausted"
	case o.BudgetStop:
		return "budget"
	case o.HookStop:
		return "hook"
	case o.Verified:
		return "verified"
	case len(o.FilesTouched) == 0:
		return "done"
	default:
		// Files changed but the policy refused the claim (or the sweep
		// never ran to a verified state) — never reported as done.
		return "unverified"
	}
}

func rounds(o *orchestrator.BuildOutcome) int {
	if o == nil {
		return 0
	}
	return o.Rounds
}

// toolSequence is the assistant's tool calls in order — the log
// channel's core invariant (tool order classes per the plan's risk
// note: names in sequence, never prose).
func toolSequence(o *orchestrator.BuildOutcome) []string {
	out := []string{}
	if o == nil {
		return out
	}
	for _, m := range o.Messages {
		for _, b := range m.Blocks {
			if b.Type == "tool_use" {
				out = append(out, b.Name)
			}
		}
	}
	return out
}

func touched(o *orchestrator.BuildOutcome) []string {
	if o == nil || len(o.FilesTouched) == 0 {
		return []string{}
	}
	return append([]string(nil), o.FilesTouched...)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Diffs reports invariant differences between a recorded golden and a
// fresh replay — empty means identical. Each line names the field, so a
// red suite says exactly which invariant moved.
func Diffs(want, got Transcript) []string {
	var out []string
	if want.Task != got.Task {
		out = append(out, fmt.Sprintf("task: want %q got %q", want.Task, got.Task))
	}
	if want.Outcome != got.Outcome {
		out = append(out, fmt.Sprintf("outcome: want %q got %q", want.Outcome, got.Outcome))
	}
	if want.Rounds != got.Rounds {
		out = append(out, fmt.Sprintf("rounds: want %d got %d", want.Rounds, got.Rounds))
	}
	out = append(out, sliceDiffs("tools", want.Tools, got.Tools)...)
	out = append(out, sliceDiffs("files_touched", want.FilesTouched, got.FilesTouched)...)
	out = append(out, sliceDigestDiffs(want.Digests, got.Digests)...)
	return out
}

func sliceDiffs(field string, want, got []string) []string {
	var out []string
	if len(want) != len(got) {
		out = append(out, fmt.Sprintf("%s: want %v got %v", field, want, got))
		return out
	}
	for i := range want {
		if want[i] != got[i] {
			out = append(out, fmt.Sprintf("%s[%d]: want %q got %q", field, i, want[i], got[i]))
		}
	}
	return out
}

func sliceDigestDiffs(want, got []string) []string {
	if len(want) == len(got) {
		for i := range want {
			if want[i] != got[i] {
				return []string{fmt.Sprintf(
					"request_digests[%d]: (system, messages, tools) drifted — recorded %s, got %s (re-record with TALONSTUDIO_UPDATE_GOLDEN=1 and review the diff)",
					i, want[i], got[i])}
			}
		}
		return nil
	}
	return []string{fmt.Sprintf(
		"request count: recorded %d request(s), replay sent %d (the loop's request shape changed, or the script over/under-ran)",
		len(want), len(got))}
}

// CheckCriteria returns every failed pass criterion for one replay: the
// log channel (outcome/rounds/tools/files), the diff channel (content
// markers over the final snapshot), and the grade channel (evals.Grade
// for tasks that produce code).
func CheckCriteria(task Task, tr Transcript, runErr error, files map[string]string) []string {
	var fails []string
	if runErr != nil {
		fails = append(fails, "replay error: "+runErr.Error())
	}
	e := task.Expect
	if e.Outcome != "" && tr.Outcome != e.Outcome {
		fails = append(fails, fmt.Sprintf("outcome: want %q got %q", e.Outcome, tr.Outcome))
	}
	if e.MaxRounds > 0 && tr.Rounds > e.MaxRounds {
		fails = append(fails, fmt.Sprintf("rounds-to-green: %d over the %d-round budget", tr.Rounds, e.MaxRounds))
	}
	if e.Tools != nil {
		fails = append(fails, sliceDiffs("tool sequence", e.Tools, tr.Tools)...)
	}
	if e.FilesTouched != nil {
		fails = append(fails, sliceDiffs("files touched", e.FilesTouched, tr.FilesTouched)...)
	}
	paths := make([]string, 0, len(e.Content))
	for path := range e.Content {
		paths = append(paths, path)
	}
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[j] < paths[i] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	for _, path := range paths {
		body, ok := files[path]
		if !ok {
			fails = append(fails, fmt.Sprintf("content: %s missing from the workspace", path))
			continue
		}
		lower := strings.ToLower(body)
		for _, marker := range e.Content[path] {
			if strings.HasPrefix(marker, "!") {
				if strings.Contains(lower, strings.ToLower(marker[1:])) {
					fails = append(fails, fmt.Sprintf("content: %s must not contain %q", path, marker[1:]))
				}
				continue
			}
			if !strings.Contains(lower, strings.ToLower(marker)) {
				fails = append(fails, fmt.Sprintf("content: %s missing %q", path, marker))
			}
		}
	}
	if e.Eval != nil {
		card := evals.Grade("golden", evals.Sample{
			Files:    files,
			Verified: tr.Outcome == "verified",
			Rounds:   tr.Rounds,
		}, e.Eval.eval())
		for _, f := range card.Failures() {
			mark := ""
			if f.Critical {
				mark = " (critical)"
			}
			fails = append(fails, fmt.Sprintf("grade[%s]%s: %s", f.Name, mark, f.Detail))
		}
	}
	return fails
}

// UpdateEnabled is the record-mode switch: truthy set 1|true|on|v1 (the
// repo's flag convention). CI never sets it, so the tree guard proves
// the suite never rewrites its own goldens.
func UpdateEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TALONSTUDIO_UPDATE_GOLDEN"))) {
	case "1", "true", "on", "v1":
		return true
	}
	return false
}

// LoadTask reads and validates one task fixture.
func LoadTask(t *testing.T, path string) Task {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read task %s: %v", path, err)
	}
	var task Task
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatalf("parse task %s: %v", path, err)
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("task %s: %v", path, err)
	}
	return task
}

// LoadGolden reads a recorded transcript; nil when none exists yet
// (first record run).
func LoadGolden(t *testing.T, path string) *Transcript {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read golden %s: %v", path, err)
	}
	var tr Transcript
	if err := json.Unmarshal(raw, &tr); err != nil {
		t.Fatalf("parse golden %s: %v", path, err)
	}
	return &tr
}

// SaveGolden records a transcript as pretty JSON for review diffs.
func SaveGolden(t *testing.T, path string, tr Transcript) {
	t.Helper()
	raw, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		t.Fatalf("encode golden %s: %v", path, err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
}
