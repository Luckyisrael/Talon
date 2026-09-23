package golden

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The eval gate's three clauses live here (PLAN_WORLDCLASS W13):
//
//  1. replay determinism — the same fixture replayed twice yields the
//     same transcript, byte for byte;
//  2. budget — the whole golden set replays in under three minutes
//     (scripted + offline: a slow suite means real work crept in);
//  3. mutation — a deliberate regression (prompt or behavior) flips a
//     task red through the digest channel and the criteria channels.
//
// TestGoldenSuite also carries the golden-diff review loop: red on any
// invariant change until TALONSTUDIO_UPDATE_GOLDEN=1 re-records and the
// resulting transcript diff is reviewed in the PR.

// planTasks is the six tasks W13.2 names. Deleting one to go green is
// caught here, not in review.
var planTasks = []string{
	"scaffold-weather-app",
	"plan-existing-feature",
	"fix-broken-import",
	"wallet-connect-flow",
	"steer-mid-build",
	"resume-after-crash",
}

const tasksDir = "tasks"

// suiteFiles lists the task fixtures (goldens excluded), sorted.
func suiteFiles(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(tasksDir, "*.json"))
	if err != nil {
		t.Fatalf("glob tasks: %v", err)
	}
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasSuffix(m, ".golden.json") {
			continue
		}
		files = append(files, m)
	}
	if len(files) == 0 {
		t.Fatalf("no task fixtures under %s", tasksDir)
	}
	return files // Glob returns sorted order
}

func goldenPath(taskFile string) string {
	return strings.TrimSuffix(taskFile, ".json") + ".golden.json"
}

// TestGoldenSuite replays every task against its recorded golden: the
// invariants must match (digests red first on prompt/tool/guardrail
// drift) and all three criterion channels must pass. In record mode the
// goldens are rewritten instead of compared.
//
// Note on plan-existing-feature: it writes nothing by design, so its
// criteria are the log and the workspace diff only — Grade's
// build-finished check would honestly fail a build that produced no
// code, and reporting green on that would lie.
func TestGoldenSuite(t *testing.T) {
	files := suiteFiles(t)
	for _, id := range planTasks {
		// LoadTask fatals if the fixture is missing or invalid.
		LoadTask(t, filepath.Join(tasksDir, id+".json"))
	}
	if len(files) < len(planTasks) {
		t.Fatalf("golden suite incomplete: %d task(s), want at least the %d plan tasks", len(files), len(planTasks))
	}

	start := time.Now()
	for _, file := range files {
		task := LoadTask(t, file)
		gpath := goldenPath(file)
		t.Run(task.ID, func(t *testing.T) {
			record := UpdateEnabled()
			var golden *Transcript
			if !record {
				golden = LoadGolden(t, gpath)
				if golden == nil {
					t.Fatalf("no golden recorded for %s — run with TALONSTUDIO_UPDATE_GOLDEN=1 and review the diff", task.ID)
				}
			}
			tr, gotFiles, err := Replay(t, Case{Task: task, Expect: golden})
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if record {
				SaveGolden(t, gpath, tr)
			} else if diffs := Diffs(*golden, tr); len(diffs) > 0 {
				for _, d := range diffs {
					t.Errorf("golden drift: %s", d)
				}
			}
			for _, f := range CheckCriteria(task, tr, nil, gotFiles) {
				t.Errorf("criterion failed: %s", f)
			}
		})
	}
	if elapsed := time.Since(start); elapsed > 3*time.Minute {
		t.Errorf("golden set took %v, over the 3-minute gate — scripted replays must stay free and offline", elapsed)
	}
}

// TestGoldenReplayDeterminism is gate clause 1: same fixture in,
// identical transcript out — twice. Divergence means nondeterminism
// leaked into the loop (time, map order, temp paths).
func TestGoldenReplayDeterminism(t *testing.T) {
	for _, file := range suiteFiles(t) {
		task := LoadTask(t, file)
		t.Run(task.ID, func(t *testing.T) {
			first, _, err := Replay(t, Case{Task: task})
			if err != nil {
				t.Fatalf("first replay: %v", err)
			}
			second, _, err := Replay(t, Case{Task: task})
			if err != nil {
				t.Fatalf("second replay: %v", err)
			}
			if diffs := Diffs(first, second); len(diffs) > 0 {
				for _, d := range diffs {
					t.Errorf("replay is not deterministic: %s", d)
				}
			}
			a, merr := json.Marshal(first)
			if merr != nil {
				t.Fatal(merr)
			}
			b, merr := json.Marshal(second)
			if merr != nil {
				t.Fatal(merr)
			}
			if string(a) != string(b) {
				t.Errorf("transcripts differ at byte level:\n%s\n%s", a, b)
			}
		})
	}
}

// TestGoldenMutationDetection is gate clause 3: a deliberate regression
// must flip a task red. Two regressions, the two the plan names — a
// prompt edit, caught by the (system, messages, tools) digest channel,
// and a behavior edit, caught by the workspace diff, the log criteria,
// and the golden transcript.
func TestGoldenMutationDetection(t *testing.T) {
	file := filepath.Join(tasksDir, "scaffold-weather-app.json")
	task := LoadTask(t, file)
	golden := LoadGolden(t, goldenPath(file))
	if golden == nil {
		t.Fatalf("no golden for %s — record with TALONSTUDIO_UPDATE_GOLDEN=1 first", task.ID)
	}

	t.Run("prompt regression flips red", func(t *testing.T) {
		prompt, err := task.SystemPrompt()
		if err != nil {
			t.Fatal(err)
		}
		tr, _, err := Replay(t, Case{
			Task:         task,
			SystemPrompt: prompt + "\n\n(steer: prefer inline styles everywhere)",
			Expect:       golden,
		})
		if err == nil {
			if diffs := Diffs(*golden, tr); len(diffs) == 0 {
				t.Fatal("a mutated prompt replayed to an identical transcript — the digest channel is not coupled to the system prompt")
			}
			return
		}
		if !strings.Contains(err.Error(), "drifted") {
			t.Fatalf("want digest drift for a mutated prompt, got: %v", err)
		}
	})

	t.Run("behavior regression flips red", func(t *testing.T) {
		if len(task.Script) < 2 || task.Script[1].Tool != "write_file" {
			t.Fatalf("fixture changed: script[1] is no longer the first write_file (got %+v)", task.Script[1])
		}
		mutated := task
		mutated.Script = append([]Turn(nil), task.Script...)
		mutated.Script[1].Args = json.RawMessage(
			`{"path": "app/wrong.tsx", "content": "export default function Wrong() {\n  return null;\n}\n"}`)

		// No Expect: this replay must isolate the criteria channels —
		// the loop itself succeeds, but the original task's pass
		// criteria and the golden transcript both go red.
		tr, gotFiles, err := Replay(t, Case{Task: mutated})
		if err != nil {
			t.Fatalf("mutated replay (digests off) should complete the loop, got: %v", err)
		}
		fails := CheckCriteria(task, tr, nil, gotFiles)
		if len(fails) == 0 {
			t.Fatal("criteria passed despite the build writing the wrong file — the workspace-diff channel is dead")
		}
		if diffs := Diffs(*golden, tr); len(diffs) == 0 {
			t.Fatal("golden transcript unchanged by a behavior regression — the log/digest channel is dead")
		}
	})
}
