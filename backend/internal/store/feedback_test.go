package store

import (
	"strconv"
	"testing"
)

// The feedback rows carry a FOREIGN KEY to projects, so a test that writes a
// rating needs the project to exist. seedProjects makes the ids real.
func seedProjects(t *testing.T, s *Store, ids ...int64) {
	t.Helper()
	if _, err := s.db.Exec(
		"INSERT OR IGNORE INTO users (id, email, password_hash) VALUES (1, 't@t', 'x')"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, id := range ids {
		if _, err := s.db.Exec(
			"INSERT OR IGNORE INTO projects (id, user_id, name, slug) VALUES (?, 1, ?, ?)",
			id, "p"+strconv.FormatInt(id, 10), "p"+strconv.FormatInt(id, 10)); err != nil {
			t.Fatalf("seed project %d: %v", id, err)
		}
	}
}

// A first rating inserts. The row is the person's view, not an append-only
// history of their changing mind.
func TestRecordFeedbackInserts(t *testing.T) {
	s := newTestStore(t)
	seedProjects(t, s, 1)

	if err := s.RecordFeedback(1, "msg-1", 1, "this is exactly what I meant"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	f, err := s.FeedbackFor(1, "msg-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if f == nil {
		t.Fatal("a recorded rating must be findable")
	}
	if f.Rating != 1 || f.Comment != "this is exactly what I meant" {
		t.Errorf("got rating %d comment %q", f.Rating, f.Comment)
	}
}

// A re-vote overwrites in place: UNIQUE(project_id, message_id) is what keeps
// the signal as the settled view, and the update must not leave a stale row.
func TestRecordFeedbackRevoteReplaces(t *testing.T) {
	s := newTestStore(t)
	seedProjects(t, s, 1)

	if err := s.RecordFeedback(1, "msg-1", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFeedback(1, "msg-1", -1, "on reflection, no"); err != nil {
		t.Fatal(err)
	}

	f, err := s.FeedbackFor(1, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if f.Rating != -1 || f.Comment != "on reflection, no" {
		t.Errorf("the re-vote must win, got rating %d comment %q", f.Rating, f.Comment)
	}

	// Exactly one row: the re-vote replaced, it did not append.
	all, err := s.ProjectFeedback(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("a re-vote must not mint a second row, got %d", len(all))
	}
}

// "No feedback" is the common case and must be nil, not an error — the caller
// renders it as unrated rather than handling a failure.
func TestFeedbackForUnratedIsNilNotError(t *testing.T) {
	s := newTestStore(t)

	f, err := s.FeedbackFor(1, "never-rated")
	if err != nil {
		t.Fatalf("an unrated message must not error, got %v", err)
	}
	if f != nil {
		t.Errorf("an unrated message must be nil, got %+v", f)
	}
}

// Ratings are isolated per project: feedback in one project must never show up
// in another's tuning view, even for the same message id.
func TestFeedbackIsProjectScoped(t *testing.T) {
	s := newTestStore(t)
	seedProjects(t, s, 1, 2)

	if err := s.RecordFeedback(1, "msg-1", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFeedback(2, "msg-1", -1, ""); err != nil {
		t.Fatal(err)
	}

	a, err := s.ProjectFeedback(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].Rating != 1 {
		t.Errorf("project 1 must see only its own rating, got %+v", a)
	}
	b, err := s.ProjectFeedback(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1 || b[0].Rating != -1 {
		t.Errorf("project 2 must see only its own rating, got %+v", b)
	}
}

// A feedback write with no message to attach to is refused — it has nothing to
// bind the rating to.
func TestRecordFeedbackNeedsAMessageID(t *testing.T) {
	s := newTestStore(t)

	if err := s.RecordFeedback(1, "", 1, ""); err != ErrNoMessageID {
		t.Errorf("got %v, want ErrNoMessageID", err)
	}
}

// An unrated project is an empty list, not an error.
func TestProjectFeedbackEmptyIsNilNotError(t *testing.T) {
	s := newTestStore(t)

	all, err := s.ProjectFeedback(99)
	if err != nil {
		t.Fatalf("an unrated project must not error, got %v", err)
	}
	if len(all) != 0 {
		t.Errorf("an unrated project must be empty, got %d", len(all))
	}
}
