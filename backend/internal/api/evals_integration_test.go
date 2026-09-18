package api

// This is the live end of the evals harness. The evals package grades a build
// from its files alone and never imports the orchestrator; here we supply the
// missing half — a real BuildFunc that drives the fixed suite through the exact
// newBuildSession + RunAgentLoop path the HTTP handler uses — and print a
// per-model scorecard.
//
// Reusing the production wiring is the whole point: a runner that duplicated the
// tool registration or the RunOptions assembly would silently drift from what
// ships, and then the scorecard would grade a builder no user ever touches.
//
// It is opt-in. A real run needs a configured model (AI_MODEL et al.) and spends
// real tokens driving a full build per eval, so it has no place in `go test
// ./...`. Set RUN_EVALS=1 to run it. In CI it is skipped but still COMPILED, and
// that compile is what matters there: it proves this stays wired to the handler
// as the handler evolves.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/evals"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/store"
)

// TestEvalsLiveScorecard runs the fixed eval suite against the configured model
// and logs a deterministic scorecard. Skipped unless RUN_EVALS=1 and a model is
// configured. Give it room: a full build per eval is many model round-trips, so
// run with e.g. `RUN_EVALS=1 go test ./internal/api -run EvalsLive -v -timeout 60m`.
func TestEvalsLiveScorecard(t *testing.T) {
	if os.Getenv("RUN_EVALS") == "" {
		t.Skip("set RUN_EVALS=1 to run the live evals suite (needs a configured model and real tokens)")
	}

	aiClient, err := ai.New()
	if err != nil {
		t.Skipf("no model configured (%v); set AI_PROTOCOL / AI_BASE_URL / AI_API_KEY / AI_MODEL", err)
	}

	// The knowledge layer and store the real handler is built on. A temp DB keeps
	// the run self-contained; the template package.json is the pinned dependency
	// set every skill and rule is written against.
	knowledge, err := knowledge.Load("../../template/package.json")
	if err != nil {
		t.Fatalf("loading knowledge: %v", err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "evals.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	user, err := st.CreateUser("evals@local", "x")
	if err != nil {
		t.Fatalf("creating eval user: %v", err)
	}

	orch := orchestrator.New(aiClient, st, knowledge)
	h := NewPromptHandler(st, aiClient, t.TempDir(), "", orch, nil, knowledge, nil)

	templateRoot, err := filepath.Abs("../../template")
	if err != nil {
		t.Fatalf("resolving template path: %v", err)
	}

	// A generous per-eval ceiling so one stuck build is recorded as a failed
	// sample rather than hanging the whole run; the others still score.
	perEval := 20 * time.Minute
	if v := os.Getenv("EVALS_TIMEOUT"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil {
			perEval = d
		}
	}

	var promptID int64
	build := func(ctx context.Context, ev evals.Eval) (evals.Sample, error) {
		promptID++
		ws := t.TempDir()
		if err := stageTemplate(t, templateRoot, ws); err != nil {
			return evals.Sample{}, err
		}

		// A real project row so write_memory / write_adr have a valid target; an
		// FK error surfacing as a failed tool result would only add noise to the
		// build the model is being graded on.
		proj, err := st.CreateProject(user.ID, "eval-"+ev.ID, "", "eval-"+ev.ID,
			"com.evals."+sanitizePkg(ev.ID), "", "eval")
		if err != nil {
			return evals.Sample{}, err
		}

		sess := orch.CreateSession(promptID, proj.ID, ws)
		defer orch.RemoveSession(promptID)
		defer sess.Emitter.Close()
		// Drain events for the life of the session: the emitter blocks once its
		// buffer fills, and a build with verbose command output would stall.
		go func() {
			for {
				select {
				case <-sess.Emitter.Done():
					return
				case <-sess.Emitter.Events():
				}
			}
		}()

		bs := h.newBuildSession(ws, proj.ID, 0, "", nil)

		evalCtx, cancel := context.WithTimeout(ctx, perEval)
		defer cancel()

		outcome, runErr := orch.RunAgentLoop(evalCtx, sess, orchestrator.RunOptions{
			SystemPrompt: h.systemPrompt(),
			History:      []ai.Message{ai.UserText(ev.Prompt)},
			Tools:        bs.defs,
			Execute:      bs.executor(),
			ReadOnlyTool: bs.reg.IsReadOnly,
			FilesTouched: bs.fs.Touched,
			Exists:       bs.fs.Exists,
		})

		files, snapErr := evals.Snapshot(ws)
		if snapErr != nil {
			return evals.Sample{}, snapErr
		}
		sample := evals.Sample{Files: files}
		if outcome != nil {
			sample.Verified = outcome.Verified
			sample.Rounds = outcome.Rounds
		}
		return sample, runErr
	}

	model := aiClient.Capabilities().Model
	if model == "" {
		model = "configured-model"
	}

	cards := evals.Run(context.Background(), model, evals.Suite(), build)
	report := evals.RenderScorecards(cards)
	t.Logf("\n%s", report)

	// Optionally persist the report so it can be diffed across runs or archived
	// against a model id, the comparison the whole harness exists to enable.
	if out := os.Getenv("EVALS_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(report), 0o644); err != nil {
			t.Errorf("writing report to %s: %v", out, err)
		}
	}
}

// stageTemplate copies the pinned template into a fresh workspace so a build has
// real code to edit, and symlinks the template's node_modules in so the
// typecheck and lint gates actually execute. Without the dependencies those
// gates fail open and Verified would mean nothing. The native and VCS trees are
// skipped: an eval never builds an APK, and copying them would only be slow.
func stageTemplate(t *testing.T, src, dst string) error {
	t.Helper()
	skip := map[string]bool{
		"node_modules": true, ".git": true, ".expo": true,
		"android": true, "ios": true, "dist": true, "build": true,
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		return err
	}

	nm := filepath.Join(src, "node_modules")
	if _, err := os.Stat(nm); err == nil {
		if err := os.Symlink(nm, filepath.Join(dst, "node_modules")); err != nil {
			return err
		}
	}
	return nil
}

// sanitizePkg turns an eval id into a valid Android package segment: no dashes,
// letters only where it matters. Eval ids like "balance-viewer" would otherwise
// be rejected as a package name.
func sanitizePkg(id string) string {
	var b []rune
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		}
	}
	if len(b) == 0 {
		return "app"
	}
	return string(b)
}
