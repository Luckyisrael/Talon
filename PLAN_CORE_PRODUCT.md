# PLAN_CORE_PRODUCT.md — prompt, frame, live E2E, OAuth, notifications, mobile

Status: DRAFT v2 — awaiting approval. Ordered by the user's stated sequence:
prompt first, frame, then the live E2E, then OAuth (email/password removed),
then OneSignal push notifications, then the mobile app wiring. A final pass
re-runs the E2E against the finished product. "Leave no stone": every
follow-up from PLAN_DEVICE_VIDEO.md is absorbed into phase C or G.

Approved-by-user decisions baked into v2:
- Push notifications via **OneSignal** for the mobile app (user input
  needed, plan complete, build complete — "basically anything to notify
  the user").
- The E2E target app is a **fresh project; the previous 22 workspaces are
  deleted** (backed up first — see C0).
- Maestro on redroid: **attempt it**; if the emulator genuinely can't be
  driven, pin the honest verdict and report the blocker (C5).

What I verified before writing this (not assumed):
- **The OneSignal native SDK is now installed by the user via yarn**
  (`react-native-onesignal@5.5.10` — dist/android/ios complete, yarn.lock
  entry present; `onesignal-expo-plugin@2.7.1`; `react-native-qrcode-styled@0.4.0`
  with react-native-svg already present). **Yarn bypasses the npm EALLOWSCRIPTS
  wall** — the mobile install path is: `yarn add <pkg>` (not npm; the yarn
  global cache is read-only under this harness, so cache-folder overrides
  are applied when yarn fetches). E's vendoring workaround is retired.
- **AI gateway is rate-limited to 8 requests/min** (user-stated). The
  backend has retries (3 attempts, 3s/6s fixed backoff on 429/502/503/504)
  but **no pacing** — parallel read-only tool batches fire simultaneously
  and each orchestrator round is a separate model call. Phase C adds a
  client-side pacer (min-interval between gateway calls, 429-aware
  backoff honoring Retry-After when present) so a live build stays under
  the budget; the E2E driver also serializes its own model turns and
  paces them. Evals/unit tests unaffected (they mock the gateway).
- The build system prompt (`backend/internal/ai/prompts.go` SystemPromptBuild,
  138 lines) has **no device-awareness section** — the agent is never told
  about `metro_status`, `capture_screenshot`, `tail_logcat`, `run_e2e`, or
  that the user watches a live mirror. The DV1–DV5 tools are registered but
  undescribed.
- The phone frame (`frontend/components/ide/PhonePreview.tsx` ~460–470) is a
  10px bezel, `rounded-[30px]` outer / `rounded-[20px]` screen, 3 span side
  keys, obsidian→carbon gradient. Slimmer/premium means geometry + edge
  treatment, not more chrome.
- **scrcpy-server.jar is missing from the host** (`findScrcpyServer()` finds
  nothing) — so the live stream runs JPEG fallback and input runs the
  adb-shell fallback. The H.264 live smoke from the DV follow-ups needs the
  jar. Network is up (github.com, registry.npmjs.org reachable).
- **Maestro is not installed** (`run_e2e` honestly skips today). Install
  path is `curl -fsSL 'https://get.maestro.dev' | bash` → `~/.maestro/bin`.
- Auth today: email/password (`/v1/auth/register`, `/v1/auth/login`),
  bcrypt + timing-decoy. The `users` table has `password_hash NOT NULL`.
  The mobile app's auth screen is **already OAuth-shaped** (GitHub + Google
  buttons, dummy handlers) — the mobile side aligns naturally.
- gitremote has a complete GitHub OAuth pattern to follow: env-gated
  config, state tokens, public callback route.
- The template app's Android id is `com.seekerstudio.app`; the dev-client
  APK (271MB, built) is the same package with expo-dev-client — the
  Maestro flow's `appId` target.
- Mobile app state: tanstack/react-query 5.90 is installed with a
  QueryClientProvider mounted, but **zero `useQuery` usage** — every tab
  renders mock data (SEED_SCREENS, LOG_LINES, `echoes`, FauxQr). `tsc` is
  clean. `expo-web-browser` + `expo-secure-store` are installed;
  `expo-auth-session` is NOT (and npm's project-install policy blocks
  adding it — the flow below avoids needing it).
- AI env is live: OpenRouter, `nex-agi/nex-n2.5-pro:free`, key set. The
  E2E will use the real model.

---

## Phase A — the device-aware system prompt

The agent has a live device loop it doesn't know about. It learns it.

