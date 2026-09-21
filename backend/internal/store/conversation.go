package store

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// ConversationMessage is one persisted turn. Blocks holds the raw JSON array of
// content blocks (text / tool_use / tool_result) exactly as the provider saw it,
// so a reload reconstructs the conversation without lossy summarisation.
type ConversationMessage struct {
	ID           int64     `json:"id"`
	ProjectID    int64     `json:"project_id"`
	PromptID     int64     `json:"prompt_id"`
	Seq          int64     `json:"seq"`
	Role         string    `json:"role"`
	Blocks       string    `json:"blocks"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

// AppendMessage stores one turn at the next sequence number for the project.
// user_id is denormalized from projects for per-tenant RLS — every row is
// tagged with its owner so a DB-level filter can enforce isolation.
func (s *Store) AppendMessage(projectID, promptID int64, role, blocksJSON string, inTok, outTok int) error {
	_, err := s.db.Exec(
		`INSERT INTO conversation_messages (project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens)
		 VALUES (?, (SELECT user_id FROM projects WHERE id = ?), ?, COALESCE((SELECT MAX(seq) FROM conversation_messages WHERE project_id = ?), 0) + 1, ?, ?, ?, ?)`,
		projectID, projectID, promptID, projectID, role, blocksJSON, inTok, outTok,
	)
	if err == nil {
		end := len(blocksJSON)
		if end > 80 {
			end = 80
		}
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES ((SELECT user_id FROM projects WHERE id = ?), ?, 'append_message', ?, ?)", projectID, projectID, role, blocksJSON[:end])
	}
	return err
}

// GetConversation returns the most recent `limit` messages in chronological
// order. limit <= 0 returns the whole conversation.
func (s *Store) GetConversation(projectID int64, limit int) ([]ConversationMessage, error) {
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(
			`SELECT id, project_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
			 FROM conversation_messages WHERE project_id = ?
			 ORDER BY seq DESC LIMIT ?`, projectID, limit)
	} else {
		rows, err = s.db.Query(
			`SELECT id, project_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
			 FROM conversation_messages WHERE project_id = ?
			 ORDER BY seq DESC`, projectID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.PromptID, &m.Seq, &m.Role,
			&m.Blocks, &m.InputTokens, &m.OutputTokens, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Query was DESC to apply LIMIT to the tail; flip back to chronological.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// scanConversationRows drains message rows in chronological order.
func scanConversationRows(rows *sql.Rows) ([]ConversationMessage, error) {
	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.PromptID, &m.Seq, &m.Role,
			&m.Blocks, &m.InputTokens, &m.OutputTokens, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// GetConversationBefore returns up to `limit` messages older than beforeSeq,
// chronological. The pager for long transcripts: page one is the tail,
// earlier pages walk back by seq.
func (s *Store) GetConversationBefore(projectID, beforeSeq int64, limit int) ([]ConversationMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	rows, err := s.db.Query(
		`SELECT id, project_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
		 FROM conversation_messages WHERE project_id = ? AND seq < ?
		 ORDER BY seq DESC LIMIT ?`, projectID, beforeSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConversationRows(rows)
}

// MaxConversationSeq is the watermark clients poll for "anything new": the
// highest stored seq, or 0 for a fresh project.
func (s *Store) MaxConversationSeq(projectID int64) (int64, error) {
	var seq sql.NullInt64
	err := s.db.QueryRow(
		`SELECT MAX(seq) FROM conversation_messages WHERE project_id = ?`, projectID,
	).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

// SearchConversationBlocks finds turns whose stored blocks mention q
// (case-insensitive substring over the raw JSON). LIKE metacharacters in q
// are escaped so a query for "100%" does not become a wildcard. Returns
// whole turns newest first, capped — excerpts, not a dump.
func (s *Store) SearchConversationBlocks(projectID int64, q string, limit int) ([]ConversationMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	rows, err := s.db.Query(
		`SELECT id, project_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at
		 FROM conversation_messages WHERE project_id = ? AND blocks LIKE ? ESCAPE '\'
		 ORDER BY seq DESC LIMIT ?`, projectID, "%"+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConversationRows(rows)
}

// TrimConversation archives then deletes all but the newest `keep` messages.
// Archive is append-only so a long build never loses its early turns.
func (s *Store) TrimConversation(projectID int64, keep int) error {
	_, _ = s.db.Exec(
		`INSERT INTO conversation_archive (id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at)
		 SELECT id, project_id, user_id, prompt_id, seq, role, blocks, input_tokens, output_tokens, created_at FROM conversation_messages
		 WHERE project_id = ? AND seq <= (SELECT COALESCE(MAX(seq), 0) - ? FROM conversation_messages WHERE project_id = ?)`,
		projectID, keep, projectID)
	_, err := s.db.Exec(
		`DELETE FROM conversation_messages WHERE project_id = ? AND seq <= (
			SELECT COALESCE(MAX(seq), 0) - ? FROM conversation_messages WHERE project_id = ?
		)`, projectID, keep, projectID)
	if err == nil {
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES ((SELECT user_id FROM projects WHERE id = ?), ?, 'trim_conversation', ?, ?)", projectID, projectID, strconv.Itoa(keep), "")
	}
	return err
}

// ClearConversation removes the entire conversation for a project.
func (s *Store) ClearConversation(projectID int64) error {
	_, err := s.db.Exec("DELETE FROM conversation_messages WHERE project_id = ?", projectID)
	if err == nil {
		_, _ = s.db.Exec("INSERT INTO audit_log (user_id, project_id, action, target_id, detail) VALUES ((SELECT user_id FROM projects WHERE id = ?), ?, 'clear_conversation', ?, '')", projectID, projectID, strconv.FormatInt(projectID, 10))
	}
	return err
}

// ── Checkpoints ───────────────────────────────────────

// CheckpointFile records a single file's content before a turn modified it.
// Existed=false means the file was created by the turn, so a restore deletes it.
type CheckpointFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Existed bool   `json:"existed"`
}

type Checkpoint struct {
	ID        int64            `json:"id"`
	ProjectID int64            `json:"project_id"`
	PromptID  int64            `json:"prompt_id"`
	Label     string           `json:"label"`
	Files     []CheckpointFile `json:"files"`
	CreatedAt time.Time        `json:"created_at"`
}

// CreateCheckpoint stores the pre-change state of the given files.
func (s *Store) CreateCheckpoint(projectID, promptID int64, label string, files []CheckpointFile) (int64, error) {
	data, err := json.Marshal(files)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(
		"INSERT INTO checkpoints (project_id, prompt_id, label, files) VALUES (?, ?, ?, ?)",
		projectID, promptID, label, string(data),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListCheckpoints returns checkpoint metadata (newest first) without file bodies.
func (s *Store) ListCheckpoints(projectID int64, limit int) ([]Checkpoint, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, project_id, prompt_id, label, files, created_at
		 FROM checkpoints WHERE project_id = ? ORDER BY id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Checkpoint
	for rows.Next() {
		var c Checkpoint
		var filesJSON string
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.PromptID, &c.Label, &filesJSON, &c.CreatedAt); err != nil {
			return nil, err
		}
		// Callers listing checkpoints only need paths, not bodies.
		var files []CheckpointFile
		if json.Unmarshal([]byte(filesJSON), &files) == nil {
			for i := range files {
				files[i].Content = ""
			}
			c.Files = files
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheckpoint returns one checkpoint including full file contents.
func (s *Store) GetCheckpoint(id int64) (*Checkpoint, error) {
	var c Checkpoint
	var filesJSON string
	err := s.db.QueryRow(
		`SELECT id, project_id, prompt_id, label, files, created_at FROM checkpoints WHERE id = ?`, id,
	).Scan(&c.ID, &c.ProjectID, &c.PromptID, &c.Label, &filesJSON, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(filesJSON), &c.Files); err != nil {
		return nil, err
	}
	return &c, nil
}

// PruneCheckpoints keeps only the newest `keep` checkpoints for a project.
func (s *Store) PruneCheckpoints(projectID int64, keep int) error {
	_, err := s.db.Exec(
		`DELETE FROM checkpoints WHERE project_id = ? AND id NOT IN (
			SELECT id FROM checkpoints WHERE project_id = ? ORDER BY id DESC LIMIT ?
		)`, projectID, projectID, keep)
	return err
}

// ── Token accounting ──────────────────────────────────

// RecordPromptTokens attributes token spend to a prompt and rolls it up on the
// user's usage row.
func (s *Store) RecordPromptTokens(promptID, userID int64, in, out, cacheRead int) error {
	if _, err := s.db.Exec(
		`UPDATE prompts SET input_tokens = input_tokens + ?, output_tokens = output_tokens + ?,
		 cache_read_tokens = cache_read_tokens + ? WHERE id = ?`,
		in, out, cacheRead, promptID,
	); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`UPDATE usage SET input_tokens = input_tokens + ?, output_tokens = output_tokens + ? WHERE user_id = ?`,
		in, out, userID,
	)
	return err
}

// RecordPromptRouting attributes a turn's dollar cost and routing decision to
// the same prompt and usage rows the token counters update — one ledger, not
// two. Billing reads tokens and dollars from the same source of truth.
func (s *Store) RecordPromptRouting(promptID, userID int64, costUSD float64, model, reason string) error {
	if _, err := s.db.Exec(
		`UPDATE prompts SET cost_usd = cost_usd + ?, routed_model = ?, route_reason = ? WHERE id = ?`,
		costUSD, model, reason, promptID,
	); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`UPDATE usage SET cost_usd = cost_usd + ? WHERE user_id = ?`,
		costUSD, userID,
	)
	return err
}

// UsageCostTotal sums model spend across all usage rows for the metrics
// scrape. A missing column (pre-migration database) reads as zero rather
// than failing the scrape.
func (s *Store) UsageCostTotal() (float64, error) {
	var total *float64
	if err := s.db.QueryRow("SELECT SUM(cost_usd) FROM usage").Scan(&total); err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}
