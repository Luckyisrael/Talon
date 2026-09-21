package store

import (
	"database/sql"
	"time"
)

// Refresh tokens: opaque, single-purpose credentials that mint new access
// tokens without another OAuth round trip. Only the SHA-256 hash is stored —
// a database read never yields a usable token. Rotation replaces the presented
// token each use; presenting an already-rotated token means the token leaked,
// so the whole family dies (see ClaimRefreshToken).
type RefreshToken struct {
	ID          int64
	UserID      int64
	Hash        string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	ReplacedBy  *int64
	LastUsedAt  *time.Time
	DeviceLabel string
}

func (s *Store) AddRefreshToken(userID int64, hash string, expiresAt time.Time, deviceLabel string) (*RefreshToken, error) {
	if len(deviceLabel) > 128 {
		deviceLabel = deviceLabel[:128]
	}
	res, err := s.db.Exec(
		"INSERT INTO refresh_tokens (user_id, token_hash, expires_at, device_label) VALUES (?, ?, ?, ?)",
		userID, hash, expiresAt.UTC().Format("2006-01-02 15:04:05"), deviceLabel,
	)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &RefreshToken{ID: id, UserID: userID, Hash: hash, ExpiresAt: expiresAt}, nil
}

func (s *Store) GetRefreshToken(hash string) (*RefreshToken, error) {
	var t RefreshToken
	var revokedAt, lastUsed sql.NullTime
	var replacedInt sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, user_id, token_hash, created_at, expires_at, revoked_at, replaced_by,
		        last_used_at, device_label
		 FROM refresh_tokens WHERE token_hash = ?`,
		hash,
	).Scan(&t.ID, &t.UserID, &t.Hash, &t.CreatedAt, &t.ExpiresAt, &revokedAt, &replacedInt,
		&lastUsed, &t.DeviceLabel)
	if err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	if replacedInt.Valid {
		t.ReplacedBy = &replacedInt.Int64
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	return &t, nil
}

// GetRefreshTokenByID loads one row for ownership checks. Callers compare
// UserID themselves; a missing row is an error either way.
func (s *Store) GetRefreshTokenByID(id int64) (*RefreshToken, error) {
	var t RefreshToken
	var revokedAt, lastUsed sql.NullTime
	var replacedInt sql.NullInt64
	err := s.db.QueryRow(
		`SELECT id, user_id, token_hash, created_at, expires_at, revoked_at, replaced_by,
		        last_used_at, device_label
		 FROM refresh_tokens WHERE id = ?`,
		id,
	).Scan(&t.ID, &t.UserID, &t.Hash, &t.CreatedAt, &t.ExpiresAt, &revokedAt, &replacedInt,
		&lastUsed, &t.DeviceLabel)
	if err != nil {
		return nil, err
	}
	if revokedAt.Valid {
		t.RevokedAt = &revokedAt.Time
	}
	if replacedInt.Valid {
		t.ReplacedBy = &replacedInt.Int64
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	return &t, nil
}

// TouchRefreshToken records a successful rotation use. Drives the "last
// active" column the sessions screen reads.
func (s *Store) TouchRefreshToken(id int64) error {
	_, err := s.db.Exec(
		`UPDATE refresh_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`,
		id,
	)
	return err
}

// UserSession is one live login a user can inspect and revoke.
type UserSession struct {
	ID          int64      `json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	DeviceLabel string     `json:"device_label"`
}

// ListUserSessions returns live refresh tokens newest first: unrevoked and
// unexpired. Hashes never leave the database.
func (s *Store) ListUserSessions(userID int64) ([]UserSession, error) {
	rows, err := s.db.Query(
		`SELECT id, created_at, expires_at, last_used_at, device_label
		 FROM refresh_tokens
		 WHERE user_id = ? AND revoked_at IS NULL AND expires_at > CURRENT_TIMESTAMP
		 ORDER BY id DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserSession
	for rows.Next() {
		var u UserSession
		var lastUsed sql.NullTime
		if err := rows.Scan(&u.ID, &u.CreatedAt, &u.ExpiresAt, &lastUsed, &u.DeviceLabel); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			u.LastUsedAt = &lastUsed.Time
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// RotateRefreshToken spends one token and mints its replacement in a single
// UPDATE guarded on "still live": concurrent double-use claims exactly one
// winner, the loser sees zero rows and fails closed.
func (s *Store) RotateRefreshToken(id, replacementID int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE refresh_tokens SET revoked_at = CURRENT_TIMESTAMP, replaced_by = ?
		 WHERE id = ? AND revoked_at IS NULL`,
		replacementID, id,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RevokeRefreshToken kills one token by id (logout on one device).
func (s *Store) RevokeRefreshToken(id int64) error {
	_, err := s.db.Exec(
		`UPDATE refresh_tokens SET revoked_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND revoked_at IS NULL`,
		id,
	)
	return err
}

// ExpireRefreshToken backdates a row past expiry. Test and admin tooling:
// production rows expire on their own clock.
func (s *Store) ExpireRefreshToken(hash string) error {
	_, err := s.db.Exec(
		`UPDATE refresh_tokens SET expires_at = datetime('now', '-1 day')
		 WHERE token_hash = ?`,
		hash,
	)
	return err
}

// RevokeUserRefreshTokens kills every live token for a user: reuse detected
// (a rotated token presented again), so the whole family is suspect.
func (s *Store) RevokeUserRefreshTokens(userID int64) error {
	_, err := s.db.Exec(
		`UPDATE refresh_tokens SET revoked_at = CURRENT_TIMESTAMP
		 WHERE user_id = ? AND revoked_at IS NULL`,
		userID,
	)
	return err
}

// PurgeExpiredRefreshTokens deletes dead rows (expired or long-revoked).
// Best-effort janitor duty, called on refresh; a failure never fails the turn.
func (s *Store) PurgeExpiredRefreshTokens() {
	s.db.Exec(`DELETE FROM refresh_tokens
		WHERE expires_at < datetime('now', '-7 days')
		OR (revoked_at IS NOT NULL AND revoked_at < datetime('now', '-7 days'))`)
}
