# Stability gaps — found by driving the real app, not inferred

Monitoring log for the build under test: project **34 "StreamBuildTest"**,
prompt **#86** ("add another screen so I can add other peoples wallet
address"), sent through the companion app's Build tab at 03:53 and completed at
04:04:15.

**Rule for this pass: record, do not fix.** Every entry is something observed on
this machine / this phone in this session, with the evidence that proves it.
Where an entry rests on a single observation it says so. Promote anything here
to a real change only after it is reproduced a second time.

Session window: 2026-10-05 03:35 → 04:30 (local, UTC+1).

### Fix round — 2026-10-05, ~14:00–15:30

The "record, do not fix" rule was superseded for the four demo-blocking
entries. What changed, and what proves it:

| Entry | Status | Evidence |
|---|---|---|
| **G-11** push | **FIXED · verified end-to-end** | `lib/push.ts` now posts the **subscription** id (poll + change listener). `users.onesignal_id` = `2a2ca1f5…`; OneSignal accepted `{"id":"a7d901f8…"}` (no `errors[]`); phone showed the notification (`NotificationRecord pkg=com.talon.app … tickerText=delivery…`) |
| **G-10** Metro file map | **FIXED · unit + live-probe tested; live turn pending** | `metro.EnsureFresh` — probe → mtime nudge → same-port restart, run async after every build turn (`streamBuild`). Ladder tested in `freshness_test.go`; the probe URL answered **200 against a real Metro** (and a dead port was detected). Root-cause hardening (watchman) still open — see the entry |
| **G-14** device gate | **FIXED · unit-tested; live ladder run pending** | `run_boot` now boots the workspace through the shipped dev client over Metro (`devclient.LaunchLinkForPort`, proven on device: `talonapp://…` → bundle loaded) when there is no APK, instead of asking for a package nothing installs |
| **G-02** Device tab | **FIXED · regression-tested** | `useLiveSession` no longer reports an unanswered query as idle; the tab shows `checking…` and **no Start button** until the server answers. Two red-before tests in `__tests__/(tabs)/device.test.tsx` |
| **suite red** | **FIXED** | `session-expiry.test.tsx` mocked `@/lib/push` with only `initPush` while `_layout.tsx` has imported `pushAvailable`/`registerPushWithBackend` since `7f5c1f1` — 4 tests failed for a reason they don't exercise. Mock completed; **116/116 mobile, full Go suite green** |

Not fixed, still open: **G-01** (second occurrence recorded), **G-05**,
**G-07**, **G-08**, **G-13** (external edit stands), **G-15**, **G-17** (new).

---

## What passed (so the negatives are read in proportion)

| Check | Result |
|---|---|
| Prompt → code, through the app | **Works.** Send at 03:53 (composer → `POST /v1/prompts/86/answer`), `turn done-verified after 14 rounds in 4m22s (avg 19s/round)`, 10 files, verify policy strict |
| New screen exists and is reachable | **Works.** `app/watchlist.tsx` + 5 `features/watchlist/*` files; home screen gained a **Watchlist** button → `router.push('/watchlist')` → screen renders |
| Feature actually functions | **Works.** Typed `So11111111111111111111111111111111111111112` + nickname → button disabled until the address parsed, then **Add to watchlist** wrote the row `Wrapped SOL / So1111..111112` and cleared the form |
| Dev client launches the workspace | **Works.** Device tab → *Open on this phone* → `talonapp://` → `com.talon.devclient` → wallet home, no error screen |
| Cold-start push re-registration | **Fires.** `POST /v1/users/push-token status=204` at 04:14:54 on a genuine process restart (see G-11 for why delivery still fails) |
| Deep-link routing fix | Holding — no "Unmatched Route" this session |

---

## G-01 · `adb reverse` for the session Metro silently disappears

**Observed.** Backend set it at session start —

```
03:39:26 adb reverse set: tcp:36635 -> tcp:36635
```

