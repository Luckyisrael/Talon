# PLAN: Session Resume, Phase 15/16/17 Extraction, and Size Optimization

Three workstreams the user asked to plan together. Each is understood from the
code itself (file:line evidence below), not from the phase docs' promises.

---

## Workstream 1 — The device-refresh bug (do FIRST, it's a live product bug)

**What the user saw:** a device was running; refreshing the page lost it; they
had to generate another device.

**Root cause (verified):** `IDELayout.tsx:110` keeps the session id only in the
Zustand in-memory store (`frontend/lib/store.ts:160` — `sessionId: null` on
mount). No route re-attaches to a live session; the backend has
`LatestLiveSession(projectID)` (`backend/internal/store/store.go:1044`) used
internally by 4 flows (buildsession, metro_events, testinfo, reaper-touch) but
**no HTTP route exposes it**. `GET /v1/sessions/{id}` exists but the frontend
never asks "does this project have a live session?" on mount.

Secondary hazard: the reaper pauses at 10 idle minutes (`reaper.go:33`). A
user refreshing after 10+ minutes hits a *paused* session — resume must handle
that state transition, not just re-attach to running ones.

**The fix (small, 3 pieces):**

1. **Backend:** `GET /v1/projects/{projectID}/live-session` — ownership-checked
   (same middleware as testinfo), returns the `LatestLiveSession` row or 204.
   Touches the session on read (a page load IS user activity — the same signal
   the stream join sends, `ws/stream.go:94`).
2. **Frontend `IDELayout` mount effect:** call the new route before showing
   "idle". States: running → set sessionId, previewStatus "streaming";
   paused → set sessionId, offer "Resume device" (call `POST /v1/sessions/{id}/resume`);
   none → idle as today.
3. **A phase-guard test** (backend) pinning the new route: returns the live row,
   404 vs 204 semantics for a project with no session, ownership enforced
   (another user's project → 404, never a row).

**Also fixes silently:** the mobile app's device tab (`talonmobileapp` polls
sessions) gets resume-for-free from the same route.

**What this does NOT do:** no session persistence across backend restart
(the in-memory metro/ws maps die with the process — the DB row survives but
streaming doesn't; honest behavior is `error` state on the preview, which the
page already renders).

---

## Workstream 2 — Phase 15/16/17 extraction (extract, don't absorb)

I read all three docs (251 lines) and verified implementation state against the
code. The verdict per phase:

### Phase 15 (VPS deployment & observability) — mostly SKIP for now
**What it is:** deploy-the-real-stack phase — IaC (compose), provisioning
automation, staging smoke gates, concurrent load tests, burn-rate dashboard,
tested backup/restore + rollback.
**Reality check:** presupposes a VPS and a decision to deploy there. The user
has not chosen a host; the AI gateway is rate-limited (8 req/min) making
"concurrent load tests" meaningless until the gateway is upgraded. The smoke
test that exists and is real (E2E run-12, all green) already covers §2.3's
spirit.
**Worth extracting NOW (cheap, high-value):**
- **2.6 Backup/restore as a tested procedure** — we already keep
  `backups/pre-e2e-*.tar.gz`. Extract: a tiny `make backup` / `make restore`
  that produces and *verifies* (sqlite integrity check + row counts) a DB
  backup. Small, testable, useful even single-machine.
- **2.3's smoke test** — already exists as `make e2e-live`. Document it in the
  README as the deploy gate. No new code.
**Skip (until a real VPS is chosen):** provisioning scripts, load tests,
burn-rate dashboard, rollback drills. These are deployment-day artifacts, and
building them now would be speculative machinery for a host that doesn't exist.

### Phase 16 (physical device mode) — MOSTLY DONE, two real gaps
**What exists (verified in code):** the relay (`backend/internal/relay/relay.go`
— self-hosted session-scoped TCP tunnel, dies with session, bounded conns),
the QR surface (`TestOnPhone.tsx` renders `info.deep_link` = `exp://host:port`),
dev-client APK download (`DevClientURL` — authenticated download route),
relay+metro lifecycle tied to sessions, "no session → honest empty state"
disclosure.
**The two REAL gaps vs the doc's non-negotiables:**

