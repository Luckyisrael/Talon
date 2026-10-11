package store

import (
	"database/sql"
	"errors"
)

// app_settings holds the few facts the server generates for itself and must
// survive restarts, with no natural row anywhere else \u2014 today only the
// devnet-SOL export treasury. It is not a cache: losing the treasury would
// strand every payment already sent to that address, which is why it lives in
// the same database as the ledger those payments buy from.

// EnsureSolTreasury returns the persisted devnet-SOL export treasury,
// generating and persisting one on first use. The generator is injected so
// this file stays free of key math; the read-insert-read is what makes two
// concurrent boots converge on ONE address instead of splitting payments
// across two.
func (s *Store) EnsureSolTreasury(create func() (string, error)) (string, error) {
	const key = "sol_treasury"
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	addr, err := create()
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(
		`INSERT INTO app_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		key, addr,
	); err != nil {
		return "", err
	}
	// Re-read: if another writer won the race, THEIR address is the one
	// payments must reach, so the loser adopts it rather than quoting two.
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", err
	}
	return v, nil
}
