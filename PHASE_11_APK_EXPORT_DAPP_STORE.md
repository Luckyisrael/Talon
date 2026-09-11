# Talon — Phase 11: APK Export & dApp Store Bundle

**Objective:** A user can take a project from the dev loop to a genuinely signed, submission-ready release artifact — a real signed APK, a correctly structured dApp Store manifest, and validated store assets — without Talon ever touching the user's own publisher wallet or private keys.

**Reference:** Solana Mobile dApp publishing documentation (`docs.solanamobile.com/dapp-publishing`, `docs.solanamobile.com/dapp-store`), `PHASE_3_IDENTITY_CONFIGURATION.md` §2.7, `PHASE_10_FREEMIUM_BILLING.md` §2.9, `FRONTEND_UI_UX_OVERHAUL.md` §3.5 (emulator toolbar screenshot capture)

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 5 must be complete and independently verified — this phase produces a real native build, and depends on the identity system (Phase 3) and native module baking (Phase 5) already working correctly. Phase 10 must also be complete, since export is a tier-gated feature and this phase implements the capability that Phase 10 only defined the gate for.

---

## 1. Scope & Non-Goals

**In scope:** the release build pipeline, per-project signing key management, dApp Store manifest generation, and validation of store assets (screenshots, icon, banner) against Solana's actual documented requirements.

**Explicitly out of scope — do not touch in this phase:**
- Actual submission to the Solana dApp Store — that action is the user's own, performed with their own wallet, either through the Publisher Portal or the `dapp-store-cli`. This phase produces what they'd submit; it does not submit on their behalf.
- The emulator toolbar UI itself — that's `FRONTEND_UI_UX_OVERHAUL.md` §3.5. This phase defines what happens to a screenshot *after* it's captured (validation, use as a store asset).
- Any change to the dev-loop build pipeline — export is a separate, isolated build process from Phase 5's dev client.

If a change would touch the user's publisher wallet, transaction signing, or the dev-loop's fast-path build behavior, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Export is a genuine, deliberate native release build
Export triggers a full release build (the equivalent of `gradlew assembleRelease` or a bundled AAB, as required) — never a reuse of the dev-client's debug build. Unlike Phase 5's dev loop, a native rebuild here is expected and correct; this is not the fast path, and should not be optimized as if it were.

### 2.2 The real, personalized identity is baked in only at this step
Per `PHASE_3_IDENTITY_CONFIGURATION.md` §2.7, this is the first and only point in the product where the project's actual, personalized bundle ID and scheme are compiled into a native artifact. Confirm this explicitly at export time — check the built artifact's real identity — rather than assuming it carried over correctly from the dev client, which runs under a different, fixed identity throughout development.

### 2.3 Signing keys are per-project, generated once, never shared
Each project gets its own signing keystore, generated at first export and persisted securely for all future exports of that same project — never a single signing key shared across every exported app. Consistent signing across a project's future updates is required both for correctness and because app stores expect it. Keystore storage follows the same credential-hygiene standard established in Phase 1 — never plaintext, access-controlled to the owning user only.

### 2.4 Export is rejected server-side for ineligible users
Consistent with `PHASE_10_FREEMIUM_BILLING.md` §2.9, the export endpoint itself must reject the request for a user without the required tier, regardless of what the frontend shows — this phase is what makes that existing rule concrete and testable.

### 2.5 The dApp Store manifest is generated from real project data
Manifest/config generation (structured to match what `@solana-mobile/dapp-store-cli` expects) is populated from the project's actual stored identity, actual generated app metadata, and catalog details the user has genuinely provided — never placeholder or dummy values a user would have to discover and fix manually before they could submit.

### 2.6 Screenshot assets are validated against Solana's real, documented requirements before use
Per Solana Mobile's published guidelines, a screenshot used as a store asset must be validated to be **at least 1080px in both width and height**, and every screenshot in the set must share **consistent orientation** (all portrait or all landscape) — this must be checked programmatically before a captured screenshot is accepted into the project's store-asset collection, not left for the user to discover as a rejection at actual submission time. Icon (**512×512px**) and banner (**1200×600px**) are separate asset types with their own fixed-dimension requirements — they are not screenshots, must not be conflated with the emulator's screenshot capture flow, and need their own upload or generation step.

### 2.7 Talon never touches the user's publisher wallet
Solana dApp Store submission is wallet-based: the publisher's own wallet signs the actual submission transactions and pays SOL for asset storage (typically via ArDrive) and the NFT minting the listing itself requires. Talon must never request, store, transmit, or use a user's publisher wallet private key, under any circumstance. This platform's responsibility ends at producing a correctly signed APK and a correctly structured bundle ready for the user to submit themselves, through the Publisher Portal or the CLI, with their own wallet.

### 2.8 Real costs are surfaced honestly, ahead of time
Because actual dApp Store publishing costs real SOL for storage and minting, the export flow must clearly communicate this to the user as part of the export experience — this should not be something they discover for the first time only after leaving Talon and reaching Solana's own submission flow.

### 2.9 Export never disrupts the active dev-loop session
Triggering an export build must run as a fully separate, isolated build process — it must not block, slow, or interfere with the user's ongoing prompt/generation work in the same project's dev session.

---

## 3. Verification Checklist

- [ ] A completed export produces a genuinely signed release artifact, confirmed by inspecting the artifact's actual signature — not assumed from the build succeeding
- [ ] The exported artifact's package/bundle identity is confirmed to match the project's real, personalized identity from Phase 3 — inspected directly in the built artifact
- [ ] Exporting the same project twice reuses the same signing keystore both times, confirmed directly
- [ ] Two different projects are confirmed to have distinct signing keystores
- [ ] A user without the required tier is confirmed to be rejected by the export endpoint directly, even when called outside the frontend
- [ ] A generated manifest is confirmed to contain the project's real identity and catalog details, with no placeholder values remaining
- [ ] A screenshot narrower or shorter than 1080px is confirmed to be rejected from the store-asset collection at capture/validation time, not silently accepted
- [ ] A set of screenshots with mixed orientation is confirmed to be flagged, not silently accepted as a valid set
- [ ] Icon and banner assets are confirmed to be handled as their own distinct upload/generation flow, separate from the emulator screenshot tool
- [ ] A full audit of the export code path confirms no user wallet private key is ever requested, transmitted, or stored by the platform
- [ ] The export flow is confirmed to clearly communicate real publishing costs (SOL for storage/minting) to the user before they leave Talon for actual submission
- [ ] Triggering an export build is confirmed not to block or degrade an active, ongoing dev-loop session in the same project

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing — a real export, a real inspected artifact, real screenshot validation attempts with both passing and failing inputs — not by reviewing the pipeline code and assuming correctness.
- No path exists, anywhere in the codebase, where a user's publisher wallet private key could be requested or handled by the platform — this is treated with the same severity as a credential-hygiene violation from Phase 1.
- A user could take the artifacts and manifest this phase produces directly to Solana's Publisher Portal or `dapp-store-cli` and submit without needing to fix or regenerate anything Talon was responsible for producing.

**This phase completes the full build-to-store pipeline. Nothing further downstream depends on this except production deployment itself.**