**Changes (`backend/internal/ai/prompts.go`):**
- Add a `## The device` section to `SystemPromptBuild`:
  - The user watches a live mirror of the device at all times. Metro events
    (bundle %, hot reloads, redbox errors) are visible to them — the agent
    reads the same events through `metro_status`.
  - After landing edits, `metro_status` is the cheapest error check — a
    redbox names the file and line before any screenshot is needed.
    `capture_screenshot` + `run_vision` are for *what it looks like*,
    not *whether it compiled*.
  - `tail_logcat` when something crashes at runtime.
  - `run_e2e`: author short flows under `.maestro/flows/` (launch + one
    assertion; `appId: com.seekerstudio.app`), then run them. E2E is part
    of finishing, not an extra.
  - Session facts the user experiences: the device pauses after 10 idle
    minutes and is destroyed after 30; the preview badge explains it.
- Extend the plan-mode tail: when the plan implies device verification,
  name it (the E2E flow is authored during the build, from the plan).

**Changes (`backend/internal/ai/capabilities.go`):** the three
WorkingDiscipline tiers gain one sentence each: verify on the device per
unit — `metro_status` after bundling, a screenshot when the change is
visual, an E2E flow when the feature is a flow.

**Constraint:** the persona section is process-fixed → prompt-cache
stability is preserved by construction (the text lands in the const;
`promptsec` fingerprints it once). Tests pinning prompt bytes
(`TestOpenAISystemPromptIsCacheable`, any prompts tests) are updated.

**Verify:** `go test ./internal/ai/ ./internal/harness/promptsec/` green;
`make evals` green; the DV phase guards still pass.

## Phase B — the premium phone frame

**Changes (`frontend/components/ide/PhonePreview.tsx`):**
- Bezel 10px → **5px**; outer radius `rounded-[30px]` → `rounded-[34px]`
  with the screen `rounded-[29px]` (concentric corners — the premium tell).
- Edge treatment: replace the flat `border-smoke` gradient with a hairline
  metallic edge — `ring-1 ring-white/12` + an inset top specular
  (`inset 0 1px 0 rgba(255,255,255,0.10)`) over a near-black body.
- Side keys: 3px → **2px**, shortened, recessed 1px into the bezel so they
  read machined rather than stuck on.
- Punch-hole: 2.5 → 2 units, 10px → 8px from the top.
- Top strip scrim: `from-black/70` → `from-black/50`, tighter padding —
  the readouts stay legible but stop competing with the screen.
- Grounding shadow: softer, wider (`0 32px 80px -24px rgba(0,0,0,0.65)`),
  drop the second inset.
- Everything behavioral is untouched: canvas, tap/swipe mapping, nav keys,
  metro badge, redbox banner, fps readout, WebCodecs path.

**Verify:** the existing PhonePreview suites (states, badge, negotiation)
stay green untouched — the frame is chrome, not logic; `npx tsc --noEmit`
clean.

## Phase C — the live E2E: the entire core product, no smoke

The three PLAN_DEVICE_VIDEO follow-ups land here first (they are the
prerequisites), then a repeatable harness drives the real product.

**C0 — clean slate (approved): fresh project, previous 22 workspaces gone.**
- `workspaces/` currently holds 22 project dirs (17–38, 1.3 GB, git-ignored)
  plus the dev DB `backend/seekersstudio.db` that references them.
- Before deletion: `tar czf backups/pre-e2e-<timestamp>.tar.gz` of
  `workspaces/` + the dev DB (backups/ added to .gitignore — 1.3 GB never
  enters git). Then `rm -rf workspaces/*` and archive-move the dev DB.
- The studio then starts empty; the E2E's first act is creating its fresh
  project from the template. This is also the honest-user test: a brand
  new account on a brand new install.

**C1 — prerequisites (the follow-ups):**
- Install Maestro (`curl -fsSL 'https://get.maestro.dev' | bash`, PATH
  `~/.maestro/bin`) — lights up `run_e2e`.
- Place `scrcpy-server.jar` (v2.1.2 release jar from Genymobile/scrcpy
  GitHub releases → `~/.local/share/scrcpy/scrcpy-server.jar`) — lights up
  the scrcpy control socket AND the H.264 video path. The live H.264 smoke
  the doc called for happens here, in the real stream.
- Start the redroid container (no device is running today): the warm pool
  creates it on demand, but C's first boot asserts from a clean slate —
  docker image present, `launch-dev.sh` path proven as the fallback.
- `PUBLIC_HOST` set (LAN IP when detectable) — the relay's deep link is
  shaped for real phones.
