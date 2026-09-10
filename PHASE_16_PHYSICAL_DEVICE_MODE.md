# SeekerStudio — Phase 16: Physical Device Development Mode

**Objective:** Let a user run agent-generated code on their own physical Android device — including a real Seeker device — by sideloading the project's dev client and connecting to a live Metro session via QR code, as a complement to the Redroid emulator preview. This trades some of the agent's automated self-verification capability for real hardware fidelity, and is the first point in the product where Seed Vault-dependent code becomes genuinely testable.

**Reference:** `PHASE_5_NATIVE_DEVCLIENT.md`, `PHASE_7_ENGINEERING_INTELLIGENCE.md` (§2.8 self-verification), `PHASE_8_MOCK_WALLET_MWA.md` §2.7 (Seed Vault limitation this phase partially resolves), `PHASE_1_SECURITY_HYGIENE.md` (tunnel exposure rules)

---

## 0. Gate Condition — Do Not Start Without This

Phase 5 (Native Dev-Client Baking) and Phase 7 (Engineering Intelligence) must be complete and independently verified. This is an additive feature layered on top of both — it is not part of the critical path to launch and should not block or delay Phases 9 through 15.

---

## 1. Scope & Non-Goals

**In scope:** distributable dev-client packaging, a secure per-session Metro tunnel, QR code/deep-link connection flow, tunnel lifecycle management, and the explicit handling of reduced agent verification when a user is on their own device.

**Explicitly out of scope — do not touch in this phase:**
- Any change to the Redroid emulator or its video/input pipeline — this feature is additive to Phase 6, not a replacement for it.
- Building any form of remote screen-mirroring or automated inspection of a user's own physical device — the agent is not meant to gain "eyes" on hardware it doesn't control. The verification gap this creates is handled by being explicit about it (§2.6), not by trying to eliminate it.
- Fully automated, scan-free deployment — a manual QR scan or deep-link tap to connect is expected and fine; this phase does not need to remove that step.

If a change would touch Redroid's own streaming pipeline or attempt to give the agent visibility into a user's personal device, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 The dev client is independently distributable, not only baked into Redroid
The exact same dev-client artifact Phase 5 bakes into the project's Redroid template must also be packaged as a standalone, sideloadable APK a user can download directly. This is one binary with two distribution paths — never two separately built artifacts that could drift out of sync with each other.

### 2.2 Metro exposure is per-session, scoped, and authenticated
The tunnel exposing a session's Metro bundle endpoint to the public internet must be scoped to that specific session and require that session's own credential to connect. A raw, unauthenticated port forwarded to the internet is not acceptable under any circumstance — this is held to the same standard as any other externally reachable endpoint under Phase 1.

### 2.3 Tunnels are torn down with their session, not left dangling
A Metro tunnel must not outlive the session it belongs to. When a session ends — whether the user closes it explicitly or Phase 9/10's idle-timeout lifecycle destroys it — the tunnel is torn down immediately. A stale, still-reachable tunnel from a dead session is a real security gap, not a harmless leftover.

### 2.4 The QR code embeds authentication, not just a location
The QR code or deep link presented to the user must encode whatever token or credential the dev client needs to authenticate to that specific session — never a bare URL that anyone who happened to see the code could also connect to.

### 2.5 Physical-device mode is additive, never the sole or default path
Redroid-based preview remains the default, always-available experience. Physical-device mode is something a user opts into — the product must never assume every user has a Seeker device or is willing to sideload an APK, and must work completely without this feature for anyone who doesn't.

### 2.6 Reduced verification is disclosed plainly, never silently degraded
The agent cannot screenshot or read logs from a user's own physical device the way it can with Redroid. The moment a user enables physical-device mode, this must be stated to them clearly — not discovered later when a bug slips through unnoticed. Per `PHASE_7_ENGINEERING_INTELLIGENCE.md` §2.8, the agent's own reporting must reflect this: a change verified only via physical-device mode is communicated with visibly different, more cautious language than a change the agent verified automatically against Redroid.

### 2.7 Parallel Redroid verification runs alongside physical-device mode when capacity allows
Where server capacity permits, a lightweight Redroid instance for the same project should continue running in parallel with a user's physical-device connection, preserving the agent's automated self-healing loop even while the user watches changes land on their own hardware. This is a capacity-dependent optimization the architecture should support, not a hard requirement every session must meet.

### 2.8 This is the intended validation path for Seed Vault and other real-hardware-only behavior
Per `PHASE_8_MOCK_WALLET_MWA.md` §2.7, Seed Vault cannot be exercised in Redroid at all. This phase is the first point where a user connected via a real Seeker device can actually test Seed Vault-dependent code. The agent's context and guardrails must reflect this distinction — Seed Vault code is no longer purely "unverified, best-effort" once genuinely exercised on real hardware through this path.

### 2.9 The distributed dev client matches the project's pinned template version exactly
Per Phase 5's versioning rules, the sideloadable dev client must be the exact same version as the project's baseline `template:vN` — never a different or newer build that could introduce a native-module mismatch against what the project's generated code actually expects.

### 2.10 Tunnel infrastructure stays lightweight
Since part of this feature's purpose is relieving pressure on constrained emulator capacity, the tunnel/relay mechanism itself must not become a new resource bottleneck in its place — it should be cheap enough, per session, that adopting this feature is a genuine relief valve rather than trading one capacity problem for another.

---

## 3. Verification Checklist

- [ ] A project's dev client is confirmed downloadable as a standalone APK, independent of any active Redroid session
- [ ] The sideloaded dev client is confirmed to be the exact same build as what's baked into that project's Redroid template — checked directly, not assumed
- [ ] Scanning a session's QR code from a real physical device connects that device to the correct session's Metro server — tested with two concurrent sessions to confirm no cross-session connection is possible
- [ ] Attempting to connect using a tampered or missing token is confirmed to be rejected
- [ ] Ending a session (explicitly or via idle timeout) is confirmed to tear down its tunnel — a previously valid QR code or link is confirmed to fail afterward
- [ ] A user in physical-device mode is confirmed to see a clear, explicit statement that automated verification is reduced — not silence on the matter
- [ ] A code change tested only via physical-device mode is confirmed to be reported by the agent with visibly more cautious language than a Redroid-verified change
- [ ] Where parallel Redroid verification is active, it's confirmed to continue functioning correctly alongside a live physical-device connection for the same project
- [ ] A Seed Vault-dependent code path is confirmed genuinely testable end-to-end on a real Seeker device via this mode

---

## 4. Definition of Done

- Every item in §3 is independently confirmed using a real physical device — ideally real Seeker hardware, or at minimum a real Android phone — not simulated or assumed from the design.
- The tunnel and QR flow are confirmed secure against cross-session access and reuse after session teardown, tested directly with adversarial attempts, not just the happy path.
- A tester using physical-device mode is confirmed to understand, without being told separately, that they're seeing something the agent cannot automatically verify — the in-product messaging alone should make this clear.

**This phase is additive and does not block launch. Build it once the core beta is stable, not as a prerequisite to shipping one.**
