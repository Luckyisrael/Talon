package store

import (
	"database/sql"
	"time"
)

// MessageFeedback is one human judgement of one model message. It lives in the
// sidecar, never the session log — see the schema comment.
type MessageFeedback struct {
	ID        int64
	ProjectID int64
	MessageID string
	// Rating is the person's verdict. Kept as an int rather than a bool so a
	// third value (e.g. "mixed") can be added without a migration; today the
	// UI writes 1 (good) or -1 (bad), and 0 is "no opinion, just a comment".
	Rating   int
	Comment  string
	CreateAt time.Time
	Updated  time.Time
}

// RecordFeedback upserts one message's feedback. A re-vote replaces the prior
// rating in place: the row is the person's settled view, not an append-only
// history of their changing mind, and UNIQUE(project_id, message_id) is what
// keeps it that way. A comment is optional — a bare rating is still a signal.
func (s *Store) RecordFeedback(projectID int64, messageID string, rating int, comment string) error {
	if messageID == "" {
		return ErrNoMessageID
	}
	_, err := s.db.Exec(
		`INSERT INTO message_feedback (project_id, message_id, rating, comment, created_at, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(project_id, message_id) DO UPDATE SET
		   rating = excluded.rating,
		   comment = excluded.comment,
		   updated_at = CURRENT_TIMESTAMP`,
		projectID, messageID, rating, comment)
	return err
}

// ErrNoMessageID names a feedback write with nothing to attach to.
var ErrNoMessageID = &storeError{"feedback needs a message id"}

type storeError struct{ msg string }

func (e *storeError) Error() string { return e.msg }

// FeedbackFor returns the feedback recorded for one message, or nil when the
// person never rated it. A nil row is not an error — "no feedback" is the
// common case and the caller renders it as unrated.
func (s *Store) FeedbackFor(projectID int64, messageID string) (*MessageFeedback, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, message_id, rating, comment, created_at, updated_at
		 FROM message_feedback WHERE project_id = ? AND message_id = ?`,
		projectID, messageID)
	var f MessageFeedback
	err := row.Scan(&f.ID, &f.ProjectID, &f.MessageID, &f.Rating, &f.Comment, &f.CreateAt, &f.Updated)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ProjectFeedback returns every rated message for a project, oldest first —
// the tuning view that answers "what did this person actually think of the
// output here". An empty project is an empty list, not an error.
func (s *Store) ProjectFeedback(projectID int64) ([]MessageFeedback, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, message_id, rating, comment, created_at, updated_at
		 FROM message_feedback WHERE project_id = ? ORDER BY created_at ASC`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageFeedback
	for rows.Next() {
		var f MessageFeedback
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.MessageID, &f.Rating, &f.Comment, &f.CreateAt, &f.Updated); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