- **AI gateway pacer** (rate limit 8 req/min): a min-interval gate in the
  AI client (`backend/internal/ai/client.go`) applied to every gateway
  call across both protocols — chat/plan/agentic turns serialize behind
  it; 429 responses back off honoring `Retry-After` when the gateway
  sends it (else exponential: 10s, 20s, 40s). Env-tunable
  (`AI_MIN_REQUEST_INTERVAL`, default derived to stay under 8/min);
  off-by-default behavior stays byte-identical when unset so unit tests
  and evals (which mock the gateway) never slow down.

**C2 — the harness: `backend/cmd/e2e` (a Go driver) + `e2e/run-live.sh`:**
The driver speaks the real client surface — HTTP, WebSocket, SSE —
against a running stack with the **real model** (env is live). Hermetic by
override: scratch `DB_PATH`, scratch `WORKSPACE_DIR`, real docker, real
template, real APKs. It walks the product exactly as a user does:

1. **Account + project** — register/login (email mode in v1; OAuth-stub
   mode in the final pass after D), create a project from the template.
2. **Plan** — a plan-mode prompt (live model) → the structured plan returns;
   a follow-up answer is submitted; `ask_user` pause + `/answer` approval
   path exercised.
3. **Build** — a build-mode prompt ("a counter screen with a tap
   increment") → the agent writes files, typechecks; the driver asserts
   the trace: files written, `run_checks` verdict, rounds bounded.
4. **Device** — session create (warm pool claims; assert the claim log
   line) → stream WS connects (negotiation frame; frames arrive) →
   input WS tap → metro SSE sees `bundle_start…bundle_done` → the badge
   events reach the client surface.
5. **Verify ladder** — the build turn is prompted to verify on device:
   `run_boot`, `capture_screenshot` (+ vision), `tail_logcat`,
   `test_wallet_roundtrip` (mock wallet installs at session create),
   and `run_e2e` with an **agent-authored** Maestro flow (appId
   `com.seekerstudio.app`) — the driver asserts each verdict from the
   trace, and asserts the *redbox path* by a deliberate broken edit
   (agent told to introduce a syntax error, watch `metro_status` classify
   it, fix it).
6. **Kill + start** — destroy the session (assert container gone from
   `docker ps`, relay port freed) → create again → assert the warm pool
   served the second boot fast (the cold ~2m13s vs warm claim).
7. **Phone relay** — `GET /test-info` → `available: true`, then curl the
   `metro_url` through the relay → 200 from metro (what a phone would get).
8. **UI** — frontend built and served; root + login render 200; component
   suites green. (Browser-visual E2E via Playwright is blocked by the
   npm install policy — honest limitation; the API/WS/SSE driver covers
   the full pipeline logic.)

The script: preflight (docker image, maestro, jar, adb, model env),
start backend + frontend as managed jobs, run the driver, teardown,
print a per-step report, exit honestly. `make e2e-live` is the entry.

**Risks named up front:** Maestro-on-redroid (uiautomator on emulated
ARM) may not drive the device — if it genuinely can't, the driver proves
the blocker and pins `run_e2e`'s real-failure verdict (honest skip is
already correct; we do not fake a pass). **The gateway is rate-limited
(8 req/min)** — the client-side pacer (C1) + retry backoff keep live
turns under budget; a sustained 429 wall is reported as a real blocker,
never retried in a loop.

**Verify:** one full green run end-to-end; committed as a repeatable
target; each bug the run surfaces is fixed in the phase that owns it.

## Phase D — GitHub + Google auth, email/password removed

Pattern: gitremote's env-gated OAuth, extended to sign-in.

**Backend (`backend/internal/auth`, new `oauth.go`; `internal/api/auth.go`;
`internal/store`):**
- Config from env: `AUTH_GITHUB_CLIENT_ID/SECRET`,
  `AUTH_GOOGLE_CLIENT_ID/SECRET`, callback URLs derived from the existing
  public-origin logic. Dark-until-configured, exactly like gitremote.
- Flow: `GET /v1/auth/providers` (honest: which are configured) →
  `GET /v1/auth/{provider}/start?redirect=<target>` (mints a single-use
  state token binding the redirect) → provider consent →
  `GET /v1/auth/{provider}/callback` (public route, like the GitHub
  callback) → exchange → **find-or-create user by (provider, provider_id)**
  → mint a **single-use 60s code** → 302 to `redirect?code=…` →
  `POST /v1/auth/exchange {code}` → `{token, user}`. The token never
  appears in a URL; the code is one-time and short-lived.