— but `adb reverse --list` on the phone later returns only `tcp:8081` and
`tcp:8080` (the two added by hand). The `36635` entry is gone and nothing
re-creates it.

**Blast radius.** The device's only remaining path to the workspace bundler is
the HTTP relay `http://10.32.106.54:17555`, which is **IP-address based**, and
the laptop's tether IP changed four times this session. A dropped reverse plus
one DHCP renewal = the dev client can't load a bundle, and the error reads like
an app bug. The reverse is what makes the link IP-independent; it is set once
and never watched.

**Evidence.** `backend/internal/metro/metro.go:143` sets it; the only removal
path is session teardown (line 168).

**Second occurrence (2026-10-05, ~15:00).** `adb -s 429fd391 reverse --list`
returned **empty** — not just the session's `36635`, but the hand-added `8081`
and `8080` too. A device reconnect/adb-server restart clears *every* reverse
and nothing re-creates them; the dev client's `127.0.0.1:PORT` then lands on a
closed port and reads as an app failure. Re-set by hand
(`adb reverse tcp:8081 tcp:8081`). Confirms the class: reverses are set once
and never watched. (Plumbing that keeps the demo alive regardless: the phone
talks to `localhost` via reverse, and `backend/.env` API URL is `localhost` —
G-01's IP-churn half is neutralised for the demo.)

---

## G-02 · The Device tab states a pre-poll snapshot as fact

**Observed twice.**

*03:50* — badge `Metro activity: unavailable — the bundler is not running for
this session`, while the backend had logged `metro ready for session 30 on
port 36635` (03:39:26) and `[metro 30] Android Bundled 2887ms index.js (1961
modules)` (03:40:07).

*04:18* — badge `no session`, card **"No session. The pairing QR and Metro
relay appear with one."** with a lime **Start session** button, and
`Pair another phone: Loading…` — while `GET /v1/projects/34/live-session`
answered `200 bytes=226` continuously and `sessions/30` was `running`. Polling
resumed at 04:20:30 and the screen corrected itself to `running · bundler
connected · session #30`.

The card that says "Loading…" is honest; the one next to it that says "no
session" is not.

**Blast radius.** Anyone (or any agent) reading that screen concludes the
bundler/session is dead and presses **Start session** — which would create a
second container and a second Metro while #30 and its `expo start` on port
36635 are still alive. This is the exact wrong signal a judge would see first.

**Status: FIXED.** Root cause was one expression: `useLiveSession` called back
with `data ?? null`, so before the first response (react-query's `undefined`)
it reported *idle* — wiping the SecureStore id it had just read. The hook now
stays silent until the server answers and returns
`{ answered, failed }`; the tab renders `checking…` / `session unknown` and
**withholds the Start button** until `answered`. Red-before tests in
`__tests__/(tabs)/device.test.tsx` ("stays in checking until /live-session
answers…" and "keeps the locally known session until the server actually says
idle"); `npx tsc --noEmit` clean; suite 116/116.

---

## G-03 · `verify-state` 404s until the first verification exists

**Observed.** `GET /v1/projects/34/verify-state → 404` (`bytes=37`) at 03:51:31,
then `→ 200` (`bytes=132`) from 04:04:16 onward — i.e. the route exists and the
404 means "no verify state recorded yet".

**Severity: low, reclassified.** It is not a missing route; it is a legitimate
"nothing yet" answer that the client polls every ~30s. Noted only because a
permanent red 404 in the request log is where people look first when hunting a
real failure.

---

## G-04 · Poll noise buries real events

**Observed.** 23 minutes produced 201 `req=` lines, overwhelmingly
`test-info` / `live-session` / `sessions/30` at 5s intervals, all at `INFO`,
sharing the stream with `prompt-sec`, orchestrator rounds and relay auth.
A rotated `backend.log.pre-L59` already exists, which is evidence this has
forced "read the tail and hope" before.

**Worse, during a turn the log goes quiet about the turn itself:**
`POST /v1/prompts/86/answer` only appears when the request *completes*
(`latency_ms=262211`), so a 4½-minute turn shows no request line for its whole
duration. Monitoring a turn by tailing the access log shows nothing happening.

---

## G-05 · Workspace `package.json` still declares the SDK-57 skew

**Observed.** The template fix (commit `7f5c1f1`) pinned three packages to SDK
55, but `backend/workspaces/34/package.json` still says:

```
"expo-file-system": "^57.0.7",
"expo-notifications": "^57.0.21",
"expo-sharing": "^57.0.22"
```

`node_modules` currently holds `55.0.26 / 55.0.27 / 55.0.24`, so nothing is
broken *right now* — the manifest and the install have simply diverged.

**Blast radius.** Latent, and it is the same class as the crash that hid the
whole app behind an error screen (`NoSuchMethodError getDirectConverter`). The
next `npm install` in that workspace re-resolves `^57` onto an SDK 55 native
build and the crash is back. Any workspace generated before `7f5c1f1` carries
the same manifest.

---

## G-06 · Companion Metro served a recorded runtime error (observed once)

**Observed.** `GET http://localhost:8081/` returned Metro's static error page:

```
"Cannot read properties of undefined (reading 'getEnforcing')"
  talonmobileapp/lib/push.ts:12   (import { OneSignal } from 'react-native-onesignal')
  talonmobileapp/app/_layout.tsx:8
```

`TurboModuleRegistry.getEnforcing` undefined means the native `onesignal`
module was missing at that moment. The app subsequently ran normally, so this
is a single observation.

**Status.** Needs a second occurrence before it is called a defect.

---

## G-07 · Session Metro start arguments are rejected by the current Expo CLI

```
[metro 30 err]  --non-interactive is not supported, use $CI=1 instead
[metro 30]  ERROR  An unknown error occurred while installing React Native DevTools.
[metro 30]  FATAL:sandbox/linux/suid/client/setuid_sandbox_host.cc:169
             ... chrome-sandbox ... owned by root and has mode 4755
```

The bundler starts anyway (`metro ready for session 30 on port 36635`).
Cosmetic today, but two of the three lines print as `ERROR`/`FATAL` — the same
place a real bundler failure would appear. Source: `metro.go:69` passes
`--non-interactive`.

---

## G-08 · Deep-link fix is not propagated to pre-existing workspaces

`app/+native-intent.ts` exists in `backend/template/` and in
`backend/workspaces/34/` only. `workspaces/` is gitignored (`.gitignore:66`),
so a fresh clone gets the fix only through the template; every workspace
generated before `7f5c1f1` still routes deep links to "Unmatched Route" until
it is applied per-workspace or regenerated.

---

## G-09 · Build artifacts are tracked and go stale

`backend/template/.tsbuildinfo` and `dev-client/v1.0.0/native-modules.json`
show up as modified on every build. `e2e/__pycache__/`, `external-plugin/` and
`e2e/run-gauntlet-continue.sh` sit untracked (timestamps 2026-10-04, left
alone this pass). Every commit carries two generated files nobody read. Low
severity, pure diff noise.

---

## G-10 · CRITICAL — Metro did not know a file the AI had just written; the dev client hard-crashed

**This is the one that took the app down.**

Timeline:

| Time | Event |
|---|---|
| 03:55 | AI writes `features/watchlist/watchlist-row.tsx` (and siblings) |
| 04:04:15 | Turn completes `done-verified` — typecheck, lint, bundle all green |
| 04:04:56 | Dev client: `DebugServerException: The development server returned response error code: 500` → `UnableToResolveError ... @/features/watchlist/watchlist-row` → **native crash** |
| 04:05:08 | Same crash, second PID |
| 04:07:11 | Same crash, third PID |
| 04:08:11 | `[metro 30] λ Bundling failed ... router-server render.js` — same cause |
| 04:09 | **mtime-only `touch` of that one file** |
| 04:09:04 | `HTTP 200 size=15068519` / `Android Bundled 2732ms index.js (2008 modules)` |

MIUI showed *"TalonApp keeps stopping"* three times; `dumpsys window` reported
`Application Error: com.talon.devclient`.

**The file existed on disk, was readable, and was 14 minutes old** when Metro
still answered 500 for it. `ls` and `md5sum` both saw it; Metro's file map did
not. Touching it — changing nothing but mtime — made it resolve instantly.

**Why this matters more than the crash:** every gate the product relies on was
green. The AI's `run_checks` (tsc, eslint) pass because they read the
filesystem; the verify ladder reported `verified · 10 files`; only the *live*
Metro instance that serves the phone disagreed — and its disagreement is
delivered as a **native crash dialog**, not as a readable build error in the
product's own UI. A judge tapping *Open on this phone* after a successful turn
gets "app keeps stopping".

**Not yet determined:** why exactly that one file was missed (its four siblings
created in the same batch resolved fine). inotify queue overflow
(`fs.inotify.max_queued_events` = 16384, `max_user_instances` = 128) is the
leading suspect but has **not** been confirmed — this is a single reproduction.

**Status: FIXED (self-heal), root cause still open.** `metro.EnsureFresh`
(`backend/internal/metro/freshness.go`) runs the ladder **after every build
turn** — `streamBuild` → `go h.ensureMetroFresh(projectID)`, in the background
while the reply is being read, never on the launch path:

1. **probe** the entry bundle — the exact request the dev client makes at
   launch (`…/index.bundle?platform=android&dev=true&…`). Healthy costs one
   cached request *and warms the bundle*, so the phone's first launch after a
   turn is already built.
2. **nudge** — bump mtimes across the workspace's own sources
   (`node_modules`/`.git`/build output skipped), content untouched.
3. **restart** — last resort, on the **same port** (adb reverse + the relay
   captured it once) with the emitter carried over so subscribers stay
   attached.

Verified: ladder unit-tested (`freshness_test.go`, incl. the defeat path),
and the probe URL answered **200 against a real Metro** live (41s cold build),
with a dead port correctly reported as unreachable. What is *not* yet proven
live is a whole turn → auto-heal sequence — that lands during demo rehearsal.

**Root cause remains open:** Metro runs with **no watchman** on this machine
(`watchman` not installed; apt has 4.9.0-9build3 but install needs sudo), so
the file map rides the node watcher that missed the create in the first place.
Watchman is the durable fix; the probe is the safety net that makes the miss
invisible to a judge. Install when a password is available:
`sudo apt-get install -y watchman`.

---

## G-11 · CRITICAL — push still cannot be delivered; the id we register is not a player id

The payload field was fixed in `7f5c1f1` (`include_external_user_ids` →
`include_player_ids`) and the running binary **provably contains the fix**:

```
$ strings $(readlink -f /proc/367091/exe) | grep include_
json:"include_player_ids"
```

Yet with the fix live, delivery still fails, twice:

```
03:59:42 notify: input_needed  push to user 1 failed: onesignal: All included players are not subscribed (target 208ca8ae-935e-4a58-a39f-49183c2c2561)
04:04:16 notify: build_success push to user 1 failed: onesignal: All included players are not subscribed (target 208ca8ae-935e-4a58-a39f-49183c2c2561)
```

**The id is wrong, not the field.** From the device's own OneSignal store
(`run-as com.talon.app cat shared_prefs/OneSignal.xml`), this phone holds two
different UUIDs:

```json
"MODEL_STORE_identity":   [{"onesignal_id": "208ca8ae-935e-4a58-a39f-49183c2c2561", ...}]
"pushSubscriptionId":      "2a2ca1f5-7a69-498f-aef0-dc489df992cd"
"MODEL_STORE_subscriptions": [{"id": "2a2ca1f5-7a69-498f-aef0-dc489df992cd",
                               "type": "PUSH", "optedIn": true,
                               "status": "SUBSCRIBED",
                               "address": "ceD5nYVTSTKNyYnFka8BFv:APA91bFlaOGP9F0TfkEIbyCNzG-SF5YHP98vK5q_Uv2XqNBxTj7I9EjWIZxJhoWVx7Nd0cGbKrBXEGiYo0w8Xp9yHHEghwmtUlODYhgKbsyxyqHkHZUwB6s"}]
```

* `users.onesignal_id` = **`208ca8ae…`** — the OneSignal **user/identity** id,
  which is exactly what `OneSignal.User.getOnesignalId()` returns.
* The **push subscription (player) id** is **`2a2ca1f5…`**, `SUBSCRIBED`, with a
  live FCM token — and nothing in the backend has ever seen it.

`include_player_ids` is being handed an identity id, so OneSignal answers
"All included players are not subscribed". Sending `2a2ca1f5…` should deliver;
that is the hypothesis to test when someone is allowed to fix.

**Also corrected in this pass:** the earlier theory that "the app just needs one
relaunch so the cold-start re-registration writes the correct id" is **wrong**.
The app did cold-start (force-stop + launch, 04:14), `registerPushWithBackend()`
did run and did answer `POST /v1/users/push-token 204` — and it posted
`getOnesignalId()`, i.e. `208ca8ae…` again. Relaunching cannot fix an id-type
mismatch.

**Observability defect that hid this:** `registerPushWithBackend()` returns `''`
from four different places (`pushAvailable` false, no device id, no token,
exception) and every one of them is silent — `catch { return '' }`. A failed
registration and a skipped registration are indistinguishable from outside.

**Status: FIXED · delivery proven end-to-end (2026-10-05, 14:03–14:17).**

* Client: `lib/push.ts` posts the **subscription** id — `getIdAsync()` polled
  (≤15s, OneSignal assigns it asynchronously) plus a `pushSubscription`
  `change` listener that re-posts the moment it arrives, deduped, and
  `console.warn` instead of a silent catch. `POST /v1/users/push-token` →
  `204` ×2 at 14:03:12; `users.onesignal_id` for user 1 moved from
  `208ca8ae…` (identity) to **`2a2ca1f5…`** (subscription).
* Wire: OneSignal accepted the same payload the backend now builds —
  `{"id":"a7d901f8-e599-4210-b695-4b2576f66e80"}`, **no `errors[]`** (the
  failing shape was `{"id":"","errors":["All included players are not
  subscribed"]}`).
* Device: `NotificationRecord(… pkg=com.talon.app … channel=
  fcm_fallback_notification_channel …)`, `tickerText=delivery…`,
  `when=` 14:17 — a real FCM delivery, not an accepted request.

Corrections landed with it: `notify.go`'s payload comment no longer claims the
id comes from `getOnesignalId()` (it names the subscription id and the G-11
bug), and the local `extID` is now `subID`. What remains unproven is a push
triggered by a *product* event (build success/failure) rather than a direct
REST send with the backend's own credentials — the dispatch path itself is
unchanged and covered by `TestPushTargetsPlayerIDsNotExternalIDs`.

---

## G-12 · The companion app itself segfaulted once (observed once)

```
04:11:26 F/libc   Fatal signal 11 (SIGSEGV), code 2 (SEGV_ACCERR)
                  in tid 17197 (mqt_v_js), pid 16892 (com.talon.app)
04:11:27 F/DEBUG  Process uptime: 25s
                  #01 libreactnative.so  facebook::react::MountingCoordinator::pullTransaction(bool)+524
                  #02 libreactnative.so  facebook::react::FabricUIManagerBinding::schedulerDidFinishTransaction(...)
                  #04 libreactnative.so  facebook::react::UIManager::shadowTreeDidFinishTransaction(...)
                  #22 libhermesvm.so …
```

A heap-access violation (`SEGV_ACCERR`, fault addr inside `[anon:libc_malloc]`)
during a **Fabric** shadow-tree commit — not in OneSignal, not in app JS. The
same bundle had run for 20 minutes beforehand, and the immediate relaunch
survived 25+ seconds with no repeat, so this is currently a one-off.

**Blast radius.** If it recurs it kills the companion during a build turn, and
the turn keeps running on the server with nobody watching it.

---

## G-13 · ESLint heap-crashes on the generated bundle; the durable fix lives in a file the agent may not write

**Observed in the AI's own report (turn #86):**

```
FATAL ERROR: Ineffective mark-compacts near heap limit
Allocation failed - JavaScript heap out of memory
```

`run_bundle` writes a **26 MB** minified bundle to
`.expo-bundle/_expo/static/js/android/index-*.js`; `eslint.config.js` ignores
`.expo/`, `node_modules/`, `android/`, `ios/` but not `.expo-bundle/`, so ESLint
parses it until V8 passes 2 GB and aborts. The agent worked around it by
deleting the artifact and reported:

> "The durable fix needs you … I could not apply it, that file is
> template-managed and blocked my edit."

**Update:** `backend/template/eslint.config.js` *was* edited at **04:17:37** —
after the turn ended at 04:04 — to add `.expo-bundle/` and `.tsbuildinfo`. That
edit did **not** come from this session. Either another agent session (two
`opencode` processes are running) or the user applied exactly what the AI
asked for. Recorded as an external change, not reverted.

---

## G-14 · The agent's device gate can never pass, by construction

The AI closed its turn with:

> "Device verification remains **pending, not done**: the attached device is
> showing the MWA 'Fake dApp' harness, Metro has never produced a bundle, and
> my app's package is on none of the three connected devices, so this is
> verified statically only."

**Both installation claims were checked and are TRUE:**

```
$ adb shell pm list packages | grep talon
package:com.talon.app
package:com.talon.devclient
package:host.exp.exponent          # com.talon.streambuildtest is absent
app.json → "package": "com.talon.streambuildtest"

$ adb -s localhost:15555 shell dumpsys window | grep mCurrentFocus
mCurrentFocus=Window{… com.solana.mobilewalletadapter.fakedapp/.MainActivity}
```

So the verify ladder's device probe asks for `com.talon.streambuildtest`, which
**the product never installs** — the generated JS is served into the shared
provisioned dev client (`com.talon.devclient`) over Metro. And the session
container (port 15555) genuinely boots to the MWA fake-dapp harness.

**Consequence:** "device verification pending" is not a transient state this
turn failed to reach; it is a state **no turn can reach**. The ladder reports
`verified · 10 files` (static only) forever, while the human path — Device tab
→ *Open on this phone* → dev client → Watchlist — works fine. The gap between
what the badge says and what a judge experiences is the whole issue.

*(One sub-claim does not hold: "Metro has never produced a bundle" — the
backend logged `Android Bundled 2887ms index.js (1961 modules)` at 03:40:07,
before the turn. Its `metro_status` probe disagrees with the bundler's own log;
single observation, needs a second before it is called a defect.)*

**Status: FIXED (unit-tested; live ladder run pending).** `run_boot` no longer
stops at "boot skipped" when this project has no APK. It now boots the path
the product actually uses — the shipped dev client loading the workspace from
Metro — and holds it to the *same* evidence standard as the APK path: `pm path`
proves the client is installed, `logcat -c` then launch, `pidof` polled for 5s,
and the first post-launch log line naming the package is what separates
"booted" from "process exists but ran no code". Summary reads
`dev client com.talon.devclient loaded the workspace from Metro :PORT — …`.

Wiring: `tools` deliberately does not import `devclient` (the
`AllowlistInterface` boundary), so `buildsession.go` passes
`DevClientPkg`/`DevClientLaunchLink` in. The launch shape lives once, in
`devclient.LaunchLink`, used by both this gate and `test-info`'s pairing link —
pinned by `TestLaunchLinkForPortWrapsTheLoopbackDeepLink`.

**Proven on device:** `talonapp://expo-development-client/?url=exp%3A%2F%2F127.0.0.1%3A8081`
→ Metro 22→24 lines, activity moved from the launcher home into
`com.talon.devclient/.MainActivity`. The bare `exp://` + explicit component
form that `launchArgs` uses does **not** load the dev client (observed: 0 new
Metro lines, parks on the launcher home) — recorded so nobody "simplifies" the
link back to it.
Still to prove live: the ladder running inside a real turn against a session
container (needs a started session — rehearsal, not a code gap).

---

## G-15 · A route file has no default export

On companion boot:

```
04:11:26 W/ReactNativeJS: 'Route "./onboarding/screens.tsx" is missing the required
default export. Ensure a React component is exported as default.', { [Stack] name: 'Stack' }
```

The file exists and is registered as a stack screen but exports no component.
Single observation, printed once per cold start.

---

## G-16 · Tooling notes for anyone re-running this (my tools, not the app)

- **`uiautomator dump` fails intermittently** on this MIUI build with
  `FileNotFoundException: /data/system/theme_config/theme_compatibility.xml`
  and writes a 0-byte file. It recovers on retry (it worked again later in the
  same session). No root available to fix it (`adbd cannot run as root in
  production builds`).
- **`screencap` returns stale frames** — screenshots showed clocks 2–3 minutes
  behind the device while claiming to be live, which initially made a working
  screen look broken and a broken screen look working. Always compare the clock
  in the image against `adb shell date`, and prefer a hierarchy dump for
  ground truth.

---

## G-17 · `dev.sh` truncates the backend log on every restart

**Observed.** `.dev-logs/backend.log` was 167 KB of evidence at 04:59 (push
failures, `[metro 30]` lines, turn timings) and 3.6 KB after the 13:24 restart —
`dev.sh` re-opens the file with `>`, so every restart destroys the log the
previous run produced. The G-11 failure lines quoted in this document survive
only because they were copied here first.

**Blast radius.** Mid-demo debugging: a restart to clear a problem also erases
the evidence that explained it. Fix is `>>` plus a rotate (or `tee -a`), kept
out of this round deliberately — a logging change during demo week touches
every command's output path.

---

## Watching

| # | Signal | Source |
|---|---|---|
| prompt 86 | status / round / files_touched | `prompts` table |
| workspace 34 | md5 of `app/`, `features/`, `components/` | `/tmp/opencode/ws34.before.md5` vs `after` |
| orchestrator | rounds, tool calls, errors | `.dev-logs/backend.log` |
| push | `POST /v1/users/push-token`, `notify: … failed` | `.dev-logs/backend.log` |
| bundler | serve/reload of the new route | `[metro 30]` lines + device |
| crashes | `logcat -b crash` | device |

**Open questions for the fix round (in priority order):** ✅ G-10 → ✅ G-11 →
✅ G-14 → G-01 → ✅ G-02 → G-05.

**Still open, in demo-week priority:** (1) **G-10 root cause** — install
watchman (needs sudo); the probe is the safety net, watchman is the cure.
(2) **G-01** — re-assert `adb reverse` after any device reconnect (or watch it).
(3) **G-14 / G-10 live proof** — one rehearsal turn against a session, reading
the `metro: bundle probe` / `dev client … loaded the workspace` lines.
(4) **G-15** — `onboarding/screens.tsx` missing its default export (prints on
every companion boot). (5) G-05 / G-07 / G-08 / G-13 / G-17 — after the demo.
