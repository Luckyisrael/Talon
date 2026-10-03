# CLOCK IN — Submission Checklist

Draft answers for the submission form. Everything marked ✅ is filled from the
repository itself (verifiable at the cited paths/commits). Everything marked
**TODO** needs a human decision or a link only you can create.

---

## PROJECT TITLE*

**✅ Talon**

> A browser IDE, a companion mobile app, and an AI build agent for the Solana
> Mobile ecosystem: describe an app in plain language, the agent plans the
> screens, writes React Native/Expo code, verifies it through typecheck, lint,
> device boot, vision, and interaction gates, and ships a signed Android APK
> plus a Dapp Store Portal submission bundle.

---

## HAS THE TEAM AND/OR THE PROJECT RECEIVED ANY PRIOR FUNDING FROM A VENTURE CAPITAL FIRM OR ANGEL INVESTOR?

**✅ NO**

> No funding of any kind — self-funded, single-developer project. (Nothing in
> the repository suggests otherwise; confirm this is accurate for your team.)

---

## HAS YOUR PROJECT BEEN BUILT IN THE LAST 3 MONTHS?

**✅ YES**

> First commit: `2026-08-04` — *"Phase 0: scaffold monorepo (Go backend +
> Next.js frontend + contracts + docs)"*. Actively developed through the
> submission cut-off (`2026-10-03`, HEAD `6b7cca6`) — the entire project is
> ~2 months old.

---

## HAVE YOU WON A PREVIOUS HACKATHON WITH THIS PROJECT? IF YES WHICH ONE, IF NO PUT N/A.

**N/A** — **TODO (confirm)**

> Answer as N/A unless this project has already won something; if it has,
> name the hackathon here.

---

## IF PORTING AN EXISTING APPLICATION OVER TO MOBILE, WHAT MAJOR FEATURES OR NEW SIGNIFICANT MOBILE DEVELOPMENT HAVE YOU DONE?

*Not applicable as a port — Talon is a new project, not a port. Answering the
next question instead, as the form allows.*

---

## IF NO, WHAT MAJOR FEATURES OR NEW SIGNIFICANT MOBILE DEVELOPMENT HAVE YOU DONE?

**✅ Major features / significant mobile development:**

1. **A native Android companion app built from scratch** (`talonmobileapp/` —
   Expo/React Native, Android-only, 25 screens): chat with the build agent
   with a live streaming tool trace, plan-approval cards, per-file diffs,
   build-completion notifications, Solana Mobile wallet sign-in, and a
   Metro-connected Device tab for live testing. Code viewing, full execution
   trace, and terminal stay web-only, by design.

2. **Device-in-the-loop verification (redroid warm pool).** The build agent
   doesn't claim an app works — a Dockerized Android emulator
   (`backend/internal/redroid/`) boots it through a warm-container pool and
   runs a verify ladder: typecheck → lint → bundle → Gradle → **device boot →
   vision (screenshot) → interaction gates**. Screenshots are served over the
   API; the pool reconciles orphaned containers every cleanup tick.

3. **Physical-device handoff: token-gated Metro relay with QR pairing** so a
   real phone loads the dev build straight from the agent's workspace
   (`backend/internal/relay/`), plus a sideloaded dev-client handoff
   (`PHASE_16_PHYSICAL_DEVICE_MODE.md`, `PHASE_17_COMPANION_MOBILE_APP.md`).

4. **A complete Android release pipeline written in Go**
   (`backend/internal/release/`): Expo prebuild → `assembleRelease` →
   per-project keystore generation and persistence (keytool, one keystore per
   project, password stored encrypted) → `zipalign`/`apksigner` signing → a
   downloadable signed APK that is re-verified before delivery (aapt package
   identity must match the project; `apksigner verify` certificate must match
   the project keystore), together with a submission bundle (`SUBMIT.md` +
   `dapp-store.json`) formatted for Solana Mobile's Dapp Store Portal.

5. **Solana Mobile authentication**: sign-in with a Solana wallet
   (challenge/ed25519-signature verification) alongside GitHub/Google OAuth;
   the generated-app template ships a demo wallet for Mobile Wallet Adapter
   testing (`backend/template/`).

