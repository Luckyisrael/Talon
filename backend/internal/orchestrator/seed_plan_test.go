package orchestrator

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/store"

	// The sqlite driver registers itself on import; the test binary must pull it
	// in directly, as the store package's own tests do.
	_ "modernc.org/sqlite"
)

func TestSeedPlanTasksFromPlanJSON(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	u, err := s.CreateUser("dev@example.com", "hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	p, err := s.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	plan := ai.AppPlan{
		Screens: []ai.Screen{
			{Name: "Home", Description: "balance overview"},
			{Name: "Send SOL", Description: "transfer form"},
		},
		Features:   []string{"connect wallet via MWA", "view recent transactions"},
		Navigation: "tab bar",
		Packages:   []string{"expo-router"},
	}
	planJSON, _ := json.Marshal(plan)
	if err := s.UpdateProjectPlan(p.ID, string(planJSON)); err != nil {
		t.Fatalf("update plan: %v", err)
	}

	o := New(nil, s, nil)
	sess := o.CreateSession(1, p.ID, t.TempDir())

	tasks := sess.GetTasks()
	if len(tasks) != 4 {
		t.Fatalf("want 4 seeded tasks (2 screens + 2 features), got %d: %+v", len(tasks), tasks)
	}

	byID := map[string]string{}
	for _, tk := range tasks {
		byID[tk.ID] = tk.Description
		if tk.Status != "pending" {
			t.Errorf("seeded task %q should start pending, got %q", tk.ID, tk.Status)
		}
	}
	if d := byID["plan:screen:send-sol"]; d != "Screen: Send SOL — transfer form" {
		t.Errorf("send-sol screen description wrong: %q", d)
	}
	if _, ok := byID["plan:screen:home"]; !ok {
		t.Error("home screen task not seeded")
	}
	if d := byID["plan:feature:0"]; d != "Feature: connect wallet via MWA" {
		t.Errorf("feature 0 description wrong: %q", d)
	}
	if _, ok := byID["plan:feature:1"]; !ok {
		t.Error("feature 1 not seeded")
	}
}

func TestSeedPlanTasksNoPlanIsHarmless(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	u, _ := s.CreateUser("dev@example.com", "hash")
	p, err := s.CreateProject(u.ID, "proj", "1.0.0", "proj", "com.x.proj", "com.x.proj", "proj")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// No plan saved — seeding must be a silent no-op, not a crash.
	o := New(nil, s, nil)
	sess := o.CreateSession(1, p.ID, t.TempDir())
	if len(sess.GetTasks()) != 0 {
		t.Errorf("a project with no plan should seed no tasks, got %d", len(sess.GetTasks()))
	}
}
