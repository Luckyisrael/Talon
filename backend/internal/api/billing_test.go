package api

// Integration tests for the server-side billing rules of Phase 9 (§3). These
// exercise the trusted core — the verified-webhook ingest path and the HTTP
// handlers around it — against a real store and the mock provider, so the
// guarantees are demonstrated at runtime rather than asserted from reading code.
//
// Coverage map to PHASE_9_PAYMENT_PROFILE_FOUNDATION.md §3:
//   #2  one customer per user ......... TestBillingOneCustomerPerUser
//   #3  grant only after webhook ...... TestBillingUpgradeGrantsTierOnlyThroughVerifiedWebhook
//   #4  forged/unsigned rejected ...... TestBillingForgedWebhookRejected
//   #5  duplicate delivery idempotent . TestBillingDuplicateWebhookIdempotent
//   #9  cancellation downgrades ....... TestBillingCancellationDowngrades
//   #10 auditable events .............. asserted within #4 and #5

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/seekersstudio/backend/internal/billing"
	"github.com/seekersstudio/backend/internal/store"
)

// newBillingTestHarness builds a real store, a Free user, and a BillingHandler
// backed by the mock provider (which is a billing.Simulator, so test mode is on).
func newBillingTestHarness(t *testing.T) (*BillingHandler, *billing.Mock, *store.Store, *store.User) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "billing.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	user, err := st.CreateUser("buyer@local", "hash")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	mock := billing.NewMock("test-secret", "http://localhost:3000/checkout")
	h := NewBillingHandler(st, mock, "http://localhost:3000")
	return h, mock, st, user
}

// authedRequest forges a request carrying the given user id in context, the way
// the auth middleware would, so handler methods that read GetUserID work.
func authedRequest(userID int64, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), UserIDKey, userID))
}

