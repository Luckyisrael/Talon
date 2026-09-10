# SeekerStudio — Phase 17: Companion Mobile App

**Objective:** A genuinely native Seeker companion app — not a compressed IDE port — that lets a user create projects, chat with the agent, review and approve plans, get notified of build status, authenticate via their Solana Mobile wallet, and hand off directly into live testing on the sideloaded dev client. Code viewing, the full execution trace, and terminal access stay exclusively on the web IDE, by design. This app is a strictly additive entry point alongside the existing web IDE — not everyone has a Seeker device, and not everyone who does will want it; the web experience, including Phase 16's own physical-device handoff, must remain fully capable on its own. This is built to be a standalone, winnable hackathon submission demonstrating deep, idiomatic Solana Mobile Stack integration.

**Reference:** `PHASE_7_ENGINEERING_INTELLIGENCE.md` (Architecture Decision Record, event schema), `PHASE_9_PAYMENT_PROFILE_FOUNDATION.md` (existing account system this app's auth links to), `PHASE_16_PHYSICAL_DEVICE_MODE.md` (dev-client sideload and handoff mechanism)

---

## 0. Gate Condition

This app depends conceptually on Phase 7's plan/ADR structure and event schema, Phase 9's account system, and Phase 16's dev-client sideload mechanism existing in some working form — they do not need to be fully production-hardened, but the underlying data and mechanisms this app surfaces must genuinely exist and function.

---

## 1. Scope & Non-Goals

**In scope:** native chat/create flow, plan approval, push notifications, wallet-based authentication, project/session status list, and same-device handoff to the dev client.

**Explicitly out of scope — permanently, not just for now:**
- Code viewing, file tree, multi-file diffs, or terminal access — these remain exclusive to the web IDE. This is a design decision, not a temporary limitation to revisit later.
- Building or altering the payment/billing system — wallet-based sign-in here is about identity, not payment. Phase 9's Stripe-based billing foundation is untouched by this phase.
- The full, granular execution trace from `AGENT_ENGINE_SPEC_ADDENDUM.md` §11 — mobile shows meaningful status summaries, not the Reading/Creating/Updating blow-by-blow.

If a change would add code editing, a file browser, or touch how billing works, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Wallet-based sign-in is the primary, native authentication path
Rather than porting a password/JWT login form to a phone screen, the companion app authenticates via Mobile Wallet Adapter: the user connects their wallet and signs a challenge message, which the backend verifies to establish or link their session. This is the idiomatic mobile pattern for this platform, and a genuine, deep MWA integration — not a superficial wallet-connect button bolted on to check a box.

### 2.2 Wallet auth is about identity, not payment — this does not reopen Phase 9
Signing a message to prove wallet ownership and link to an account is a separate concern from how that account is billed. Phase 9's Stripe-based payment foundation stays exactly as specified. Do not conflate "sign in with wallet" with "pay with wallet," and do not use this phase as an occasion to revisit that earlier decision.

### 2.3 The companion app never attempts to replicate the web IDE's code surface
No code viewer, no file tree, no diff view, no terminal — permanently, not just because of time constraints. If a user needs any of these, the app's job is to clearly point them to the web IDE, not to offer a degraded, scrollable attempt at the same thing.

### 2.4 Chat and plan approval are the core interaction, built genuinely mobile-native
The prompt/chat interface and the plan-approval flow — presenting the Architecture Decision Record from Phase 7 in a clearly readable, summarized form (state management choice, folder convention, screens involved), with explicit approve, reject, and request-changes actions — are this app's central experience, designed as first-class native UI rather than a webview embedding the desktop chat panel.

### 2.5 Status is shown as a meaningful summary, never the full trace
The app surfaces status at the right level of abstraction for a phone: "Building…," "3 files updated," "Waiting for your approval," "Error — needs your input." The full multi-file, verb-by-verb trace stays on the desktop; mobile gets the headline, not the transcript.

### 2.6 Notifications work when the app is backgrounded or fully closed
Because a mobile OS suspends background network activity, status changes (build complete, plan ready for approval, error needing input) must be delivered as real push notifications — never an assumption that a persistent WebSocket survives backgrounding. Reopening the app reconciles against real current state; it does not rely on having stayed continuously connected.

### 2.7 Same-device handoff to the dev client uses a deep link, not a QR code
Phase 16's QR flow exists for cross-device handoff — a desktop session handing to a phone. When the companion app and the sideloaded dev client are both running on the same physical device, the handoff must be a direct Android deep link/intent carrying the same session credential a QR code would encode. A user should never be asked to scan their own device's screen with its own camera.

### 2.8 The handoff credential carries the same security properties regardless of delivery method
Whether delivered via deep link (same-device) or QR code (cross-device), the underlying credential is identical in its security properties, per Phase 16 §2.2–2.4: scoped to one session, authenticated, and expiring with that session. Only the delivery mechanism changes.

### 2.9 The session list reflects real, current state on every open
Reopening the app after being closed or backgrounded must reconcile against the backend's actual current state for every visible project — status shown must never be a stale value carried over from before the app was last open.

### 2.10 Visual design matches the established language, expressed natively
The app follows the same design principles as the web product — one dominant design language, Solana brand colors reserved for genuinely Solana-specific contexts, deliberate motion, fully designed interactive states — implemented as real native Android UI, not a webview wrapping the web app's styling.

### 2.11 Session credentials are stored securely on-device
Wallet-linked session tokens use Android's secure, Keystore-backed storage — never plaintext shared preferences or an unencrypted cache. This is held to the same severity as Phase 1's credential-hygiene rules.

### 2.12 The demo flow is rehearsed end to end, not assembled from untested parts
The complete flow — connect wallet, create a project by chatting, approve a plan, receive a notification when it's ready, hand off to the dev client, and see the app running natively on the same device — must be run successfully, back to back, more than once before submission. Individually working pieces that have never been tested together are not the same as a working demo.

### 2.13 The companion app is additive — it never replaces or diminishes the web IDE's existing handoff
Phase 16's QR-code handoff — from a web IDE session directly to any Android phone — is the primary, always-available physical-device testing path, and it must work exactly as well with the companion app never installed at all. Not every user has a Seeker device, and not every user who does will want a second app. The deep-link handoff in §2.7 exists only for the narrower case of someone already inside the companion app on the same device as their dev client — it is one additional shortcut for one specific context, not a replacement for the general, cross-device QR flow every web-only user depends on. Building the companion app must never become an occasion to weaken, gate, or deprioritize Phase 16's existing web-based path.

---

## 3. Verification Checklist

- [ ] Wallet-based sign-in is confirmed to authenticate via a real signed message, verified server-side — tested directly, not mocked
- [ ] Wallet auth is confirmed to link correctly to an existing account without touching or altering the payment method on file
- [ ] No code-viewing, file-tree, or terminal surface exists anywhere in the app — confirmed by direct inspection
- [ ] The plan approval flow is confirmed to present a real, readable Architecture Decision Record and correctly send approve/reject/request-changes back to the backend
- [ ] Status shown is confirmed to be a meaningful summary, never the raw multi-file trace
- [ ] A build-complete or error-needing-input event is confirmed to trigger a real push notification while the app is backgrounded or fully closed
- [ ] Reopening the app after being closed is confirmed to reconcile to real current state, not stale cached status
- [ ] The same-device handoff to the dev client is confirmed to use a deep link, tested directly — never a QR code requiring the user to scan their own screen
- [ ] The deep-link credential is confirmed to carry the same scoped, authenticated, expiring properties as Phase 16's QR flow
- [ ] Session token storage is confirmed to use secure, Keystore-backed storage, not plaintext
- [ ] The full demo flow — wallet connect through dev-client handoff — is confirmed to succeed in at least two consecutive, unassisted runs
- [ ] Phase 16's web-based QR handoff is confirmed to work exactly as before with the companion app fully uninstalled — tested directly on a separate device with no companion app present

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing on a real Seeker device, not simulated or assumed.
- The full demo flow has been rehearsed and proven reliable back to back, not assembled from parts tested only in isolation.
- The app reads, visually and functionally, as a genuine native Seeker experience built specifically for this platform — not a compressed or repurposed version of the web IDE.

**This phase is built to be demoed live in front of judges. Reliability of the full flow matters more than feature breadth — a smaller set of things that work flawlessly beats a longer list of things that mostly work.**