- Store: `users` gains `provider`, `provider_id`, `avatar_url`;
  `password_hash` becomes nullable (migration, tolerated-if-present);
  upsert semantics: same provider account re-links in place.
- **Delete** `POST /v1/auth/register`, `POST /v1/auth/login`, the
  password validation/hashing/decoy path (the enumeration surface goes
  with it), and `lib/auth`'s email helpers where they only served the form.
- Providers' profile fetch: GitHub `read:user user:email`, Google
  `openid email profile`; email stored when the provider returns one
  (both do with these scopes).

**Web frontend:**
- `login/page.tsx` → the two provider buttons (the mobile screen's
  design language); a dark provider renders its button disabled with
  "not configured on this server" — honest, like every other dark
  integration. `register/page.tsx` deleted; nav links updated.
- New `auth/callback/page.tsx`: reads `?code`, exchanges, sets the token,
  routes to the dashboard.

**Mobile (wired fully in F, but the flow is decided here):**
`expo-web-browser.openAuthSessionAsync` to `/v1/auth/{provider}/start?
redirect=expokit://auth/callback` (the app scheme, already in app.json) →
backend redirects with the code → the app exchanges via POST. **No
`expo-auth-session` install needed** — the backend drives the flow; both
clients follow it. One source of truth for auth.