// mustTier reads the user's current tier, failing the test on error.
func mustTier(t *testing.T, st *store.Store, userID int64) string {
	t.Helper()
	u, err := st.GetUserByID(userID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	return u.Tier
}

// tamper returns a copy of b with one byte flipped, so a signature computed over
// the original no longer matches — the essence of a forged payload.
func tamper(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	if len(out) > 0 {
		out[0] ^= 0xff
	}
	return out
}

// §3 #3 — Pro access activates only after a verified webhook confirms payment.
// Starting checkout must not grant anything; only completing it (which routes a
// signed event through the same verify path a live webhook would) flips the tier.
func TestBillingUpgradeGrantsTierOnlyThroughVerifiedWebhook(t *testing.T) {
	h, _, st, user := newBillingTestHarness(t)

	if got := mustTier(t, st, user.ID); got != "free" {
		t.Fatalf("new user tier = %q, want free", got)
	}

	// Starting checkout returns a hosted URL and must NOT change the tier.
	rec := httptest.NewRecorder()
	h.StartUpgrade(rec, authedRequest(user.ID, `{"tier":"pro"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("StartUpgrade status = %d, body %s", rec.Code, rec.Body.String())
	}
	var checkout billing.Checkout
	if err := json.Unmarshal(rec.Body.Bytes(), &checkout); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if checkout.URL == "" {
		t.Error("StartUpgrade returned no checkout URL")
	}
	if got := mustTier(t, st, user.ID); got != "free" {
		t.Fatalf("tier = %q after StartUpgrade alone; the upgrade must not grant before the webhook", got)
	}

	// Completing checkout drives a signed event through ingest, and only then is
	// Pro granted.
	rec = httptest.NewRecorder()
	h.TestComplete(rec, authedRequest(user.ID, `{"tier":"pro"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("TestComplete status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := mustTier(t, st, user.ID); got != "pro" {
		t.Fatalf("tier = %q after completed (verified) upgrade, want pro", got)
	}

	// The subscription the profile reads back reflects real state (spec 2.6): a
	// masked card and a future renewal date, both sourced from the verified event.
	sub, err := st.GetSubscriptionByUser(user.ID)
	if err != nil || sub == nil {
		t.Fatalf("GetSubscriptionByUser: sub=%v err=%v", sub, err)
	}
	if sub.Status != "active" {
		t.Errorf("subscription status = %q, want active", sub.Status)
	}
	if sub.PaymentLast4 != "4242" {
		t.Errorf("payment_last4 = %q, want 4242", sub.PaymentLast4)
	}
	if sub.CurrentPeriodEnd == nil || !sub.CurrentPeriodEnd.After(time.Now()) {
		t.Errorf("current_period_end = %v, want a future renewal date", sub.CurrentPeriodEnd)
	}
}

// §3 #4 — forged or unsigned webhooks are rejected before anything is trusted,
// and (§3 #10) a rejected call leaves no state change and no audit row.
func TestBillingForgedWebhookRejected(t *testing.T) {
	h, mock, st, user := newBillingTestHarness(t)

	// A genuinely signed event we can then corrupt.
	payload, signature, err := mock.SimulateEvent(billing.Event{
		Type:                   billing.EventSubscriptionActive,
		UserID:                 user.ID,
		ProviderSubscriptionID: "sub_forge",
		Tier:                   "pro",
		Status:                 "active",
	})
	if err != nil {
		t.Fatalf("SimulateEvent: %v", err)
	}

	cases := []struct {
		name    string
		payload []byte
		sig     string
	}{
		{"empty signature", payload, ""},
		{"wrong signature", payload, "deadbeef"},
		{"tampered payload", tamper(payload), signature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, err := h.ingest(tc.payload, tc.sig)
			if !errors.Is(err, billing.ErrInvalidSignature) {
				t.Fatalf("ingest err = %v, want ErrInvalidSignature", err)
			}
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", status)
			}
		})
	}

	// No forged call may change state or leave an audit trail.
	if got := mustTier(t, st, user.ID); got != "free" {
		t.Errorf("tier = %q after forged webhooks, want free", got)
	}
	events, err := st.ListBillingEvents(user.ID, 10)
	if err != nil {
		t.Fatalf("ListBillingEvents: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("audit rows = %d after forged webhooks, want 0", len(events))
	}

	// The same rejection at the real HTTP attack surface: the unauthenticated
	// webhook route answers 400 for a bad signature.
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/webhook", bytes.NewReader(payload))
	req.Header.Set(signatureHeader, "not-a-valid-signature")
	rec := httptest.NewRecorder()
	h.Webhook(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Webhook status = %d for bad signature, want 400", rec.Code)
	}
}

// §3 #5 — a duplicate delivery of the same event (identical bytes, which real
// providers do send) grants once and writes a single audit row (§3 #10).
func TestBillingDuplicateWebhookIdempotent(t *testing.T) {
	h, mock, st, user := newBillingTestHarness(t)

	// Sign once, deliver the exact same bytes twice.
	payload, signature, err := mock.SimulateEvent(billing.Event{
		Type:                   billing.EventSubscriptionActive,
		UserID:                 user.ID,
		ProviderSubscriptionID: "sub_dup",
		Tier:                   "pro",
		Status:                 "active",
	})
	if err != nil {
		t.Fatalf("SimulateEvent: %v", err)
	}

	for i := 0; i < 2; i++ {
		status, err := h.ingest(payload, signature)
		if err != nil {
			t.Fatalf("ingest #%d: %v", i+1, err)
		}
		if status != http.StatusOK {
			t.Errorf("ingest #%d status = %d, want 200", i+1, status)
		}
	}

	if got := mustTier(t, st, user.ID); got != "pro" {
		t.Errorf("tier = %q, want pro", got)
	}
	events, err := st.ListBillingEvents(user.ID, 10)
	if err != nil {
		t.Fatalf("ListBillingEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("audit rows = %d after duplicate delivery, want 1", len(events))
	}
}

// §3 #9 — a verified cancellation event downgrades access back to Free.
func TestBillingCancellationDowngrades(t *testing.T) {
	h, mock, st, user := newBillingTestHarness(t)

	// Reach Pro through the verified path first.
	if _, err := h.simulateAndIngest(mock, billing.Event{
		Type:                   billing.EventSubscriptionActive,
		UserID:                 user.ID,
		ProviderSubscriptionID: "sub_cancel",
		Tier:                   "pro",
		Status:                 "active",
	}); err != nil {
		t.Fatalf("grant pro: %v", err)
	}
	if got := mustTier(t, st, user.ID); got != "pro" {
		t.Fatalf("setup: tier = %q, want pro", got)
	}

	// A verified cancellation must return the user to Free.
	if _, err := h.simulateAndIngest(mock, billing.Event{
		Type:                   billing.EventSubscriptionCanceled,
		UserID:                 user.ID,
		ProviderSubscriptionID: "sub_cancel",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := mustTier(t, st, user.ID); got != "free" {
		t.Errorf("tier = %q after cancellation, want free", got)
	}
	sub, err := st.GetSubscriptionByUser(user.ID)
	if err != nil || sub == nil {
		t.Fatalf("GetSubscriptionByUser: sub=%v err=%v", sub, err)
	}
	if sub.Status != "canceled" {
		t.Errorf("subscription status = %q after cancellation, want canceled", sub.Status)
	}
}

// §3 #2 — repeated upgrade attempts / abandoned checkouts never mint a second
// customer record for the same user.
func TestBillingOneCustomerPerUser(t *testing.T) {
	h, _, st, user := newBillingTestHarness(t)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.StartUpgrade(rec, authedRequest(user.ID, `{"tier":"pro"}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("StartUpgrade #%d status = %d, body %s", i+1, rec.Code, rec.Body.String())
		}
	}

	c, err := st.GetCustomerByUser(user.ID)
	if err != nil || c == nil {
		t.Fatalf("GetCustomerByUser: c=%v err=%v", c, err)
	}
	// The mock derives a deterministic, user-scoped id; the store's UNIQUE(user_id)
	// is the hard backstop against duplicates.
	if want := "cus_mock_" + strconv.FormatInt(user.ID, 10); c.ProviderCustomerID != want {
		t.Errorf("provider customer id = %q, want %q", c.ProviderCustomerID, want)
	}
}

// §3 #8 — the self-serve cancel path the /settings UI calls. Backend half only
// (no browser here): cancel-at-period-end keeps Pro until the term ends, while an
// immediate cancel downgrades now. Both route through the same verified path.
func TestBillingSelfServeCancel(t *testing.T) {
	grantPro := func(t *testing.T, h *BillingHandler, mock *billing.Mock, userID int64) {
		t.Helper()
		periodEnd := time.Now().Add(30 * 24 * time.Hour)
		if _, err := h.simulateAndIngest(mock, billing.Event{
			Type:                   billing.EventSubscriptionActive,
			UserID:                 userID,
			ProviderSubscriptionID: "sub_selfserve",
			Tier:                   "pro",
			Status:                 "active",
			CurrentPeriodEnd:       &periodEnd,
			PaymentLast4:           "4242",
		}); err != nil {
			t.Fatalf("grant pro: %v", err)
		}
	}

	t.Run("cancel at period end keeps Pro until the term ends", func(t *testing.T) {
		h, mock, st, user := newBillingTestHarness(t)
		grantPro(t, h, mock, user.ID)

		rec := httptest.NewRecorder()
		h.Cancel(rec, authedRequest(user.ID, `{"immediate":false}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("Cancel status = %d, body %s", rec.Code, rec.Body.String())
		}
		if got := mustTier(t, st, user.ID); got != "pro" {
			t.Errorf("tier = %q after cancel-at-period-end, want pro (access kept until the term ends)", got)
		}
		sub, err := st.GetSubscriptionByUser(user.ID)
		if err != nil || sub == nil {
			t.Fatalf("GetSubscriptionByUser: sub=%v err=%v", sub, err)
		}
		if sub.Status != "active" || !sub.CancelAtPeriodEnd {
			t.Errorf("sub = {status:%q cancel_at_period_end:%v}, want {active true}", sub.Status, sub.CancelAtPeriodEnd)
		}
	})

	t.Run("immediate cancel downgrades now", func(t *testing.T) {
		h, mock, st, user := newBillingTestHarness(t)
		grantPro(t, h, mock, user.ID)

		rec := httptest.NewRecorder()
		h.Cancel(rec, authedRequest(user.ID, `{"immediate":true}`))
		if rec.Code != http.StatusOK {
			t.Fatalf("Cancel status = %d, body %s", rec.Code, rec.Body.String())
		}
		if got := mustTier(t, st, user.ID); got != "free" {
			t.Errorf("tier = %q after immediate cancel, want free", got)
		}
	})
}
