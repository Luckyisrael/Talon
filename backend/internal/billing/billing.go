// Package billing is the payment-provider abstraction for Phase 9. Nothing above
// this package knows which processor is in use: the API and store layers speak
// only to the Provider interface and the normalized types defined here, so a real
// processor slots in later as a provider swap rather than a rewrite (spec 2.8).
//
// Two invariants the spec calls non-negotiable are structural in this design:
//   - Raw card data never reaches the backend (2.1). The provider hands back a
//     hosted checkout URL; the card is entered there, on the processor's PCI page.
//     Nothing in these types carries a PAN, CVV, or expiry — only tokens and ids.
//   - Paid state is driven by verified webhooks (2.3), never a client claim. The
//     only trusted input is a payload that passes VerifyWebhook; the API layer
//     acts on the returned Event and nothing else.
package billing

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidSignature is returned by VerifyWebhook when a payload is unsigned or
// its signature does not match — a forged or tampered webhook. The handler maps
// it to 400 and processes nothing (spec 2.4).
var ErrInvalidSignature = errors.New("billing: invalid webhook signature")

// Provider is the seam every payment processor implements. It is deliberately
// small: create/lookup a customer, start a hosted checkout, cancel, and verify an
// inbound webhook. Deciding what an event means for our tables (upsert the
// subscription, set the tier, write the audit row) is provider-agnostic business
// logic and lives in the API layer, not here.
type Provider interface {
	// Name identifies the provider for storage and audit, e.g. "mock" or "stripe".
	Name() string

	// EnsureCustomer returns the provider-side customer id for a user, creating one
	// if the provider has none yet. Must be idempotent per user so repeated calls
	// never mint a duplicate customer (spec 2.2).
	EnsureCustomer(ctx context.Context, userID int64, email string) (string, error)

	// StartUpgrade opens a checkout for a tier and returns where to send the user
	// to complete payment — a hosted, PCI-compliant page. Raw card data is entered
	// there, never here (spec 2.1).
	StartUpgrade(ctx context.Context, req UpgradeRequest) (Checkout, error)

	// Cancel ends the user's subscription, at period end when atPeriodEnd is true
	// (a downgrade that preserves paid access until the term runs out) or
	// immediately otherwise. The resulting state still arrives as a webhook.
	Cancel(ctx context.Context, providerSubscriptionID string, atPeriodEnd bool) error

	// VerifyWebhook authenticates a raw webhook body against its signature and
	// returns the normalized event. It returns ErrInvalidSignature for anything it
	// cannot prove the provider sent.
	VerifyWebhook(payload []byte, signature string) (Event, error)
}

// Simulator is an optional capability a test-mode provider may implement: it
// produces a correctly-signed webhook payload as if the provider had sent it, so
// the real verified-webhook path can be exercised end-to-end without an external
// processor. Production providers do not implement this; the API layer feature-
// detects it with a type assertion and only exposes the test-complete route when
// it is present (spec 2.5 — test mode is a deliberate, detectable state).
type Simulator interface {
	SimulateEvent(evt Event) (payload []byte, signature string, err error)
}

// UpgradeRequest is the input to StartUpgrade.
type UpgradeRequest struct {
	UserID             int64
	ProviderCustomerID string
	Tier               string
	// SuccessURL and CancelURL are where the hosted checkout returns the user on
	// completion or abandonment. Provider-agnostic; supplied by the API layer.
	SuccessURL string
	CancelURL  string
}

// Checkout is where to send the user to complete payment.
type Checkout struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
}

// EventType is the normalized billing event set we act on, independent of any
// provider's own event names — the mapping from a provider's vocabulary to these
// happens inside that provider's VerifyWebhook.
type EventType string

const (
	// EventSubscriptionActive: a subscription became (or remains) active and paid.
	EventSubscriptionActive EventType = "subscription.active"
	// EventSubscriptionCanceled: a subscription was canceled or lapsed; access
	// reverts to free at (or after) the period end carried on the event.
	EventSubscriptionCanceled EventType = "subscription.canceled"
	// EventPaymentFailed: a charge failed; recorded for audit and dunning. Does not
	// itself downgrade — the provider sends a cancellation if it ultimately lapses.
	EventPaymentFailed EventType = "payment.failed"
)

// Event is the normalized, trusted result of VerifyWebhook. Every field the API
// layer needs to update our tables is here, so no code above billing parses a
// provider's raw format.
type Event struct {
	// ID is the provider's event id and the idempotency key (spec 2.4).
	ID   string
	Type EventType

	UserID                 int64
	ProviderCustomerID     string
	ProviderSubscriptionID string

	Tier              string
	Status            string
	CurrentPeriodEnd  *time.Time
	CancelAtPeriodEnd bool
	PaymentLast4      string

	// Raw is the original payload, stored verbatim in the audit log (spec 2.10).
	Raw string
}