**Tests:** httptest stub providers (endpoints overridable like
gitremote's vars) for the full start→consent→callback→exchange path;
store upserts; dark-provider honesty; frontend callback page test.

**External dependency (the one thing I cannot do):** the OAuth apps
themselves must be registered by you on GitHub and Google Cloud Console;
client IDs/secrets go into `backend/.env`. Until then the buttons are
honestly dark; the E2E drives a local stub provider so the whole path is
tested without your creds.

## Phase E — OneSignal push notifications

The mobile app must reach the user's pocket, not just its foreground.
OneSignal is the channel (approved). Two delivery realities discovered:

1. **Yarn installs work on the mobile project** (the user proved it:
   react-native-onesignal, onesignal-expo-plugin, react-native-qrcode-styled
   are all in via yarn). E drops the vendoring workaround — the SDK is
   present. **Zero-SDK fallback stays as the in-app notification list**
   (backend-recorded notifications polled by the app) because it costs
   nothing and gives the honest surface until real OneSignal keys exist.
2. **Backend REST delivery needs no SDK at all.** OneSignal's
   `POST /api/v1/notifications` with a REST key is plain HTTP from Go —
   env-gated `ONESIGNAL_APP_ID` + `ONESIGNAL_REST_KEY` (dark until set,
   same law as gitremote and the AI provider).

**Trigger map (backend, `internal/notify`):** a tiny `Notifier` interface
+ a `NoopNotifier` and a `OneSignalNotifier` (implementation swapped by
env). The SSE stream handlers are the honest trigger points — they are
where the backend already decides "the turn ended" or "the user's answer
is needed":
- `QuestionEvent` → **"Your input is needed"** (ask_user pause: the one
  moment the product truly waits on the human).
- `finishBuildStream` with `status: success` → **"Build complete"**;
  `status: failed` → "Build failed"; `status: paused` → "Turn paused —
  resume when ready" (exhausted rounds).
- Plan saved (`savePlan`) → **"Your plan is ready"**.
- Session reaper: `reapDestroy` (reaper.go:113) → "Device session ended
  (idle)" — the user should know their device quietly went away; and the
  pause branch → "Device paused — tap to resume".
- Every notification carries `projectId` + (when it exists) `promptId` /
  `sessionId` as OneSignal **data payload** for deep links, sent to the
  user's registered device subscription — one registered device per user.

**Device registration (mobile):** `POST /v1/users/push-token` with the
OneSignal subscription ID (the installed SDK's `getDeviceId` / 
`getDeviceState`). Backend stores `onesignal_id` on the user. Query keyed
by user, mutation on login/registration callbacks. **In-app fallback:** the
backend records every notification it sends (`notifications` table: user,
kind, payload, sent_at, read_at) and the app polls it — same trigger
points, same payload shape, the SDK only changes the last mile. The app
ships both: push when keys exist, the list always (it doubles as history
and the honest surface before keys arrive).

**Honest notification law:** the backend notifies only on real state
changes, never as a reminder loop; the notifier never blocks the main
path (fire-and-forget with a bounded error log — a push failure must
never fail a build or an SSE stream).

**Tests:** stub OneSignal REST (httptest server, URL overridable) proving
each trigger fires exactly once with the right payload; registration
endpoint; dark-env honesty (noops with no error); notification-list
query contract.

## Phase F — the mobile app, wired

**Data flow — one source of truth:**
- `lib/api.ts` completed: every endpoint typed (auth exchange, projects,
  prompts + **the SSE stream via `expo/fetch`** (SDK 55's streaming fetch
  — RN's fetch can't stream; Expo's can), answer/steer, sessions,
  test-info, push-token, plan tasks, triage, usage, gates, notifications).
- `hooks/` (new): `useAuthBootstrap`, `useProjects`, `useActiveProject`,
  `usePromptStream` (the stream state machine over the SSE), `useAnswers`,
  `useDeviceSession` (create/destroy mutations + status query),
  `useTestInfo` (refetchInterval while live), `usePlanTasks`, `useTriage`,
  `useUsage`, `useNotifications` (E's list + unread count, poll while
  backgrounded-foreground), `usePushRegistration` (swap the device token
  with the backend on sign-in). Queries keyed, mutations invalidate —
  react-query owns fetching, caching, retries, and polling.
- **useEffect residue** (the target): fonts, the root auth gate, component
  animations, and the OneSignal SDK init/subscription listener (the SDK is
  event-native). Nothing else — no fetch-in-effect, no manual refresh state.

**Screens rebuilt on the hooks; mock data deleted:**
- `build.tsx`: real trace rows from the stream events, the real QuestionCard
  (answers via `/answer`), the real gate verdicts; the `echoes` mock dies.
- `plan.tsx`: real task checklist + triage (persisted, loaded from the
  same endpoints the web uses); `SEED_SCREENS`/`SEED_FEATURES` die.
- `device.tsx`: session start/stop, live status, **the real QR** — the
  vendored web encoder is a pure TS function; it ports to
  `lib/qr.ts` and renders with react-native-svg (installed). `FauxQr` and
  `LOG_LINES` die. Screenshot on demand via the screenshot endpoint.
- `more.tsx`: real usage meter, tier, sign-out, and the notification list
  (E's in-app fallback surface); dummy rows die.
- `auth/index.tsx`: the dummy handlers become the real
  web-browser OAuth flow from D.
- Guest mode: the "Explore without an account" entry stays but becomes
  honest — signed-out tabs render empty states with a sign-in CTA
  (queries 401 → the auth gate), not fake data.

**Verify:** `tsc --noEmit` + lint clean; prebuild passes; the screens
against the live backend from phase C's stack (the driver's scratch stack
or a dev server) — a manual smoke the user can repeat.

## Phase G — the final pass, no stone left

- Re-run the **full live E2E against the finished product** — the driver's
  auth step switches to the OAuth stub-provider mode (email/password is
  gone), so `make e2e-live` is complete and self-contained. The driver
  also asserts the notification law: a paused-for-question turn produces
  exactly one "input needed" notification; a finished build exactly one
  "build complete" — no duplicates, no reminder spam.
- Full suites: backend `go test ./...` + vet, frontend vitest + tsc,
  mobile tsc + lint, `make evals` — one phase guard per phase added here
  (A: the device section exists and is cache-stable; C: the driver's
  assertions as a guard where deterministic; D: the exchange law; E: the
  notifier trigger contract; F: the hooks' contract).
- PLAN docs updated; this doc's status → SHIPPED; a closing summary with
  what ran live, what was fixed because the E2E caught it, and the honest
  leftovers.

---

## What is deliberately NOT in this plan

- No Playwright/browser-visual E2E (npm install policy blocks it; named in
  C8 as the honest limitation).
- No real GitHub/Google credentials created by me — that is yours; the
  env-gated dark path + stub-provider E2E covers everything testable.
- No real OneSignal app/keys created by me — same law: `ONESIGNAL_APP_ID` +
  `ONESIGNAL_REST_KEY` are yours to set in `backend/.env`; until then the
  notifier noops honestly and the in-app fallback surface still ships.
  (The SDK itself — react-native-onesignal 5.5.10 + onesignal-expo-plugin —
  is installed via yarn by the user; nothing further to install.)
- No offline queue / light mode on mobile (bake-plan non-goals stand).
- The wallet tab stays out of v1 mobile (bake plan non-goal).

**Closest external dependencies on you (nothing else blocks):**
1. OAuth apps (GitHub + Google Cloud) → client IDs/secrets into
   `backend/.env`.
2. OneSignal app → app id + REST key into `backend/.env`.
3. That's it. Everything else runs against live infra in this plan.
