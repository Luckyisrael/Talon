package store

import (
	"database/sql"
	"time"
)

// Prompt attachments: images and markdown docs the user attaches to a fresh
// turn. An attachment is uploaded unclaimed (prompt_id NULL) and claimed by
// exactly one turn at send time — claiming is a single conditional UPDATE, so
// a double-send or a retry cannot attach one file to two turns.
//
// The row carries the reference; the bytes live on disk under the attachments
// dir. Stored conversation blocks reference attachments by id and never embed
// pixel bytes, so the transcript stays small and replayable.
type Attachment struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	PromptID  *int64 `json:"prompt_id,omitempty"`
	Kind      string `json:"kind"` // image | doc
	Name      string `json:"name"`
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	Path      string `json:"-"`
}

func (s *Store) AddAttachment(projectID int64, kind, name, mime, path string, size int64) (*Attachment, error) {
	res, err := s.db.Exec(
		"INSERT INTO prompt_attachments (project_id, kind, name, mime, size, path) VALUES (?, ?, ?, ?, ?, ?)",
		projectID, kind, name, mime, size, path,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Attachment{ID: id, ProjectID: projectID, Kind: kind, Name: name, Mime: mime, Size: size, Path: path}, nil
}

func (s *Store) GetAttachment(id int64) (*Attachment, error) {
	var a Attachment
	var promptID sql.NullInt64
	err := s.db.QueryRow(
		"SELECT id, project_id, prompt_id, kind, name, mime, size, path FROM prompt_attachments WHERE id = ?",
		id,
	).Scan(&a.ID, &a.ProjectID, &promptID, &a.Kind, &a.Name, &a.Mime, &a.Size, &a.Path)
	if err != nil {
		return nil, err
	}
	if promptID.Valid {
		a.PromptID = &promptID.Int64
	}
	return &a, nil
}

// ClaimAttachments links unclaimed attachments to one turn. It returns how
// many rows were claimed; the caller rejects the send unless every id was
// claimed, so an attachment already spent on another turn (or belonging to
// another project) fails the whole send rather than half-attaching.
func (s *Store) ClaimAttachments(promptID, projectID int64, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, promptID, projectID)
	for _, id := range ids {
		args = append(args, id)
	}
	placeholders := ""
	for i := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}
	res, err := s.db.Exec(
		"UPDATE prompt_attachments SET prompt_id = ? WHERE project_id = ? AND prompt_id IS NULL AND id IN ("+placeholders+")",
		args...,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListOrphanAttachments returns uploads nobody ever sent: claimed by no turn
// and older than the cutoff. Fresh unclaimed rows are the composer staging
// its next send — only age makes them garbage.
func (s *Store) ListOrphanAttachments(projectID int64, olderThan time.Duration) ([]Attachment, error) {
	cutoff := time.Now().Add(-olderThan).UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(
		`SELECT id, project_id, prompt_id, kind, name, mime, size, path
		 FROM prompt_attachments
		 WHERE project_id = ? AND prompt_id IS NULL AND created_at < ?`,
		projectID, cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		var promptID sql.NullInt64
		if err := rows.Scan(&a.ID, &a.ProjectID, &promptID, &a.Kind, &a.Name, &a.Mime, &a.Size, &a.Path); err != nil {
			return nil, err
		}
		if promptID.Valid {
			a.PromptID = &promptID.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAttachments removes rows by id. Callers delete the bytes first;
// rows without bytes are better than bytes without rows.
func (s *Store) DeleteAttachments(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids))
	placeholders := ""
	for i, id := range ids {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	_, err := s.db.Exec("DELETE FROM prompt_attachments WHERE id IN ("+placeholders+")", args...)
	return err
}

// BackdateAttachmentCreatedAt moves a row's clock. Test and admin tooling:
// production rows age on their own.
func (s *Store) BackdateAttachmentCreatedAt(id int64, at time.Time) error {
	_, err := s.db.Exec(
		`UPDATE prompt_attachments SET created_at = ? WHERE id = ?`,
		at.UTC().Format("2006-01-02 15:04:05"), id,
	)
	return err
}
