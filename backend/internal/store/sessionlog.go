package store

import (
	"fmt"
	"time"

	"github.com/luckyisrael/talon/backend/internal/harness/events"
	"github.com/luckyisrael/talon/backend/internal/harness/sessionlog"
)

// This file is the SQLite backend for the harness session log (H1): the
// append-only event record behind every agent turn. It implements the
// sessionlog.Store seam, so the log package owns projection logic and this
// layer only persists (key, seq, kind, payload, at) rows. The table is
// created in migrate() alongside the others; rows are never updated or
// deleted — compaction shadows derivation, it does not trim storage.
//
// Legacy note: conversation_messages (one row per finished message) remains
// the read path until the v1 derivation is flipped on everywhere. Both are
// written during the transition so a rollback is a flag change, not a
// migration.

// AppendEvent persists one session-log row under key at the next seq.
func (s *Store) AppendEvent(key string, kind events.Kind, payload []byte, at time.Time) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO session_events (log_key, kind, payload, created_at)
		 VALUES (?, ?, ?, ?)`,
		key, string(kind), string(payload), at,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Sync is the durability barrier: it checkpoints the WAL into the main database
// file and truncates it. AppendEvent is a synchronous insert, so every event is
// already durable across a process crash — but the connection runs
// synchronous=NORMAL, which does not fsync the WAL on every commit. Until this
// runs, a power loss can take the committed-but-unfsync'd tail with it.
//
// The checkpoint policy calls this before a model dispatch and before a
// top-level tool's side effect, fail-closed: the event that records intent must
// be on stable storage before the effect it describes happens.
func (s *Store) Sync() error {
	var busy, log, checkpointed int
	// TRUNCATE resets the WAL to zero pages, so the next barrier has a bounded
	// amount to force. A checkpoint cannot run while another connection holds a
	// read lock; busy_timeout (5s on the DSN) covers the normal case, and the
	// returned busy flag keeps a transient lock a warning rather than a failure
	// that refuses a tool the model needs.
	err := s.db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed)
	if err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("wal checkpoint was busy; %d frames remain uncheckpointed", log)
	}
	return nil
}

// EventsFrom returns every event under key from fromSeq (0 = all), in seq
// order. Payloads come back exactly as written — the log freezes them at
// append time and this layer never re-marshals.
func (s *Store) EventsFrom(key string, fromSeq int64) ([]sessionlog.Event, error) {
	rows, err := s.db.Query(
		`SELECT seq, kind, payload, created_at FROM session_events
		 WHERE log_key = ? AND seq > ? ORDER BY seq ASC`, key, fromSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sessionlog.Event
	for rows.Next() {
		var e sessionlog.Event
		var kind, payload string
		var at time.Time
		if err := rows.Scan(&e.Seq, &kind, &payload, &at); err != nil {
			return nil, err
		}
		e.Kind = events.Kind(kind)
		e.Payload = []byte(payload)
		e.At = at
		out = append(out, e)
	}
	return out, rows.Err()
}

// SessionLog opens (or resumes) the harness log for one project's build
// conversation. The key is stable per project, so a restarted process resumes
// the same log instead of starting a second one.
func (s *Store) SessionLog(projectKey string) (*sessionlog.Log, error) {
	return sessionlog.New(s, projectKey)
}

// SessionLogForked opens a child log seeded with the parent's prefix. The
// child persists under its own key from the first append.
func (s *Store) SessionLogForked(parent *sessionlog.Log, childKey string, fromSeq int64) (*sessionlog.Log, error) {
	return parent.Fork(s, childKey, fromSeq)
}
