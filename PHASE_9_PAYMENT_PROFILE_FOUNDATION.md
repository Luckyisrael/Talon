# SeekerStudio — Phase 9: Payment Provider & Account Foundation

**Objective:** Before any tier limit can be meaningfully enforced, the platform needs a real payment provider wired in and a genuine account/profile layer — customer records, payment method collection, subscription lifecycle, and a place for users to see and manage their own billing state. This phase builds that foundation. Usage enforcement (Phase 10) is built on top of it, not alongside it.

**Reference:** `PHASE_1_SECURITY_HYGIENE.md` (credential hygiene applies directly here), `PHASE_9_FREEMIUM_BILLING.md` — **renumbered to Phase 10**, its enforcement logic now depends on this phase existing first.

---

## 0. Gate Condition — Do Not Start Without This

Phase 1 (Security & Hygiene Triage) must be complete and independently verified. This phase handles real payment data and account credentials — it must be built on an already-hardened security base, not run ahead of it.

---

## 1. Scope & Non-Goals

**In scope:** payment provider integration (customer records, hosted payment method collection, subscription products for Free/Pro), webhook handling as the source of truth for subscription state, and profile management (view/edit account, view tier and billing history, manage or cancel a subscription).

**Explicitly out of scope — do not touch in this phase:**
- Usage metering and limit enforcement — that's Phase 10. This phase only makes tier and payment state *available*; it does not decide what a user is allowed to do based on it.
- Crypto/wallet-connection-based payment — explicitly a future direction, not built now. This phase must not be architected in a way that makes adding it later painful, but it is not part of this phase's deliverable.
- APK export gating — depends on tier status this phase produces, but the export capability itself is a later phase entirely.

If a change would touch device-minute counting, prompt budgets, or export build tooling, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Raw card data never touches the backend
All payment method collection happens through the payment provider's own hosted, PCI-compliant UI or SDK (e.g. Stripe Elements/Checkout). The backend only ever stores provider-issued tokens, customer IDs, and subscription IDs — never a raw card number, ever, under any circumstance.

### 2.2 One canonical customer record per user
A payment-provider customer object is created once per user, at the first billing-relevant interaction, and linked one-to-one with that user's account. Repeated upgrade attempts or re-visits to a billing page must never create duplicate customer records.

### 2.3 Subscription state is driven by provider webhooks, not client claims
The backend must never grant Pro-tier access because a client-side call says "payment succeeded." Access changes only in response to a verified webhook event from the payment provider confirming the actual state — successful payment, failed payment, or cancellation.

### 2.4 Webhook handling is signature-verified and idempotent
Every incoming webhook must be verified against the provider's signature before being trusted — unsigned or forged webhook calls must be rejected outright. Duplicate delivery of the same event (which providers do send, by design) must not cause double-processing — no double-granting access, no duplicate billing-history entries, no duplicate notifications.

### 2.5 Test mode is the default throughout development
All payment processing during development and testing uses the provider's test mode. Switching to live payment processing must be a single, deliberate, reviewed action — never an accidental default or something that happens implicitly because an environment variable was missing.

### 2.6 Profile management reflects real, current state
The account/profile view must show a user's actual current tier, renewal date, payment method on file (last four digits only, never the full number), and billing history — sourced from the exact same data Phase 10's enforcement logic will use. There must be one source of truth for this state, not a display that could drift out of sync with what's actually enforced.

### 2.7 Users can cancel or downgrade their own subscription
A user must be able to cancel or downgrade their subscription entirely through the product itself, without requiring manual backend intervention from you or your team.

### 2.8 The integration sits behind an internal abstraction, not hardwired to one provider
Payment provider calls should go through an internal interface general enough that a second payment method — including a future wallet-based path — could be added later without a full rewrite of the billing logic. This is not built now; it's a constraint on how this phase is built, so that door isn't accidentally closed.

### 2.9 Provider credentials follow Phase 1's hygiene rules exactly
API keys and webhook signing secrets are held to the same standard already established in Phase 1 — no plaintext in any tracked file, gitignored, and treated as fully compromised and requiring rotation if ever exposed.

### 2.10 Every billing-relevant event is logged auditably
Subscription creation, payment failure, and cancellation must each leave a real, inspectable trail. Payment disputes and support questions will happen — there must be a genuine record to look back on, not just current state with no history.

---

## 3. Verification Checklist

- [ ] Initiating an upgrade is confirmed to never transmit or store raw card data on the backend — verified by directly inspecting what's actually sent and stored, not assumed from using the provider's SDK
- [x] Exactly one customer record exists per user, confirmed directly, even after repeated upgrade attempts or abandoned checkout flows
- [x] Pro-tier access is confirmed to activate only after a webhook confirms successful payment — tested by simulating a delayed or failed webhook and confirming access is not granted prematurely
- [x] A forged or unsigned webhook call is confirmed to be rejected
- [x] A duplicate delivery of the same webhook event is confirmed not to cause double-processing
- [ ] All payment activity during testing is confirmed to be running in the provider's test mode, checked directly against the provider's dashboard
- [ ] The profile page is confirmed to display accurate tier, renewal date, payment-method-on-file, and billing history, sourced from the same records that will drive enforcement
- [ ] A user is confirmed able to cancel or downgrade their own subscription entirely through the product, with the resulting state correctly reflected afterward
- [x] A canceled or failed subscription is confirmed to result in correctly downgraded access
- [ ] Provider API keys and webhook secrets are confirmed to follow Phase 1's credential hygiene rules directly
- [x] Billing-relevant events (creation, failure, cancellation) are confirmed to be logged in a way that can genuinely be audited later

**Verification Progress (2026-08-22):**

