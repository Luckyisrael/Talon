package store

import (
	"errors"
)

// This file holds the release-export credit ledger — the SKR monetization
// path. It sits apart from billing.go the way conversation.go sits apart from
// store.go: one concern per file, and the two do not share tables on purpose.
// Subscriptions answer "what tier am I"; export_credits answers "how many
// release builds may I start". A paid tier never touches this ledger — it is
// the path that lets an *unpaid* owner ship exactly as many times as they
// bought, which is why exportAllowed keeps passing without a row.
//
// Three invariants are enforced here rather than in handler code:
//   - the balance IS the ledger ... SpareCredits is SUM(remaining); there is
//     no counter on users and nothing the client holds, so the number cannot
//     drift the way quota.go's three disagreeing limit copies once did.
//   - a payment grants exactly once . tx_signature UNIQUE, and a duplicate is
//     reported as (false, nil) — not an error — because a retried
//     verification is a normal client event, not a failure.
//   - a spend is atomic ............. ConsumeCredit is one conditional UPDATE,
//     so a balance of 1 admits exactly one of two concurrent exports.
//
// Nothing here trusts a client claim: the API layer proves the signature
// against the chain before GrantCredits is called, exactly as billing proves
// a webhook's HMAC before SetTier.

// ErrInvalidCreditGrant is what a caller gets for a non-positive grant. It is
// a programming error, not a user-facing condition.
var ErrInvalidCreditGrant = errors.New("credits granted must be a positive number")

// GrantCredits adds one ledger row holding `credits` for a user, all of it
// spendable immediately. source names how the balance arrived ("skr", "seed",
// "dev"); txSignature is the payment's own identifier and must be unique —
// a second call with the same signature returns (false, nil) without granting
// again, so a client that retries a verification cannot buy the same transfer
// twice. Returns true only when a new row was written.
func (s *Store) GrantCredits(userID int64, credits int, source, txSignature string) (bool, error) {
	if credits <= 0 {
		return false, ErrInvalidCreditGrant
	}
	res, err := s.db.Exec(`
		INSERT INTO export_credits (user_id, credits, remaining, source, tx_signature)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tx_signature) DO NOTHING`,
		userID, credits, credits, source, txSignature)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SpareCredits is the user's entire balance: every grant's remaining, summed.
// Callers must not cache it — it is derived from the ledger for exactly the
// reason quota.go's comment gives about limits.
func (s *Store) SpareCredits(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(remaining), 0) FROM export_credits WHERE user_id = ?`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ConsumeCredit spends exactly one credit and reports whether it could. The
// debit is a single conditional UPDATE against the oldest funded grant, so
// there is no window between reading the balance and spending it: two exports
// starting together against a balance of 1 cannot both proceed, because the
// second statement finds no row with remaining >= 1 and reports false. A read
// followed by a separate decrement is precisely how a zero-balance owner
// launches parallel builds.
func (s *Store) ConsumeCredit(userID int64) (bool, error) {
	res, err := s.db.Exec(`
		UPDATE export_credits
		   SET remaining = remaining - 1
		 WHERE id = (SELECT id FROM export_credits
		              WHERE user_id = ? AND remaining >= 1
		              ORDER BY id ASC LIMIT 1)`,
		userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
