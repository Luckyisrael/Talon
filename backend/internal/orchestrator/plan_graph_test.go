package orchestrator

import (
	"strings"
	"testing"
)

func TestPlanSlug(t *testing.T) {
	cases := map[string]string{
		"Home":          "home",
		"Send SOL":      "send-sol",
		"NFT Gallery":   "nft-gallery",
		"  Balance  ":   "balance",
		"Transaction #": "transaction",
		"":              "",
	}
	for in, want := range cases {
		if got := planSlug(in); got != want {
			t.Errorf("planSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTaskVersionBumpsOnlyOnRealChange(t *testing.T) {
	sess := newTestSession(t)
	if v := sess.TaskVersion(); v != 0 {
		t.Fatalf("fresh version = %d, want 0", v)
	}

	sess.AddTask("plan:screen:home", "Screen: Home")
	afterAdd := sess.TaskVersion()
	if afterAdd == 0 {
		t.Fatal("adding a task did not bump the version")
	}

	// A no-op update (task stays pending with the same result) must not bump the
	// version — that is what keeps the cached prompt prefix alive.
	sess.MarkPlanTask("plan:screen:home", "Screen: Home", "pending", "")
	if sess.TaskVersion() != afterAdd {
		t.Error("a redundant update bumped the version and would bust the cache")
	}

	// A real status change must bump it so the rebuild guard re-renders.
	sess.MarkPlanTask("plan:screen:home", "Screen: Home", "completed", "done")
	if sess.TaskVersion() == afterAdd {
		t.Error("a real status change did not bump the version")
	}
}

func TestRenderPlanChecklist(t *testing.T) {
	tasks := []Task{
		{ID: "plan:screen:home", Description: "Screen: Home", Status: "completed"},
		{ID: "plan:screen:send", Description: "Screen: Send", Status: "pending"},
		{ID: "plan:feature:0", Description: "Feature: view balance", Status: "in_progress"},
		{ID: "plan:feature:1", Description: "Feature: sign tx", Status: "failed"},
	}
	got := renderPlanChecklist(tasks)

	// The heading must steer the model away from re-deriving the plan — that is the
	// whole reason the checklist is injected.
	if !strings.Contains(got, "do not re-derive") {
		t.Errorf("checklist missing the anti-re-derivation heading:\n%s", got)
	}
	for _, want := range []string{
		"[x] Screen: Home",
		"[ ] Screen: Send",
		"Feature: view balance (in progress)",
		"[!] Feature: sign tx (blocked",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("checklist missing %q, got:\n%s", want, got)
		}
	}
}

func TestMarkScreenTasksFromFiles(t *testing.T) {
	sess := newTestSession(t)
	sess.AddTask("plan:screen:send", "Screen: Send")
	sess.AddTask("plan:screen:settings", "Screen: Settings")
	sess.AddTask("plan:feature:0", "Feature: view balance")

	o := &Orchestrator{}
	// The send screen's route file was written; settings was not.
	o.markScreenTasksFromFiles(sess, []string{"app/send.tsx", "lib/wallet.ts"})

	byID := map[string]string{}
	for _, tk := range sess.GetTasks() {
		byID[tk.ID] = tk.Status
	}
	if byID["plan:screen:send"] != "completed" {
		t.Errorf("send screen should be completed after its route file was written, got %q", byID["plan:screen:send"])
	}
	if byID["plan:screen:settings"] != "pending" {
		t.Errorf("settings screen must stay pending — no file touched — got %q", byID["plan:screen:settings"])
	}
	// A feature is never marked by the file heuristic: too loose to be honest.
	if byID["plan:feature:0"] != "pending" {
		t.Errorf("feature must not be marked by the file heuristic, got %q", byID["plan:feature:0"])
	}
}

func TestApplyMarkTaskDone(t *testing.T) {
	sess := newTestSession(t)
	sess.AddTask("plan:screen:send", "Screen: Send")
	sess.AddTask("plan:feature:0", "Feature: view balance")

	o := &Orchestrator{}

	// A capable model checks off a feature by its text — the heuristic can't.
	o.applyMarkTaskDone(sess, toolCall("mark_task_done", `{"task":"view balance"}`))
	// And a screen by name.
	o.applyMarkTaskDone(sess, toolCall("mark_task_done", `{"task":"Send"}`))

	byID := map[string]string{}
	for _, tk := range sess.GetTasks() {
		byID[tk.ID] = tk.Status
	}
	if byID["plan:feature:0"] != "completed" {
		t.Errorf("feature not marked done by substring match, got %q", byID["plan:feature:0"])
	}
	if byID["plan:screen:send"] != "completed" {
		t.Errorf("screen not marked done by name slug, got %q", byID["plan:screen:send"])
	}
}

func TestApplyMarkTaskDoneIgnoresUnknownAndNonTaskCalls(t *testing.T) {
	sess := newTestSession(t)
	sess.AddTask("plan:screen:home", "Screen: Home")
	startVer := sess.TaskVersion()

	o := &Orchestrator{}
	// An unmatched name changes nothing (no dishonest completion).
	o.applyMarkTaskDone(sess, toolCall("mark_task_done", `{"task":"nonexistent screen"}`))
	// A different tool is not our concern.
	o.applyMarkTaskDone(sess, toolCall("write_file", `{"path":"app/index.tsx"}`))

	if sess.TaskVersion() != startVer {
		t.Error("an unmatched or unrelated call moved the plan graph")
	}
	if sess.GetTasks()[0].Status != "pending" {
		t.Error("home should still be pending")
	}
}
