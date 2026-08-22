package store

import (
	"database/sql"
	"time"
)

// This file holds the payment/billing persistence for Phase 9. It is kept apart
// from store.go the way conversation.go is: one concern per file. The tables it
// reads (customers, subscriptions, billing_events) are created in migrate().
//
// The invariants the Phase 9 spec calls non-negotiable are enforced here at the
// database level, not just in handler code:
//   - one canonical customer per user (rule 2.2)     -> customers.user_id UNIQUE
//   - subscription is the one source of truth (2.6)  -> subscriptions.user_id UNIQUE
//   - webhook idempotency (rule 2.4)                 -> billing_events.provider_event_id UNIQUE
//
// Nothing here decides a tier from a client claim: SetTier is called only after a
// verified webhook is processed (rule 2.3).

// Customer links a local user to the payment provider's own customer record.
type Customer struct {
	ID                 int64     `json:"id"`
	UserID             int64     `json:"user_id"`
	Provider           string    `json:"provider"`
	ProviderCustomerID string    `json:"provider_customer_id"`
	CreatedAt          time.Time `json:"created_at"`
}

// Subscription is the source of truth for a user's paid state: what tier they are
// on, when it renews, the last-4 of the card on file, and whether a cancel is
// pending at period end. One row per user, written only from verified webhooks.
type Subscription struct {
	UserID                 int64      `json:"user_id"`
	ProviderSubscriptionID string     `json:"provider_subscription_id"`
	Status                 string     `json:"status"` // active | canceled | past_due | inactive
	Tier                   string     `json:"tier"`
	CurrentPeriodEnd       *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd      bool       `json:"cancel_at_period_end"`
	PaymentLast4           string     `json:"payment_last4,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// BillingEvent is one audited entry in the billing log (rule 2.10). ProviderEventID
// is the idempotency key: the same provider event is stored at most once.
type BillingEvent struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	ProviderEventID string    `json:"provider_event_id"`
	Type            string    `json:"type"`
	Payload         string    `json:"payload,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// EnsureCustomer records the provider's customer id for a user exactly once. If a
// customer row already exists the INSERT is a no-op (ON CONFLICT DO NOTHING) and
// the existing row is returned unchanged — so a caller that raced, or that passes
// a freshly-minted provider id when one is already stored, cannot create a second
// customer for the same user. Callers should still check GetCustomerByUser first
// to avoid asking the provider to create a duplicate customer on its side.
func (s *Store) EnsureCustomer(userID int64, provider, providerCustomerID string) (*Customer, error) {
	if _, err := s.db.Exec(
		`INSERT INTO customers (user_id, provider, provider_customer_id)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO NOTHING`,
		userID, provider, providerCustomerID,
	); err != nil {
		return nil, err
	}
	return s.GetCustomerByUser(userID)
}

// GetCustomerByUser returns the user's customer record, or (nil, nil) when none
// exists yet — a user who has never started an upgrade has no customer row.
func (s *Store) GetCustomerByUser(userID int64) (*Customer, error) {
	c := &Customer{}
	err := s.db.QueryRow(
		"SELECT id, user_id, provider, provider_customer_id, created_at FROM customers WHERE user_id = ?",
		userID,
	).Scan(&c.ID, &c.UserID, &c.Provider, &c.ProviderCustomerID, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

const subscriptionColumns = "user_id, provider_subscription_id, status, tier, current_period_end, cancel_at_period_end, payment_last4, created_at, updated_at"

// scanSubscription reads one subscription row in subscriptionColumns order,
// mapping the nullable renewal date and the 0/1 cancel flag onto the struct.
func scanSubscription(row *sql.Row) (*Subscription, error) {
	sub := &Subscription{}
	var periodEnd sql.NullTime
	var cancelAtEnd int
	if err := row.Scan(
		&sub.UserID, &sub.ProviderSubscriptionID, &sub.Status, &sub.Tier,
		&periodEnd, &cancelAtEnd, &sub.PaymentLast4, &sub.CreatedAt, &sub.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if periodEnd.Valid {
		t := periodEnd.Time
		sub.CurrentPeriodEnd = &t
	}
	sub.CancelAtPeriodEnd = cancelAtEnd != 0
	return sub, nil
}

// UpsertSubscription writes the current subscription state for a user, inserting
// on first sight and overwriting every provider-driven field thereafter. This is
// the only writer of paid state, and it runs only from verified webhook handling.
func (s *Store) UpsertSubscription(sub Subscription) error {
	cancel := 0
	if sub.CancelAtPeriodEnd {
		cancel = 1
	}
	// A nil renewal date is stored as SQL NULL rather than the zero time, so the
	// profile can distinguish "no renewal known" from "renews at year zero".
	var periodEnd interface{}
	if sub.CurrentPeriodEnd != nil {
		periodEnd = *sub.CurrentPeriodEnd
	}
	_, err := s.db.Exec(
		`INSERT INTO subscriptions
			(user_id, provider_subscription_id, status, tier, current_period_end, cancel_at_period_end, payment_last4, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(user_id) DO UPDATE SET
			provider_subscription_id = excluded.provider_subscription_id,
			status = excluded.status,
			tier = excluded.tier,
			current_period_end = excluded.current_period_end,
			cancel_at_period_end = excluded.cancel_at_period_end,
			payment_last4 = excluded.payment_last4,
			updated_at = CURRENT_TIMESTAMP`,
		sub.UserID, sub.ProviderSubscriptionID, sub.Status, sub.Tier, periodEnd, cancel, sub.PaymentLast4,
	)
	return err
}

// GetSubscriptionByUser returns the user's subscription, or (nil, nil) when they
// have none — a free user who never upgraded. The profile treats a nil result as
// the free tier rather than an error.
func (s *Store) GetSubscriptionByUser(userID int64) (*Subscription, error) {
	sub, err := scanSubscription(s.db.QueryRow(
		"SELECT "+subscriptionColumns+" FROM subscriptions WHERE user_id = ?", userID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sub, err
}

// SetTier updates the user's tier. It is the bridge between verified subscription
// state and the quota layer, which reads users.tier. Called only after a webhook
// has been authenticated and processed — never from a client-supplied value.
func (s *Store) SetTier(userID int64, tier string) error {
	_, err := s.db.Exec("UPDATE users SET tier = ? WHERE id = ?", tier, userID)
	return err
}

// InsertBillingEvent appends an audit entry and reports whether it was new. The
// provider_event_id UNIQUE constraint makes this idempotent: a duplicate webhook
// delivery hits ON CONFLICT DO NOTHING, affects no rows, and returns false — the
// signal for the handler to skip re-processing an event it has already handled.
func (s *Store) InsertBillingEvent(ev BillingEvent) (bool, error) {
	res, err := s.db.Exec(
		`INSERT INTO billing_events (user_id, provider_event_id, type, payload)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(provider_event_id) DO NOTHING`,
		ev.UserID, ev.ProviderEventID, ev.Type, ev.Payload,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListBillingEvents returns a user's most recent billing events for the profile's
// history view, newest first.
func (s *Store) ListBillingEvents(userID int64, limit int) ([]BillingEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(
		"SELECT id, user_id, provider_event_id, type, payload, created_at FROM billing_events WHERE user_id = ? ORDER BY id DESC LIMIT ?",
		userID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []BillingEvent{}
	for rows.Next() {
		var ev BillingEvent
		if err := rows.Scan(&ev.ID, &ev.UserID, &ev.ProviderEventID, &ev.Type, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}