1. **§2.4 "The QR embeds authentication, not just a location" — VIOLATED
   today.** `testinfo.go:73` builds the deep link as bare `exp://host:port`.
   Anyone on the network who sees the QR can connect; the relay does no auth
   (metro itself serves unauthenticated). This is the doc's exact nightmare
   scenario. Fix: mint a session-scoped token, append `?token=` to the deep
   link, and gate the relay's accepted connections on a first-bytes handshake
   or a token check in metro's HTTP layer... **BUT** — careful. The dev client
   is a compiled Expo app; changing its connection handshake requires a dev
   client rebuild (the 40min+ gradle cycle). The pragmatic extraction:
   - **Now:** session-scoped random token in the deep link + relay-level
     check (the relay is ours, not the dev client's — it can require the
     token as the first bytes on each connection; the Expo dev client's
     connection URL supports query params — `exp://host:port?token=...` is
     consumable by metro as a query param via the `EXPO_NO_INTERNET_PERMISSION`...
     needs a spike). **Honest call: this needs a 30-min spike to determine if
     the stock dev client honors query params in the deep link.** If it
     doesn't, the fallback is a reverse-proxy style auth: the relay drops
     connections whose first HTTP request lacks the token query param —
     metro never sees unauthenticated bytes.
   - **Gate:** do not rebuild the dev client APK for this alone.

2. **§2.6 "Reduced verification is disclosed plainly" — PARTIAL.** The
   TestOnPhone panel says "Scan with your phone camera…" but never says "the
   agent cannot automatically verify what runs on your phone." The
   backend's device-tool law (`prompts.go` — tools register only with an
   attached session) already handles the *agent* side honestly; the *user*
   disclosure is missing. Fix: one sentence in the TestOnPhone panel +
   a line in the plan-approval flow when the user tests on phone.

**Also extracted (from §2.7):** parallel Redroid verification while a user is
on their phone — already the architecture (the emulator session is untouched
by TestOnPhone; both run side by side). Nothing to do. Confirmed.

**Skip:** §2.9 "distributed dev client version matches template exactly" —
already enforced by construction (same `dev-client/v1.0.0/` APK the
`DevClientURL` route serves). Skip §2.1's "two distribution paths" audit —
it's one binary already.

### Phase 17 (companion mobile app) — BUILT, extract the missing bits
**What exists (verified):** the whole app — tabs (build/plan/device/more),
OneSignal push (`package.json:61,68` + `more.tsx`), MWA wallet deps
(`mobile-wallet-adapter`, `@wallet-ui/react-native-kit`), SecureStore in
`lib/auth.ts` + `lib/session.ts`, onboarding, real TanStack Query wiring.
**Real gaps vs the doc:**

1. **§2.1 "Wallet sign-in verifies a signed challenge server-side" — the
   backend has NO wallet auth.** `grep -rln "wallet" backend/internal/api/`
   → only export.go + sessions.go (feature uses, not auth). The mobile app
   has the wallet UI but nowhere to send the signature. This is Phase 17's
   single biggest real gap. Extraction decision: **the backend challenge
   endpoint (issue challenge → verify signature → mint/link JWT)** is
   worth planning as a work item, but it's a full workstream (challenge
   store, nonce, verification, linking to OAuth accounts). Put it in the
   plan as its own item with its own sub-plan.
2. **§2.7 same-device deep link handoff** — `TestOnPhone.tsx` shows QR only.
   The mobile app exists; the deep link `talon://` scheme exists in the
   backend's identity config (`identity.go` — `com.talon` package, scheme).
   Gap: the mobile app's device tab has no "open in dev client" button that
   emits the same credential the QR carries. Small, real, worth extracting.
3. **§2.9 "session list reconciles on every open"** — the app uses TanStack
   Query; verify refetchOnMount/validity are set so reopening reconciles.
   Cheap check, likely already right.
