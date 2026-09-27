package orchestrator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
)

// L12 checklist credit (ENGINEERING_STANDARDS §6). Failure modes written
// first (TEST_RULES §3):
//
//  1. FM-L12a — a passing run_checks mid-turn credits nothing: the mark
//     happens only at the turn's final verify, which a cancelled or
//     failed turn never reaches (Run-2: build cancelled at round 47 with
//     two route files on disk and a green run_checks in the same turn →
//     0/18 marked).
//  2. FM-L12b — a file written in the SAME batch as the check must not
//     claim compile truth from it: the touched set is snapshotted before
//     dispatch, so only writes from earlier batches are credited. The
//     next passing check or the final gate covers the same-batch file.
//  3. Honesty invariant — a failing run_checks credits nothing; the
//     audit runs with the mark so a completed screen whose route file is
//     gone flips back instead of staying claimed.
func TestPassingRunChecksMarksScreensWrittenBeforeIt(t *testing.T) {
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	sess.AddTask("plan:screen:send", "Screen: Send")
	opts := RunOptions{
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "run_checks" {
				return `{"passed":true,"summary":"no type errors; no lint errors"}`, false
			}
			return "written", false
		},
		FilesTouched: func() []string { return []string{"app/send.tsx"} },
		Exists:       func(path string) bool { return path == "app/send.tsx" },
	}

	o.executeTools(context.Background(), sess, []ai.ToolUseBlock{{
		ID: "c1", Name: "run_checks", Input: json.RawMessage(`{}`),
	}}, opts)

	if got := taskStatus(sess, "plan:screen:send"); got != "completed" {
		t.Fatalf("FM-L12a: a passing run_checks must credit screens written before it "+
			"and not wait for the turn's final gate (a cancelled turn never gets one) — status %q", got)
	}
}

func TestFailingRunChecksMarksNothing(t *testing.T) {
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	sess.AddTask("plan:screen:send", "Screen: Send")
	opts := RunOptions{
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "run_checks" {
				return `{"passed":false,"summary":"2 type errors"}`, false
			}
			return "written", false
		},
		FilesTouched: func() []string { return []string{"app/send.tsx"} },
		Exists:       func(path string) bool { return path == "app/send.tsx" },
	}

	o.executeTools(context.Background(), sess, []ai.ToolUseBlock{{
		ID: "c1", Name: "run_checks", Input: json.RawMessage(`{}`),
	}}, opts)

	if got := taskStatus(sess, "plan:screen:send"); got == "completed" {
		t.Fatalf("a failing run_checks must credit nothing — the checklist claims "+
			"compile truth it does not have — status %q", got)
	}
}

func TestSameBatchWriteNotCreditedByPassingCheck(t *testing.T) {
	o := New(nil, nil, nil)
	sess := NewSession(1, 1, t.TempDir(), evn())
	sess.AddTask("plan:screen:send", "Screen: Send")
	var touched []string
	opts := RunOptions{
		Execute: func(_ context.Context, name string, _ json.RawMessage) (string, bool) {
			if name == "write_file" {
				// The write lands during the batch — after the pre-dispatch
				// snapshot, so the check below never compiled it.
				touched = append(touched, "app/send.tsx")
				return "written", false
			}
			return `{"passed":true,"summary":"no type errors; no lint errors"}`, false
		},
		FilesTouched: func() []string { return append([]string(nil), touched...) },
		Exists:       func(string) bool { return false },
	}

	o.executeTools(context.Background(), sess, []ai.ToolUseBlock{
		{ID: "c1", Name: "write_file", Input: json.RawMessage(`{"path":"app/send.tsx"}`)},
		{ID: "c2", Name: "run_checks", Input: json.RawMessage(`{}`)},
	}, opts)

	if got := taskStatus(sess, "plan:screen:send"); got == "completed" {
		t.Fatalf("FM-L12b: a file written in the SAME batch as the check must not claim "+
			"compile truth from it — status %q", got)
	}
}

func taskStatus(sess *SessionState, id string) string {
	for _, task := range sess.GetTasks() {
		if task.ID == id {
			return task.Status
		}
	}
	return ""
}
