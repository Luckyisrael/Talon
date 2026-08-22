package billing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Mock is the test-mode provider (spec 2.5). It stands in for a real processor
// during development: it mints deterministic customer ids, returns a checkout URL
// into our own simulated payment page, and — crucially — signs and verifies
// webhooks with the same HMAC scheme a real provider would, so the trusted
// verified-webhook path (verify -> idempotent -> upsert -> set tier -> audit) is
// exercised for real. No card data ever touches it; there is nothing to charge.
//
// It is the only provider that also implements Simulator: the API layer uses that
// to emit a signed event from a test-only route. Swapping in a real provider means
// constructing that provider instead — no billing logic above this package moves.
type Mock struct {
	secret       string
	checkoutBase string
}

// NewMock builds the test-mode provider. secret signs and verifies webhooks (it
// is a test credential, kept out of the repo per spec 2.9); checkoutBase is the
// app URL of the simulated checkout page StartUpgrade points the user at.
func NewMock(secret, checkoutBase string) *Mock {
	return &Mock{secret: secret, checkoutBase: checkoutBase}
}

func (m *Mock) Name() string { return "mock" }

// EnsureCustomer maps a user to a stable fake customer id. Deterministic in the
// user id, so calling it again for the same user yields the same id and never a
// duplicate (spec 2.2) — the store's UNIQUE(user_id) is the second line of that.
func (m *Mock) EnsureCustomer(ctx context.Context, userID int64, email string) (string, error) {
	return "cus_mock_" + strconv.FormatInt(userID, 10), nil
}

// StartUpgrade returns a checkout URL into the app's own test-mode payment page,
// carrying the session, tier, and user so that page can drive the test-complete
// route. A real provider would instead return its hosted checkout URL here.
func (m *Mock) StartUpgrade(ctx context.Context, req UpgradeRequest) (Checkout, error) {
	sid := fmt.Sprintf("cs_mock_%d_%s", req.UserID, req.Tier)
	u, err := url.Parse(m.checkoutBase)
	if err != nil {
		return Checkout{}, fmt.Errorf("billing: bad checkout base %q: %w", m.checkoutBase, err)
	}
	q := u.Query()
	q.Set("session", sid)
	q.Set("tier", req.Tier)
	q.Set("user", strconv.FormatInt(req.UserID, 10))
	u.RawQuery = q.Encode()
	return Checkout{SessionID: sid, URL: u.String()}, nil
}

// Cancel is a no-op for the mock: there is no external subscription to end. The
// resulting state change is delivered through the same trusted webhook path as
// every other change — the API cancel handler simulates a canceled event — so the
// mock never mutates paid state directly and the "webhooks are the source of
// truth" invariant (spec 2.3) holds even in test mode.
func (m *Mock) Cancel(ctx context.Context, providerSubscriptionID string, atPeriodEnd bool) error {
	return nil
}

// wireEvent is the mock's on-the-wire webhook format. VerifyWebhook parses it and
// SimulateEvent produces it — a real provider would have its own shape and its own
// mapping to the normalized Event.
type wireEvent struct {
	EventID                string     `json:"event_id"`
	Type                   string     `json:"type"`
	UserID                 int64      `json:"user_id"`
	ProviderCustomerID     string     `json:"customer_id"`
	ProviderSubscriptionID string     `json:"subscription_id"`
	Tier                   string     `json:"tier"`
	Status                 string     `json:"status"`
	CurrentPeriodEnd       *time.Time `json:"current_period_end,omitempty"`
	CancelAtPeriodEnd      bool       `json:"cancel_at_period_end"`
	PaymentLast4           string     `json:"payment_last4,omitempty"`
}

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook authenticates the payload with a constant-time HMAC comparison and
// returns the normalized event. An empty or mismatched signature is rejected
// before the body is even parsed, so a forged webhook never reaches our tables.
func (m *Mock) VerifyWebhook(payload []byte, signature string) (Event, error) {
	expected := sign(m.secret, payload)
	if signature == "" || !hmac.Equal([]byte(expected), []byte(signature)) {
		return Event{}, ErrInvalidSignature
	}
	var w wireEvent
	if err := json.Unmarshal(payload, &w); err != nil {
		return Event{}, fmt.Errorf("billing: malformed webhook payload: %w", err)
	}
	return Event{
		ID:                     w.EventID,
		Type:                   EventType(w.Type),
		UserID:                 w.UserID,
		ProviderCustomerID:     w.ProviderCustomerID,
		ProviderSubscriptionID: w.ProviderSubscriptionID,
		Tier:                   w.Tier,
		Status:                 w.Status,
		CurrentPeriodEnd:       w.CurrentPeriodEnd,
		CancelAtPeriodEnd:      w.CancelAtPeriodEnd,
		PaymentLast4:           w.PaymentLast4,
		Raw:                    string(payload),
	}, nil
}

// SimulateEvent renders a normalized event as a signed wire payload, as if the
// provider had sent it. Test-mode only (see Simulator). If the caller left the id
// blank, a unique one is generated so each simulated delivery is a distinct event
// for idempotency purposes.
func (m *Mock) SimulateEvent(evt Event) ([]byte, string, error) {
	id := evt.ID
	if id == "" {
		id = "evt_mock_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	w := wireEvent{
		EventID:                id,
		Type:                   string(evt.Type),
		UserID:                 evt.UserID,
		ProviderCustomerID:     evt.ProviderCustomerID,
		ProviderSubscriptionID: evt.ProviderSubscriptionID,
		Tier:                   evt.Tier,
		Status:                 evt.Status,
		CurrentPeriodEnd:       evt.CurrentPeriodEnd,
		CancelAtPeriodEnd:      evt.CancelAtPeriodEnd,
		PaymentLast4:           evt.PaymentLast4,
	}
	payload, err := json.Marshal(w)
	if err != nil {
		return nil, "", err
	}
	return payload, sign(m.secret, payload), nil
}

// interface conformance — caught at compile time if a signature drifts.
var (
	_ Provider  = (*Mock)(nil)
	_ Simulator = (*Mock)(nil)
)
