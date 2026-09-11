# Talon — Phase 10: Freemium Enforcement & Billing

**Objective:** Turn usage tracking into usage *enforcement*. Free and Pro tiers must be real, hard limits — not numbers recorded for later reporting while every user effectively has unlimited access. This phase also builds the idle-pause and warm-pool lifecycle that Phase 4 deliberately deferred, and wires enforcement on top of the same metering data used for billing.

**Reference:** `PROJECT_DOCUMENTATION.md` (usage tracking, `/v1/usage`), original `IMPLEMENTATION_ROADMAP.md` Phase 6, `PHASE_4_MULTISESSION_REDROID.md` §1 (warm pool explicitly deferred here), `PHASE_9_PAYMENT_PROFILE_FOUNDATION.md` (this phase's billing logic runs on top of that foundation)

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 4 must be complete and independently verified — this phase depends directly on Phase 4's container lifecycle (pause/destroy) being solid, since idle-pause and the warm pool are extensions of that same lifecycle, not a separate system. Phase 9 (Payment Provider & Account Foundation) must also be complete — this phase enforces limits and charges overage using the customer records, subscription state, and webhook-driven payment status that Phase 9 establishes. Do not build enforcement against a payment system that doesn't exist yet.

---

## 1. Scope & Non-Goals

**In scope:** real-time tier enforcement, race-safe metering, idle-pause and destroy thresholds, the warm-pool fast-resume mechanism, billing integration driven by real metering data, and usage visibility in the frontend.

**Explicitly out of scope — do not touch in this phase:**
- Building the APK export capability itself — that's Phase 11. This phase only defines the rule that export must be tier-gated at the API layer; it does not build export.
- Any change to how sessions are created or containers are isolated — that's settled in Phase 4. This phase extends the lifecycle states (pause, warm, destroy), it does not redesign session creation.

If a change would touch export build tooling or container isolation itself rather than lifecycle/billing, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Tier resolution is enforced on every relevant request, not just recorded
Every request that creates a session or triggers an AI prompt must check the user's current tier and consume against their metered budget in real time. Logging usage for later reporting, without actually blocking at the limit, does not satisfy this phase.

### 2.2 Free and Pro budgets are hard limits
Free tier is capped at its defined device-minutes and prompt count; Pro tier is capped at its higher budget with defined overage handling beyond that. These figures must live in configuration (§2.11), but whatever they're set to, they must be genuinely enforced — access is actually blocked or paused at the limit, not merely flagged.

### 2.3 Metering is race-safe
Concurrent requests from the same user must never be able to exceed their budget due to a race condition in how usage is counted. Usage-consumption updates must be atomic under concurrent access — this must be tested with genuinely simultaneous requests, not sequential ones.

### 2.4 Idle sessions pause automatically
A session idle beyond a defined threshold must automatically transition its container to a paused state — a real, working lifecycle transition built on Phase 4's container management, not a database flag with no corresponding effect on the running container or its resource consumption.

### 2.5 Long-idle sessions are fully destroyed
A session that remains paused beyond a further threshold must be fully destroyed, with its resources reclaimed, consistent with the teardown guarantees already established in Phase 4. Paused sessions must not be allowed to accumulate indefinitely and quietly consume host capacity.

### 2.6 Fast resume requires a genuine warm pool
A small number of pre-booted, ready containers must be kept warm per host so that resuming a paused session (or starting a new one) is fast — this must be a real, measured resume time, not an aspiration. The warm pool must replenish itself automatically as it's drawn from; it must never be allowed to silently run dry and revert every session start to a cold boot.

### 2.7 Overage billing is driven by the same metering data used for enforcement, through the Phase 9 foundation
There must be exactly one source of truth for usage. Whatever data enforces the free/Pro limits in §2.1–2.3 is the same data that drives overage charges — and those charges are submitted through the payment-provider integration and customer records already established in Phase 9, never through a separate or duplicate billing path. Two independently tracked usage mechanisms that could disagree with each other is not acceptable.

### 2.8 Usage is visible to the user before they hit a limit
The frontend must surface current usage against the user's budget in a way they can see ahead of time — a user should never be blocked with no prior indication they were approaching their limit.

### 2.9 Tier-gated features are enforced at the API layer
Any feature restricted to a tier — export access being the clearest current example, even though export itself is built later — must be rejected server-side for an ineligible user regardless of what the frontend does or doesn't show. Hiding a button in the UI is not enforcement.

### 2.10 Billing failures are handled gracefully, not silently or destructively
If a payment fails for a Pro user's overage, the system must follow a defined, non-destructive response — it must not silently continue granting unlimited usage, and it must not abruptly destroy an active session mid-use without warning.

### 2.11 All limits, thresholds, and rates are configuration, not hardcoded values
Tier budgets, overage rates, idle-pause timing, and warm-pool size must be defined in configuration that can be changed without a code change scattered across multiple files. These numbers will change as the product matures; the code must be built expecting that.

---

## 3. Verification Checklist

- [ ] A free-tier user is confirmed to be genuinely blocked, not merely warned, upon exhausting their prompt and device-minute budget — tested by actually exhausting it
- [ ] A Pro-tier user is confirmed to exceed the free-tier limits and correctly enter overage billing, tested directly
- [ ] Rapid, genuinely concurrent requests from the same user are confirmed not to allow usage to exceed budget — tested with simultaneous requests, not sequential ones
- [ ] An idle session is confirmed to automatically pause after its threshold, verified by observing the container's real state, not a database flag
- [ ] A session left paused past the further threshold is confirmed to be fully destroyed with resources reclaimed
- [ ] Resuming a session is confirmed to be meaningfully fast due to the warm pool, with an actual measured resume time recorded
- [ ] The warm pool is confirmed to replenish automatically after being drawn from — tested by consuming it and observing recovery, not assumed
- [ ] Billing charges are confirmed to be driven by the same metering data used for enforcement — checked directly for any discrepancy between what's billed and what's enforced
- [ ] The frontend is confirmed to display current usage against budget before a limit is reached, not only at the moment of being blocked
- [ ] A tier-gated feature is confirmed to be rejected server-side for an ineligible user even when the API is called directly, bypassing the frontend
- [ ] A simulated failed payment is confirmed to trigger the defined graceful handling — not silent unlimited access, and not an abrupt destructive action against an active session
- [ ] A direct search confirms tier budgets, overage rates, and lifecycle thresholds are defined in configuration, not hardcoded throughout the codebase

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct, adversarial testing — actually exhausting a budget, actually racing concurrent requests, actually failing a payment — not by reviewing the design and assuming correctness.
- This phase involves real money and real resource limits; verification here should be held to a higher bar than earlier phases, not a lighter one.
- The warm pool and idle-pause/destroy lifecycle are confirmed working together correctly, without any regression to Phase 4's core isolation guarantees.

**Do not proceed to Phase 11 until every box above is genuinely checked, not estimated.**
