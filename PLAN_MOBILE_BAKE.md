# Bake Plan: Talon Companion App (`talonmobileapp/`)

Status: APPROVED — not yet started. Sources of truth: `DESIGN_MOBILE.md`
(tokens/components/screens), web `frontend/components/ide/` (behavior),
web `frontend/lib/api.ts` (endpoints).

Starting state: Solana Mobile Kit scaffold (expo-router Stack, `@/` alias,
wallet-ui account/network features working, `expo-font` + haptics +
bottom-tabs + async-storage installed). No theme, no fonts, no API client,
no auth, no onboarding — `app/index.tsx` is the kit demo screen.

## Phase 0 — Foundation (theme + fonts + API client)

- `constants/theme.ts` — Colors/Type/Spacing/Radii exactly per
  DESIGN_MOBILE.md
- `components/ui/` — `LimeButton`, `GhostButton`, `Pill`, `Card`, `Badge`,
  `Mono`, `Field` (16px input)
- Fonts via `expo-font`: Inter 400/500 + JetBrains Mono 400, splash held
  until loaded, system fallback (never block render)
- `lib/api.ts` — fetch client mirroring web endpoints (auth login,
  projects, prompts SSE stream, sessions, triage, export, usage) with
  `EXPO_PUBLIC_API_URL`; `lib/auth.ts` — JWT in expo-secure-store
  (install it; token storage is the one place async-storage isn't honest)
- `lib/session.ts` — auth gate: unauthenticated → `/auth/login`, else
  `/(tabs)/build`
- Verify: `tsc --noEmit`, app boots to login on template (no device needed)

## Phase 1 — Onboarding + Auth (DESIGN_MOBILE screens)

- `app/index.tsx` → splash (mark + floor wash, auto-route)
- `app/onboarding/{welcome,features,connect}.tsx` — swipeable, x-of-3
  caps, lime CTA / ghost Skip / MWA Later
- `app/auth/{login,verify}.tsx` — email+password login (matches web, NOT
  magic-link), error shake + coral caption
- Verify: walkthrough on emulator — splash→onboarding→login→tabs; tsc clean

## Phase 2 — Tabs shell + Build tab (AIChat mirror)

- `(tabs)/_layout.tsx` — Build/Plan/Device/More, carbon bar, lime active
  indicator
- `(tabs)/build.tsx` — status header (eyebrow `BUILD·ROUND`, project name,
  amber partial-verified line), streaming trace rows, thinking dots +
  elapsed timer, composer (multiline + lime send), **QuestionCard**
  (stacked choices + free text — the approval surface)
- Gate verdict rows (expandable mono output) + `SkippedGates` amber line,
  copy-matched to web
- Verify: point at dev backend, stream a real turn, answer a question,
  confirm SkippedGates renders

## Phase 3 — Plan tab (PlanViewer + TriageForm mirror)

- Checklist sections (Screens/Features) with 20px checkboxes,
  persisted-status load on mount (reads the durable backlog endpoint state)
- Triage card: decision/date/exposure/notes, integer-cents, revise-in-place
- Verify: against backend with real plan data

## Phase 4 — Device tab (PhonePreview mirror)

- Screenshot on demand (never autoplay), gate verdict list, session attach
  (adb target from active session)
- Verify: needs Redroid — screenshot renders, verdict rows expand

## Phase 5 — More tab + hardening

- Memory list, export entry (tier copy), tier badge, sign-out; `Meter`
  (usage/tier)
- Verify: full `tsc + lint + prebuild` (scaffold's `ci` script), cold-start
  timing, 44px touch audit

## Explicit non-goals

- No Wallet tab v1 (kit demo features stay reachable but unstyled; MWA
  connect lives in onboarding per design doc)
- No light mode, no tablets layout, no push notifications, no offline queue
- No NativeWind — explicit StyleSheet per DESIGN_MOBILE decision

## Ordering logic

Each phase is independently shippable and verifiable without a device
until Phase 4; backend already serves every endpoint the app needs
(verified live in the E2E runs).
