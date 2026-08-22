package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/seekersstudio/backend/internal/billing"
	"github.com/seekersstudio/backend/internal/store"
)

// signatureHeader carries the provider's HMAC over the raw webhook body.
const signatureHeader = "X-Billing-Signature"

// maxWebhookBytes caps the webhook body so a hostile caller cannot exhaust memory
// on the one route that is deliberately unauthenticated.
const maxWebhookBytes = 1 << 20 // 1 MiB

// proTier is the only tier a user can self-serve upgrade to. Team/enterprise are
// contact-sales (the waitlist), not a checkout.
const proTier = "pro"

// BillingHandler owns the payment flows: starting an upgrade, cancelling, and the
// one thing that actually changes paid state — processing a verified webhook. It
// talks only to the billing.Provider interface, so the concrete processor is a
// construction-time choice, not baked into this logic (spec 2.8).
//
// testMode gates the simulate route (spec 2.5). It is set from the provider being
// a billing.Simulator, so a real provider automatically closes that door.
type BillingHandler struct {
	store    *store.Store
	provider billing.Provider
	appBase  string // frontend origin, for hosted-checkout return URLs
	testMode bool
}

// NewBillingHandler wires the handler. testMode is derived from the provider: only
// a Simulator (the mock) can drive the test-complete route.
func NewBillingHandler(s *store.Store, provider billing.Provider, appBase string) *BillingHandler {
	_, isSim := provider.(billing.Simulator)
	return &BillingHandler{store: s, provider: provider, appBase: appBase, testMode: isSim}
}

// ensureCustomer returns the user's provider customer, creating one exactly once.
// It checks the store first so a returning user never triggers a second
// provider-side customer creation (spec 2.2); only a genuinely new user calls the
// provider, and the store's UNIQUE(user_id) is the backstop.
func (h *BillingHandler) ensureCustomer(r *http.Request, userID int64, email string) (*store.Customer, error) {
	c, err := h.store.GetCustomerByUser(userID)
	if err != nil {
		return nil, err
	}
	if c != nil {
		return c, nil
	}
	pid, err := h.provider.EnsureCustomer(r.Context(), userID, email)
	if err != nil {
		return nil, err
	}
	return h.store.EnsureCustomer(userID, h.provider.Name(), pid)
}

