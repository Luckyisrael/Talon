# SeekerStudio — Phase 8: Mock Wallet & MWA Testing Pipeline

**Objective:** Every session must start with a working Mobile Wallet Adapter test wallet already installed and configured — never a manual, one-off install performed after the fact. Generated apps must be able to complete a genuine end-to-end wallet connection and signing round trip, automatically, in every session, from a clean environment.

**Reference:** Solana Mobile development setup documentation (mock wallet / "fakewallet"), `PHASE_2_PROJECT_BOOTSTRAP.md`, `PHASE_4_MULTISESSION_REDROID.md`, `PHASE_5_NATIVE_DEVCLIENT.md`

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 4 must be complete and independently verified. This phase bakes into the same container image/template mechanism Phase 2 and Phase 4 established — building it before those exist would mean redoing the install strategy afterward. Phase 5 (native dev-client baking) does not need to be fully complete first, but the two should ideally land close together since both extend the same template artifact.

---

## 1. Scope & Non-Goals

**In scope:** building the mock MWA wallet APK, baking it into the session container image/template, automating any device-level configuration required for MWA sessions (such as a device PIN), and verifying a real end-to-end wallet round trip.

**Explicitly out of scope — do not touch in this phase:**
- Seed Vault testing — Seed Vault requires real Seeker hardware and cannot be exercised inside Redroid. This phase does not attempt to simulate it; it only ensures the AI is aware of that limitation (§2.7).
- Real wallet app integration (Solflare, Jupiter Mobile, etc.) for automated testing — the mock wallet is the automated-testing baseline. Real wallet compatibility is a manual, later concern, not part of this pipeline.
- APK export/signing for release — unrelated to test-time wallet infrastructure.

If a change would touch real hardware provisioning, Seed Vault APIs, or the release build pipeline, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 The mock wallet is built offline, never per-container
The mock MWA wallet APK is built once via an offline/CI pipeline — cloning the source and producing a build artifact — exactly as the dev client is in Phase 5. It must never be built live at the moment a new session or container starts.

### 2.2 The mock wallet is baked into the container image, not installed post-boot
Every new session's container must start with the mock wallet already present, as part of the container image or the template mount established in Phase 2 and Phase 4 — not a separate `adb install` step performed manually or ad hoc after the container is already running.

### 2.3 Required device configuration is automated, not manual
Any device-level setup required for MWA sessions to function correctly — such as a device PIN or lock-screen configuration — must be applied programmatically as part of session initialization. This must never depend on a human remembering to perform a manual step.

### 2.4 The mock wallet's version is pinned alongside the template version
Exactly like the dev client, the mock wallet build is versioned and tied to a specific `template:vN`. It must never be silently rebuilt or updated underneath a project that's already running — a version change is a deliberate action, not an incidental side effect.

### 2.5 Wallet state is fully isolated per session
Because Phase 4 already guarantees one container per session, this phase must specifically confirm that wallet-related state — keypairs, connection history, local storage — does not leak or persist across sessions. A wallet app carrying state forward from a previous session's container would be a meaningful isolation failure, not a cosmetic one.

### 2.6 A full connect-to-sign round trip must genuinely work
The pipeline is only considered functional when a generated app can: initiate a wallet connection, have the mock wallet present an approval prompt, and receive a valid signed transaction or signed message back in the generated app. Partial success — a connection that succeeds while signing silently fails, for example — does not count as working.

### 2.7 The AI must know Seed Vault cannot be tested here
Because Seed Vault is real-hardware-only, this must be encoded as a fact the AI's generation context is aware of, not left to be discovered by trial and error. Generated apps should default to MWA-based flows for anything this platform can actually verify before real hardware is involved, and any Seed Vault-specific code the AI produces must be understood — by the AI and by the user — as unverified, not something this pipeline confirms works.

### 2.8 The pipeline must be reproducible from a clean environment
Building the mock wallet and baking it into a fresh template must succeed starting from a clean native Ubuntu environment (per Phase 0), using only the already-established toolchain. No manual GUI-only step that can't be scripted may be a required part of this process.

### 2.9 A wallet setup failure must be a hard, visible failure
If a session's container ever starts without a correctly installed and configured mock wallet — due to a build failure, install failure, or configuration failure — this must surface as a clear, visible session-creation failure. A session that silently starts without working wallet-testing capability, while otherwise appearing normal, is not acceptable.

---

## 3. Verification Checklist

- [ ] The mock wallet APK is confirmed to be built via an offline/CI pipeline, independent of any specific session or container-creation event
- [ ] A freshly created session's container is confirmed to already have the mock wallet installed at first use — no separate post-boot install step is observed
- [ ] Device PIN or other MWA-required configuration is confirmed to be applied automatically during session initialization, with no manual step involved
- [ ] The mock wallet's version is confirmed tied to a specific template version and does not change unexpectedly underneath an already-created project
- [ ] Two concurrent sessions are confirmed to have fully independent wallet state — no shared keypair or session data observed between them, tested directly
- [ ] A generated app is confirmed able to complete a genuine connect → approve → sign → receive-signed-result round trip against the mock wallet, tested end to end on a real running session
- [ ] The AI's generation context is directly inspected and confirmed to reflect that Seed Vault cannot be tested in this environment, and that MWA is the default verifiable flow
- [ ] The full mock-wallet build-and-bake process is confirmed to run successfully from a clean native Ubuntu environment using only scripted, automatable steps
- [ ] A deliberately induced build or install failure for the mock wallet is confirmed to surface as a clear, visible failure — not a session that silently proceeds without working wallet support

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing — including a real, observed connect-to-sign round trip on a live session, not a theoretical description of how it should work.
- The mock wallet is confirmed present, configured, and isolated correctly across multiple concurrently created sessions.
- The system fails loudly and clearly when wallet setup genuinely fails, rather than degrading silently.

**Do not proceed to later phases until every box above is genuinely checked, not estimated.**