Six of the eleven boxes are now checked, on the strength of an automated integration suite — `backend/internal/api/billing_test.go` (5 tests / 9 cases, all passing) — that exercises the trusted server-side path against a real store and the mock provider, alongside green `go build ./...`, `go vet ./...`, and `go test ./...` gates. The unchecked items each need a browser or a repository-hygiene inspection that a Go test cannot honestly stand in for. Legend: ✅ = runtime-verified by an automated test; 🔨 = implemented in code, not yet exercised; ⬜ = pending a runtime gate.

- 🔨 **#1 (no raw card data):** No card field exists anywhere — backend stores only provider-issued IDs and a last-4 (`internal/store/billing.go` `Customer`/`Subscription`). The mock provider stands in for the hosted checkout; in test mode there is literally nothing that accepts a card number. *Still to verify:* inspect an actual request/DB at runtime — a Go test cannot prove the absence of a field the code never defines.
- ✅ **#2 (one customer per user):** `TestBillingOneCustomerPerUser` — three `StartUpgrade` calls (repeated attempts / abandoned checkouts) leave exactly one customer row carrying the deterministic user-scoped id; `UNIQUE(user_id)` is the hard backstop.
- ✅ **#3 (access only after webhook):** `TestBillingUpgradeGrantsTierOnlyThroughVerifiedWebhook` — `StartUpgrade` returns a checkout URL but leaves the tier **Free**; it flips to Pro only after a signed `subscription.active` event is ingested. Reinforced by #4: an event that fails verification grants nothing.
- ✅ **#4 (forged/unsigned rejected):** `TestBillingForgedWebhookRejected` — empty, wrong, and tampered-body signatures each return `ErrInvalidSignature` (HTTP 400 at the `/v1/billing/webhook` surface), leaving the tier unchanged and writing no audit row.
- ✅ **#5 (duplicate delivery idempotent):** `TestBillingDuplicateWebhookIdempotent` — the identical signed event ingested twice grants once and writes a single `billing_events` row.
- 🔨 **#6 (test mode default):** The provider is wired as `billing.NewMock(...)` in `cmd/server/main.go`; going live is a deliberate provider swap, and `test/complete` 404s under any non-Simulator provider. Inherently test-mode — there is no live dashboard to check against, so this stays open until a real provider is wired.
- 🔨 **#7 (profile reflects real state):** The *data source* is now test-backed — #3 asserts the subscription row carries the correct `payment_last4` (`4242`) and a future `current_period_end`. The `/settings` *render* of tier/renewal/last-4/history is still to be eyeballed in a browser.
- 🔨 **#8 (self-serve cancel/downgrade):** The *backend* is now verified — `TestBillingSelfServeCancel` drives `POST /v1/billing/cancel` and confirms cancel-at-period-end keeps Pro (with `cancel_at_period_end` set) while immediate cancel downgrades now. The box stays open on the `/settings` UI wiring, which a Go test can't drive.
- ✅ **#9 (canceled/failed → downgraded):** `TestBillingCancellationDowngrades` + `TestBillingSelfServeCancel/immediate` — a verified `subscription.canceled` returns the user to Free (subscription status `canceled`). Note: a single `payment.failed` is audit-only *by design* — the provider sends a cancellation if the subscription ultimately lapses, and that cancellation is the downgrade trigger.
- ✅/🔨 **#10 (auditable events + hygiene):** *Audit half — ✅:* every successful and duplicate delivery populates `billing_events` (once each) and forged deliveries write nothing, asserted throughout the suite. *Hygiene half — 🔨:* the webhook secret is read from env in `main.go` and the mock holds no real keys, but "secrets stay out of git" is a repository-inspection check, not run here.

**⬜ Outstanding runtime gates (what's left for a full §3 sign-off):**
- Backend gates are **green**: `go build ./...`, `go vet ./...`, and `go test ./...` all pass, including the new `internal/api/billing_test.go` suite and the `internal/store/driver.go` fix that registers the SQLite driver in the store package so any consumer (not just the server binary) can open the store. Frontend `next build` was green earlier this session (14 routes, incl. `/checkout`, `/pricing`, `/settings`).
- A browser pass for the display-and-click items a Go test can't cover: #1 (inspect a real request/DB for absence of card data), #7 (`/settings` shows real tier/renewal/last-4/history), #8 (cancel *through the UI*).
- A credential-hygiene check for the other half of #10: confirm the webhook secret and any provider keys are gitignored and never tracked.

**In-product upgrade entry points (deliberate deviation from the plan's wording):** the working upgrade path lives in `/settings` (the authenticated account home), `/checkout` (unmistakably labeled *test mode*), and a **signed-in-only** Pro CTA on `/pricing`. The plan named `/pricing` & `/waitlist`; `/waitlist` was intentionally **left unchanged** — it is the Team/Enterprise *contact-sales* path, where "self-serve upgrade" does not apply, and per "the UI must not lie" an anonymous pricing visitor keeps the honest "Join the waitlist" link rather than a purchase button that does not yet transact.

**Test-mode-only caveat (rule 2.5 + §0 gate):** the §0 gate (Phase 1 complete + independently verified) is being bypassed per the owner's explicit call. This foundation therefore stays **test-mode only**; the live-payment switch must not be flipped until Phase 1 is complete and independently verified.

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through real test-mode transactions, simulated webhook events, and a genuine forged-webhook rejection test — not by reading the integration code and assuming correctness.
- Profile management is confirmed to show the user their real, current billing state at all times, with no observed drift from what the backend actually holds.
- This phase involves real payment infrastructure — verification here should match the elevated rigor already required for Phase 9's enforcement logic (now Phase 10), not a lighter pass.

**Do not proceed to Phase 10 (usage enforcement, built on top of this foundation) until every box above is genuinely checked, not estimated.**