// StartUpgrade begins a checkout for the Pro tier and returns the hosted-checkout
// URL for the client to redirect to. It never touches card data (spec 2.1) and
// never changes the tier — that happens only when the resulting webhook is
// verified and processed.
func (h *BillingHandler) StartUpgrade(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Tier == "" {
		req.Tier = proTier
	}
	if req.Tier != proTier {
		// Team/enterprise are handled through the waitlist, not self-serve checkout.
		writeError(w, http.StatusBadRequest, "that tier is not available for self-serve upgrade")
		return
	}

	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	customer, err := h.ensureCustomer(r, userID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare customer")
		return
	}

	checkout, err := h.provider.StartUpgrade(r.Context(), billing.UpgradeRequest{
		UserID:             userID,
		ProviderCustomerID: customer.ProviderCustomerID,
		Tier:               req.Tier,
		SuccessURL:         h.appBase + "/settings?upgrade=success",
		CancelURL:          h.appBase + "/pricing?upgrade=cancelled",
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to start checkout")
		return
	}
	writeJSON(w, http.StatusOK, checkout)
}

// Cancel cancels the user's subscription. By default it cancels at period end (a
// downgrade that keeps paid access until the term runs out, spec 2.7); immediate
// cancellation is available via the request body. The resulting state change is
// applied through the same trusted webhook path as everything else, so cancel
// never mutates paid state directly.
func (h *BillingHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	var req struct {
		Immediate bool `json:"immediate"`
	}
	// A missing/empty body is fine — it means cancel at period end.
	_ = json.NewDecoder(r.Body).Decode(&req)

	sub, err := h.store.GetSubscriptionByUser(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load subscription")
		return
	}
	if sub == nil || sub.Status != "active" {
		writeError(w, http.StatusBadRequest, "no active subscription to cancel")
		return
	}

	if err := h.provider.Cancel(r.Context(), sub.ProviderSubscriptionID, !req.Immediate); err != nil {
		writeError(w, http.StatusBadGateway, "failed to cancel with provider")
		return
	}

	// In test mode the provider cannot send us a real async webhook, so we drive
	// the resulting state through the same verified path a real provider's webhook
	// would take. A real provider would instead deliver its own webhook and this
	// block would not run (the simulator is absent).
	if sim, ok := h.provider.(billing.Simulator); ok {
		evt := billing.Event{
			UserID:                 userID,
			ProviderSubscriptionID: sub.ProviderSubscriptionID,
		}
		if req.Immediate {
			evt.Type = billing.EventSubscriptionCanceled
			evt.Status = "canceled"
			evt.Tier = "free"
		} else {
			// Still active, but flagged to end at the current period's close. Tier
			// and renewal date are preserved so the profile can say "cancels on …".
			evt.Type = billing.EventSubscriptionActive
			evt.Status = "active"
			evt.Tier = sub.Tier
			evt.CurrentPeriodEnd = sub.CurrentPeriodEnd
			evt.CancelAtPeriodEnd = true
			evt.PaymentLast4 = sub.PaymentLast4
		}
		if status, err := h.simulateAndIngest(sim, evt); err != nil {
			writeError(w, status, "failed to apply cancellation")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// Webhook is the only route that changes paid state. It is unauthenticated by
// necessity — the provider does not carry our JWT — so trust comes entirely from
// the signature. The body is verified, deduplicated, and processed; a forged or
// unsigned payload is rejected before anything is read from it (spec 2.3, 2.4).
func (h *BillingHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	status, err := h.ingest(body, r.Header.Get(signatureHeader))
	if err != nil {
		// Signature failures are the expected rejection; everything else is ours.
		if errors.Is(err, billing.ErrInvalidSignature) {
			writeError(w, http.StatusBadRequest, "invalid signature")
			return
		}
		log.Printf("billing webhook processing failed: %v", err)
		writeError(w, status, "failed to process webhook")
		return
	}
	writeJSON(w, status, map[string]bool{"received": true})
}

// TestComplete simulates a customer finishing the hosted checkout, so the whole
// upgrade path can be exercised without a real processor. It exists only in test
// mode: a real (non-Simulator) provider makes this route 404, which is how test
// mode stays a deliberate, detectable state (spec 2.5). Even here, nothing is
// trusted directly — the simulated event is signed and then run through the exact
// same verify path as a production webhook.
func (h *BillingHandler) TestComplete(w http.ResponseWriter, r *http.Request) {
	sim, ok := h.provider.(billing.Simulator)
	if !ok || !h.testMode {
		http.NotFound(w, r)
		return
	}
	userID := GetUserID(r.Context())

	var req struct {
		Tier string `json:"tier"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Tier == "" {
		req.Tier = proTier
	}

	user, err := h.store.GetUserByID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	customer, err := h.ensureCustomer(r, userID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare customer")
		return
	}

	periodEnd := time.Now().Add(30 * 24 * time.Hour)
	evt := billing.Event{
		Type:                   billing.EventSubscriptionActive,
		UserID:                 userID,
		ProviderCustomerID:     customer.ProviderCustomerID,
		ProviderSubscriptionID: "sub_mock_" + strconv.FormatInt(userID, 10),
		Tier:                   req.Tier,
		Status:                 "active",
		CurrentPeriodEnd:       &periodEnd,
		PaymentLast4:           "4242",
	}
	if status, err := h.simulateAndIngest(sim, evt); err != nil {
		writeError(w, status, "failed to complete test upgrade")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// simulateAndIngest signs an event as the provider would, then feeds it through
// the real ingest path — verify, dedupe, process. Used by the test-complete and
// (in test mode) cancel flows so state only ever changes via a verified webhook.
func (h *BillingHandler) simulateAndIngest(sim billing.Simulator, evt billing.Event) (int, error) {
	payload, signature, err := sim.SimulateEvent(evt)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	return h.ingest(payload, signature)
}

// ingest is the trusted core shared by the real webhook and the test path. It
// verifies the signature, records the event as an audit row whose UNIQUE
// provider_event_id doubles as the idempotency gate, and processes it only if it
// was newly recorded. A duplicate delivery is acknowledged without re-processing
// (spec 2.4). Returns an HTTP status and error for the caller to surface.
//
// Ordering note: the audit insert is the idempotency reservation and precedes
// processing, so a processing failure after a successful insert would not be
// retried by a later duplicate. That is acceptable for the test-mode foundation;
// making insert+process atomic (a transaction) is a hardening item for before any
// live charge (the Phase 1 gate this build stays behind).
func (h *BillingHandler) ingest(payload []byte, signature string) (int, error) {
	evt, err := h.provider.VerifyWebhook(payload, signature)
	if err != nil {
		return http.StatusBadRequest, err
	}
	isNew, err := h.store.InsertBillingEvent(store.BillingEvent{
		UserID:          evt.UserID,
		ProviderEventID: evt.ID,
		Type:            string(evt.Type),
		Payload:         evt.Raw,
	})
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if !isNew {
		// Already seen and processed — idempotent no-op.
		return http.StatusOK, nil
	}
	if err := h.processEvent(evt); err != nil {
		return http.StatusInternalServerError, err
	}
	return http.StatusOK, nil
}

// processEvent applies a verified event to our tables. It is the only writer of
// tier, and it derives tier only from the event — never from a client. The audit
// row was already written by ingest before this runs.
func (h *BillingHandler) processEvent(evt billing.Event) error {
	switch evt.Type {
	case billing.EventSubscriptionActive:
		status := evt.Status
		if status == "" {
			status = "active"
		}
		if err := h.store.UpsertSubscription(store.Subscription{
			UserID:                 evt.UserID,
			ProviderSubscriptionID: evt.ProviderSubscriptionID,
			Status:                 status,
			Tier:                   evt.Tier,
			CurrentPeriodEnd:       evt.CurrentPeriodEnd,
			CancelAtPeriodEnd:      evt.CancelAtPeriodEnd,
			PaymentLast4:           evt.PaymentLast4,
		}); err != nil {
			return err
		}
		return h.store.SetTier(evt.UserID, evt.Tier)

	case billing.EventSubscriptionCanceled:
		if err := h.store.UpsertSubscription(store.Subscription{
			UserID:                 evt.UserID,
			ProviderSubscriptionID: evt.ProviderSubscriptionID,
			Status:                 "canceled",
			Tier:                   "free",
			CurrentPeriodEnd:       evt.CurrentPeriodEnd,
			CancelAtPeriodEnd:      false,
			PaymentLast4:           evt.PaymentLast4,
		}); err != nil {
			return err
		}
		return h.store.SetTier(evt.UserID, "free")

	case billing.EventPaymentFailed:
		// Recorded for audit only. Tier is left intact — a single failed charge is
		// not a downgrade; the provider sends a cancellation if it ultimately lapses.
		return nil

	default:
		// Unknown event types are audited (already inserted) but not acted on.
		return nil
	}
}
