package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// ProjectVerifyState is the project's last finished verification sweep as
// W6 policy sees it — the durable shadow of a turn outcome that export,
// release, and APK-ship consult before shipping. One row per project; the
// newest sweep wins (a later pass clears an earlier refusal, and a later
// refusal re-closes a pass).
//
// Status is one of "verified" | "best-effort" | "refused" (the
// orchestrator's VerifyStatus* constants — kept as plain strings here so
// the store stays dependency-free). Skipped names the ladder stages that
// did not run; Remaining names pending plan tasks; Reason explains a
// refusal. Policy records which policy classified the sweep.
type ProjectVerifyState struct {
	ProjectID int64     `json:"project_id"`
	Status    string    `json:"status"`
	Policy    string    `json:"policy"`
	Skipped   []string  `json:"skipped"`
	Remaining []string  `json:"remaining"`
	Reason    string    `json:"reason"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SetProjectVerifyState upserts the state for one project. nil slices are
// stored as empty JSON arrays so reads never need a nil check.
func (s *Store) SetProjectVerifyState(st ProjectVerifyState) error {
	if st.Skipped == nil {
		st.Skipped = []string{}
	}
	if st.Remaining == nil {
		st.Remaining = []string{}
	}
	skipped, err := json.Marshal(st.Skipped)
	if err != nil {
		return err
	}
	remaining, err := json.Marshal(st.Remaining)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO project_verify_state (project_id, status, policy, skipped, remaining, reason, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(project_id) DO UPDATE SET
			status = excluded.status,
			policy = excluded.policy,
			skipped = excluded.skipped,
			remaining = excluded.remaining,
			reason = excluded.reason,
			updated_at = CURRENT_TIMESTAMP`,
		st.ProjectID, st.Status, st.Policy, string(skipped), string(remaining), st.Reason)
	return err
}

// GetProjectVerifyState reads the recorded verdict. found=false (no error)
// means the project has never completed a sweep that recorded one — the
// caller decides what absence means (release treats it as refused).
func (s *Store) GetProjectVerifyState(projectID int64) (bool, ProjectVerifyState, error) {
	st := ProjectVerifyState{ProjectID: projectID}
	var skipped, remaining string
	err := s.db.QueryRow(`
		SELECT status, policy, skipped, remaining, reason, updated_at
		FROM project_verify_state WHERE project_id = ?`, projectID).
		Scan(&st.Status, &st.Policy, &skipped, &remaining, &st.Reason, &st.UpdatedAt)
	if err == sql.ErrNoRows {
		return false, st, nil
	}
	if err != nil {
		return false, st, err
	}
	_ = json.Unmarshal([]byte(skipped), &st.Skipped)
	_ = json.Unmarshal([]byte(remaining), &st.Remaining)
	return true, st, nil
}
