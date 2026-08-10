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
- [ ] Exactly one customer record exists per user, confirmed directly, even after repeated upgrade attempts or abandoned checkout flows
- [ ] Pro-tier access is confirmed to activate only after a webhook confirms successful payment — tested by simulating a delayed or failed webhook and confirming access is not granted prematurely
- [ ] A forged or unsigned webhook call is confirmed to be rejected
- [ ] A duplicate delivery of the same webhook event is confirmed not to cause double-processing
- [ ] All payment activity during testing is confirmed to be running in the provider's test mode, checked directly against the provider's dashboard
- [ ] The profile page is confirmed to display accurate tier, renewal date, payment-method-on-file, and billing history, sourced from the same records that will drive enforcement
- [ ] A user is confirmed able to cancel or downgrade their own subscription entirely through the product, with the resulting state correctly reflected afterward
- [ ] A canceled or failed subscription is confirmed to result in correctly downgraded access
- [ ] Provider API keys and webhook secrets are confirmed to follow Phase 1's credential hygiene rules directly
- [ ] Billing-relevant events (creation, failure, cancellation) are confirmed to be logged in a way that can genuinely be audited later

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through real test-mode transactions, simulated webhook events, and a genuine forged-webhook rejection test — not by reading the integration code and assuming correctness.
- Profile management is confirmed to show the user their real, current billing state at all times, with no observed drift from what the backend actually holds.
- This phase involves real payment infrastructure — verification here should match the elevated rigor already required for Phase 9's enforcement logic (now Phase 10), not a lighter pass.

**Do not proceed to Phase 10 (usage enforcement, built on top of this foundation) until every box above is genuinely checked, not estimated.**