4. **§2.12 demo flow rehearsal** — process, not code. Skip until the wallet
   auth exists (it's the missing leg of the demo).

---

## Workstream 3 — Size optimization (the user's quoted list, verified)

I verified every claim in the quoted table against this machine:

| Claim | Verified reality |
|---|---|
| Stock image used by manager | ✓ `manager.go:22` — `redroid/redroid:14.0.0_64only-latest` |
| launch-dev.sh installs after boot | ✓ host-path APK install, lines 72–82 |
| APK has 4 ABIs | ✓ `lib/arm64-v8a/, armeabi-v7a/, x86/, x86_64/` inside the APK |
| Export scratch cleaned | ✓ `release/build.go:96` — `defer os.RemoveAll(scratch)` **already runs** (the 1.2M in backend/exports is job metadata, not scratch) |
| Caches in repo | ✓ .gocache 287M + .gopath 501M + .gradle-tmp 5.1G |
| The "node_modules 2-3GB × 3 trees" | **Now verified — it's ~11GB total: frontend 687M + talonmobileapp 3.5G + backend/template 6.8G.** The template tree is the shocker (gradle caches + build artifacts inside it), and it doubles as every user workspace's `node_modules` via the scratch symlink — so it is load-bearing. Do not delete; the pnpm trial (S5) is the only lever, and it's deferred. |

**The plan, in execution order (each independently testable, none destroy
what exists):**

### S1. Delete the custom docker image + Dockerfile (−2.75GB, zero risk) — DO
- `docker rmi talon-redroid:latest` (stock base stays)
- Delete `infra/redroid/Dockerfile.talon` + `install-talon-apps.sh`
- Rewrite `launch-dev.sh` to run **stock** `redroid/redroid:14.0.0_64only-latest`
  and install the 3 APKs from host paths after boot (the install loop at
  lines 72–82 already does exactly this — just drop the baked-image boot path)
- Test: `bash infra/redroid/launch-dev.sh` boots, adb sees device, PIN 1234,
  3 APKs installed, one boots.
- Rationale: `manager.go` already runs stock + host-path installs; the baked
  image is used by nothing in the product path. `sessions.go` cap=5 unchanged.

### S2. Two ABIs, not four (−~50% APK) — DO, with a guard
- `backend/template/android/app/build.gradle`: add `abiFilters "x86_64", "arm64-v8a"`
  under `defaultConfig.ndk` (or the splits block) — exactly the doc's one-liner.
- **Guard:** the Seeker phone is arm64-v8a; the container is x86_64. Nobody
  needs armeabi-v7a (no target device) or x86 (no 32-bit container).
- **Cost:** requires a dev-client rebuild → the 40min gradle cycle → schedule
  as a background job, off the critical path. Do NOT rebuild until S1/S3 land
  (one rebuild, not two).
- Test: rebuilt APK's `lib/` shows exactly 2 ABI dirs; the dev client still
  boots in a session.

### S3. Verify + re-anchor the export-scratch claim (already clean — verify only)
- The scratch cleanup (`release/build.go:96 defer os.RemoveAll`) already runs
  on every build; today's `backend/exports/` is 1.2M of job metadata.
- One action: confirm on the next export that no `scratch-*` dir survives,
  then delete this item from the worry list. If a user build DOES leave
  scratch behind, find the early-return path that skips the defer and fix it.
- No code change expected.

### S4. Relocate tool caches to home (−5.9GB repo, keep machine caches) — DO
- Delete `.gocache/ .gopath/ .gradle-tmp/` from the repo (already gitignored).
- **The .gradle-tmp is 5.1GB** — the single biggest repo win. Standard
  `~/.gradle` already exists (used by release/build.go:108 —
  `GRADLE_USER_HOME=$HOME/.gradle`).
- Note: my sandbox used workspace caches because /tmp was wiped; on this
  machine the standard locations persist. Delete, don't relocate-with-copy —
  gradle re-downloads what it needs.
- Gate: S2's rebuild must use `GRADLE_USER_HOME=~/.gradle` from then on.
- Risk: zero for the repo; first post-delete gradle run re-populates
  `~/.gradle` (it already exists — likely near-instant warm).

### S5. pnpm trial on frontend only — DEFER, recommend skipping for now
- The user's own caveat (Expo tooling + pnpm symlinks) + the fact that Expo
  53 + RN 0.80 toolchain is the fragile heart of the product + we just
  climbed out of a rename-rebuild cycle = don't destabilize now.
- If pursued later: `frontend` only, never `talonmobileapp` or `backend/template`
  (both feed gradle builds where symlink layouts have bitten us).

### S6. docker prune + node_modules du — housekeeping, fold into S1
- `docker system prune -f` after S1's image deletion (reclaim build cache).
- One `du` of the three node_modules trees to have the real number in the
  plan record, no action.

### Not doing (already right shape, per the doc's own "would not" list)
- No slimming the redroid base (the product promise), no Git LFS, no web
  bundle micro-opt, no workspace `node_modules` audit (workspaces are
  source-only, 2.1MB/5 — but S6's du double-checks the template tree).

---

## Workstream 4 — The nine UI bugs (root-caused, file:line evidence)

Every bug below was traced to source before entering this plan. None is a
guess. Items are ordered by fix cost (cheapest first) — items 6/7/8 are three
symptoms of one bug and land together.

### B1. Git diff fails to load — one-line backend path bug
`backend/internal/api/gitdiff.go:28` joins `workspaceDir/project.Slug`, but
workspaces live in **numeric-id directories**: `workspace.go:81` is
`filepath.Join(m.workspacesDir, strconv.FormatInt(projectID, 10))` and
`ls backend/workspaces/` shows `1`, `2`. The handler runs `git diff` in a
directory that never exists → error every time.
**Fix:** `filepath.Join(h.workspaceDir, strconv.FormatInt(project.ID, 10))`.
Add a handler test with a real temp workspace asserting the diff renders.

### B2. 30-minute simple build — three compounding causes, honest expectations
The wallet-app prompt ran against an 8 req/min gateway with **8s pacing between
every model call** (client.go pacer, `AI_MIN_REQUEST_INTERVAL`), a
**60-round default budget** (orchestrator.go:94 `defaultMaxRounds = 60`), and
the model narrating each verify gate as a separate round. 30 min ≈ 200+ gateway
calls × 8s of pure pacing = most of the wall-clock is the pacer, not the model.
What is actually fixable:
- **Register the five missing verify tools** (see B5) — the ladder currently
  burns rounds calling tools that don't exist, then the model re-narrates the
  failure, doubling the waste.
- **Batch the verify narration:** the gate sweep already emits one `verify`
  event per stage (7 stages); the model then narrates each one again. Collapse
  per-stage narration so a full clean pass is ONE model turn, not seven.
- **Parallelize the pacer where safe:** the 8 req/min limit is the user's
  gateway tier — NOT negotiable, NOT worked around. What we can do: stop
  wasting calls on (a) nonexistent tools, (b) re-narration, (c) the
  plan-then-build double pass where the plan fits one turn.
- **Set expectations honestly in the UI:** a badge/line in the chat header
  showing "turn budget: N rounds · paced 8s/call at your gateway tier" so 30
  minutes reads as the pacing it is, not a hang.
**Not a fix:** raising the gateway tier. That's the user's account.

### B3. Chat section looks disorganized — structure audit, then layout pass
Verified the render: user messages are right-aligned bubbles (AIChat.tsx:1277),
assistant turns are full-width `AgentTrace` stacks + plan/question cards + a
TurnFooter, all in one `gap-4` column. The disorganization the user sees is
real and comes from: (a) trace rows (tools/narration/thinking) and cards
sharing one flat column with equal visual weight, (b) no turn boundary —
nothing visually separates one assistant turn from the next, (c) user bubbles
at `max-w-[88%]` next to full-width assistant blocks read as misaligned.
**Fix (layout pass, no data changes):**
- A subtle turn divider (timestamp + rule) between conversation turns.
- Assistant blocks get a consistent left indent aligned with the user bubble's
  right edge family — one visual rhythm, not two.
- Trace rows tighten: `gap-4` → contextual spacing; card/plan/question blocks
  get a uniform container (same padding, same border language).
- Files under `frontend/components/ide/` only; no API changes.

### B4. Device section looks off when QR appears — layout constraint bug
Verified: the device rail (IDELayout.tsx:562-583) is a `flex-col` with
PhonePreview `flex-1` and TestOnPhone `flex-shrink-0 overflow-y-auto` below
it. When a session starts, the QR block (168px + caption + deep-link row +
APK link) **stacks under the video and squeezes the phone preview**, and in a
narrow rail (min 220px) the 168px QR + link row overflow visually.
**Fix:**
- QR panel becomes a **collapsible drawer** inside the device rail: collapsed
  to a single "Test on your phone →" row by default; expands over (not below)
  the preview. The QR is a setup-time action, not a watch-time one.
- Deep-link row truncates harder (`min-w-0` + `truncate` on the code chip —
  already partly there) and the whole panel obeys the rail min-width.
- PhonePreview keeps its full height in every state.

### B5. "Checked X" twice + five "No tool named run_*" — the verify ladder is broken
**Two distinct defects, both verified in source:**
1. **Missing registrations:** `buildsession.go:114-118` registers only
   run_command, run_shell, run_typecheck, run_lint, run_checks — but the
   orchestrator's gate ladder (`orchestrator.go:2037-2042`) drives
   `run_bundle`, `run_gradle`, `run_boot`, `run_vision`, `run_interact`.
   Those five tools **exist** (`tools/verify.go:282,326,383,492`+interact)
   but are never `reg.Register`ed. The agent dutifully calls them, gets
   "No tool named …", and the transcript shows the ladder limping.
   **Fix:** register the five tools in buildsession.go with the same
   gating discipline as the device tools (bundle/gradle when the template
   toolchain exists; boot/vision/interact only when a session is attached).
   Add a registry test asserting every ladder stage's tool resolves.
2. **Double "Checked typecheck" rows:** the gate sweep emits one `verify`
   trace row per stage (AIChat.tsx:649-663), and the model ALSO narrates
   each check as its own narration row — so a clean pass shows 14 rows for
   7 checks. **Fix:** suppress the model's per-gate narration (prompt-side:
   "the verify sweep is already shown to the user; do not narrate it
   line-by-line — narrate the verdict") AND/OR dedupe trace-side when a
   narration text matches a just-emitted verify row's label. Prompt-side
   first (cheaper, no renderer surgery); keep the honest full output in the
   Output pane either way.
3. **"verified (static only …)" — already honest and correct.** Keep.

### B6. Shimmer keeps animating after the turn ends — leftover streaming flag
Verified: the `done` handler (AIChat.tsx:846-865) clears `status:"running"`
rows but **never clears `streaming:true`** on narration/thinking rows. The
shimmer (AgentTrace.tsx:389, `animation: shimmer-text infinite`) is driven by
`row.streaming`, so any row that never got its `text.end`/`thinking.end`
frame (error paths, aborted streams) shimmers forever.
**Fix:** in the `done` case, map every row to also clear `streaming`.
Same one-line sweep for the error/abort paths in the stream teardown.

### B7. Thinking caret should be collapsed by default — one character
`AgentTrace.tsx:361` — `const expanded = manualExpanded ?? !!streaming;`.
While streaming, thinking auto-expands. User wants collapsed default.
**Fix:** `manualExpanded ?? false` — expanded only on explicit click.
(Keep the caret; only the auto-expand changes.)

### B8. Blinking cursor under every decision/thinking — same streaming flag
The lime caret with `breathe` animation (AgentTrace.tsx:425-433) renders when
`streaming` is true on the thinking row — same stale flag as B6. After the
turn ends it keeps blinking under settled text. **Fixed by B6's sweep** —
no separate change; listed because it's a distinct symptom.

### B9. Blank dot + "0 calls" under tool rows — GroupRow summary glitch
Verified: `GroupRow` (AgentTrace.tsx:655+) renders `{count} {calls}` from
`row.children?.length`. The "0 calls" appears when a group row is emitted
with an **empty children array** — reachable when the group is created from
a run flushed at exactly the wrong moment (a read-only row still `running`
with no children committed), and the blank dot is `Glyph` returning the
default `<span className="h-1 w-1 rounded-full bg-ash" />` for
`status === undefined`.
**Fix:** (a) `groupRows` never emits a group with `children.length === 0`
— flush as plain rows instead; (b) group rows always carry an explicit
status (`running`/`done`/`fail`), never undefined; (c) defensive render:
if a group somehow arrives with 0 children, render nothing, not "0 calls".
**Plus history-rehydration check:** verify the same glitch can't come from
`/conversation` replay (rows arrive without the group wrapper) — if it can,
regroup on the client for display.

---

## Execution order & safety

```
 0. Push everything to github.com/Luckyisrael/Talon (done before any change)
 1. B1 diff path fix + test (one line, immediate user-visible win)
 2. B6+B7+B8 streaming-flag sweep (one edit in the done handler + collapse default)
 3. B9 group zero-children guard
 4. B5 register 5 verify tools + registry test + prompt dedupe (biggest behavior win)
 5. B2 verify-narration batching + honest pacing badge in chat header
 6. B3 chat layout pass (visual rhythm: turn dividers, indent, card containers)
 7. B4 QR drawer collapse in device rail
 8. W1 session-resume fix (route + mount effect + guard test)
 9. S1 image deletion + launch-dev.sh rewrite (frees 2.75GB, zero risk)
10. S4 cache deletion (frees 5.9GB from repo)
11. S2 ABI trim → ONE background gradle rebuild → verify in session
12. W2 Phase 16: token-gated relay spike (30min, decides path), disclosure sentence
13. W2 Phase 17: wallet-auth backend sub-plan, device-tab deep-link button
14. S3 export-scratch verification (one export run)
15. S6 housekeeping (docker prune + du record)
```

Bugs before infrastructure: the user sees bugs daily; disk space doesn't leak
into their day. B5 and B2 together also cut the AI spend per build turn
(fewer wasted rounds), which compounds with the 8 req/min reality.

Rules carried through all of it: 8 req/min AI gateway (B-batch fixes need NO
AI calls to implement; B5/B2 verification uses local backend tests only),
honest-empty-states law, no dev-client rebuild except the single S2 pass,
`make e2e-live` green throughout, every fix lands with its own test where a
test can pin it.

## What is deliberately NOT in this plan
- Any Phase 15 machinery (provisioning, load tests, dashboards) — no VPS chosen.
- Mobile push notification deep-dives — OneSignal is wired; demo rehearsal
  waits for wallet auth.
- The "connect account" settings feature (different-email provider linking) —
  separate backlog item.
- Maestro-on-redroid E2E assertion — backlog.
- Raising the AI gateway tier — the user's account decision, not ours; the
  pacer honors it, the plan works within it.