6. **Proven end to end against live infrastructure** (2026-10-03): a
   community-rewards app taken through plan → build → verify → device session
   → Metro relay → release export on a live server — delivered a **73.2 MB
   signed APK** (`com.talon.neighborhoodrewards`, versionName 1.0.0, minSdk
   24) plus the Dapp Store Portal submission bundle, with wallet auth, paywall,
   ownership checks, and 401/403/404 boundaries all asserted over the API.

---

## DOES YOUR APPLICATION HAVE AN SKR INTEGRATION? IF SO, HOW?

**✅ YES**

> SKR payments unlock release-export credits (the quota that lets a project
> download its signed APK). The integration lives in `backend/internal/skr/`
> and `backend/internal/api/skr.go`, and never trusts the caller:
>
> - **`POST /v1/skr/quote`** — the *server* states the price, the pack size,
>   the mint, and the treasury address the client must pay. A price the client
>   names is a price the client chooses, so all four come from deployment
>   config.
> - **`POST /v1/skr/verify {"signature": "…"}`** — the server fetches the
>   transaction *itself* via `getTransaction` on its own configured RPC
>   endpoint and re-derives the payment from the chain: transaction exists and
>   succeeded, it is an SPL Token transfer **in our mint**, landing **in our
>   treasury account**, for **exactly the pack amount** (decimal exponent read
>   from the on-chain mint, never from environment config). Verdicts are
>   three-way: `verified` / `pending` (not landed yet — client retries) /
>   `rejected` (proven not this payment, HTTP 422).
> - **Fail-closed by construction**: RPC unreachable → HTTP 502 (no credits);
>   input that isn't even a base58 signature → HTTP 400 *before* any RPC call;
>   wrong amount, wrong mint, wrong receiver → `rejected`. Verified credits
>   grant the pack (or confirm credits already held); a fresh account with no
>   credits hits the export paywall (HTTP 402).
> - **Unreachable from requests**: RPC endpoint, mint, and treasury are read
>   once at startup from the environment — a caller who could choose them
>   would be choosing the oracle that judges their own payment.
>
> Exercised fail-closed in testing (garbage signatures refused with
> `bad_signature`, `verified: false`) with no live funds — quote and verify
> paths are proven without spending money.

---

## DECK URL*

**TODO** — pitch deck link required (supported: any public URL — Google
Slides/Docs, Notion, PDF, etc. — check the form's accepted formats).
*(Required for submission per `EXPORT-CREDITS-PLAN.md`.)*

---

## DEMO VIDEO URL*

**TODO** — YouTube, Loom, or a public Google Drive link. (Still to be
recorded; submissions close 2026-10-08.)

---

## TRANSCRIPT

*(Form-side field — paste the video transcript when the video exists.)*

---

## REPOSITORY URL*

**✅ https://github.com/Luckyisrael/Talon**

> HEAD at draft time: `6b7cca6b313aad982d7c9184019cab276b8c08f9`
> (`main`).

---

## ANDROID APK URL*

**TODO — needs a hosted direct-download link.** The candidate APK is built,
signed, and verified:

| | |
|---|---|
| File | `backend/exports/project-1-1791012964.apk` (in the working tree) |
| What it is | "Neighborhood Rewards" — an app generated end-to-end by Talon, the demo app |
| Package | `com.talon.neighborhoodrewards` (versionName 1.0.0, minSdk 24) |
| Size | 73,227,310 bytes (73.2 MB) |
| Signature | `apksigner verify` passes; signer DN `CN=Talon Project 1, OU=Talon, O=Talon` |
| SHA-256 | `9825a369c04e3a521a46ff9fb451f4a347b1f0fb016221fd0619b1d09f7899eb` |

> **To finish:** upload it as a GitHub Release asset (Releases →
> "Draft a new release" → attach the `.apk`) and use the
> `https://github.com/Luckyisrael/Talon/releases/download/<tag>/…apk` link,
> or host it anywhere that gives a direct-download URL. Then paste that URL
> into the form. *(No repository credentials for uploads were available from
> the dev machine, so this step is left to you.)*

---

### Open items before submitting

1. **Deck URL** — create/link the pitch deck.
2. **Demo video URL + transcript** — record and publish (before **2026-10-08**).
3. **APK URL** — upload the signed APK and paste the direct link.
4. Confirm the two human-only answers: prior funding (**NO**), previous
   hackathon wins (**N/A**).
