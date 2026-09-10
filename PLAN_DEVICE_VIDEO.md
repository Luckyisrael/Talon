# PLAN_DEVICE_VIDEO.md — the DV phases: live device feedback, end to end

Status: **all five phases shipped** (DV1–DV5). Each landed as one commit
with its tests and phase guard; backend `go test ./...` and frontend
`vitest` stayed green throughout.

The problem this solves: the agent changes code, and the only feedback
loop was a screenshot round-trip. Metro printed its bundle progress into
a log nobody read; phones could not test at all; every preview frame was
a host-side JPEG encode; boots cost the user two minutes; and the agent
had no way to author an end-to-end flow.

## DV1 — the bundler's voice (f131f47)

Metro's stdout/stderr is classified into an event vocabulary —
`bundle_start`, `bundle_progress` (with percent), `bundle_done`,
`reloaded`, `error` (redbox), `log`. The vocabulary is one pure
classifier; a bounded drop-oldest emitter fans it out (a wedged consumer
degrades to recent, never stalls the bundler's stdout pump). Three
consumers:

- **SSE** `/v1/projects/{id}/metro` — honest `unavailable` when no
  device/bundler, `connected` frame, 15s keepalive.
- **`metro_status` tool** (device-gated): the agent reads the bundler's
  tail without a screenshot. Closes the loop between "I changed code"
  and "what does the device show".
- **PhonePreview badge**: `bundling 45%` in the top strip (self-dismissing),
  `reloaded` flash, and a persistent redbox banner over the stream until
  the next good bundle clears it.

## DV2 — scan to test on your phone (a631588)

A session-scoped **TCP relay** (backend/internal/relay) bridges the
session's metro to a public port (17555–18555, disjoint from the
container ADB range) — self-hosted, no third-party tunnel. Remote
phones load the app through it; LAN phones hit metro directly
(`--host lan`). Bounded: 8 conns/relay, 10-min idle deadline, dies with
the session. Metro binds 0.0.0.0.

`/v1/projects/{id}/test-info` returns the honest verdict: available
(deep link `exp://PUBLIC_HOST:port`, metro http:// fallback, APK link
when the build exists) or unavailable with the reason. `PUBLIC_HOST`
names the externally reachable host. `/v1/projects/{id}/dev-client.apk`
serves the prebuilt dev client to the project's owner.

The **QR** is a vendored dependency-free encoder (npm's project-scoped
install policy blocked the dep) — byte mode, EC level M, v1–v9. Proven
by an **independent decoder** in the test: decode(encode(s)) === s over
the deep-link corpus, format info verified against ISO 18004's L-level
worked example, RS remainder checked per block. The TestOnPhone panel
renders the QR, a copyable link, and the APK button.

## DV3 — the device's own encoder (deb4892)

scrcpy's video socket (video-only, max_size 1080, max_fps 30, 2 Mbps)
replaces the 30fps screencap→JPEG pipeline: the device's hardware
encoder produces H.264, the host relays NAL bytes untouched, WebCodecs
decodes on the client GPU. Own scid/port — decoupled from the control
socket, both directions.

Negotiation-first: the first WS frame is text `{"mode":"h264","w":W,"h":H}`
or `{"mode":"jpeg"}`. JPEG is the standing fallback — no WebCodecs
client, scrcpy video failure, or negotiation write failure all land
there, and the client decodes whatever mode it was told. The client's
chunk classifier reads the NAL header (SPS/IDR → key, else delta) — the
type is decode-order metadata, a lie there corrupts the stream.

## DV4 — boot prepaid, idle bounded (970d81e)

**Warm pool** (`backend/internal/redroid/pool.go`): containers booted
ahead of demand (WARM_POOL_SIZE, default 1). Claim on session create
renames the container into the session's name and rebooks the port — no
free/allocate race. Warm boots run under negative ids (no collision with
real sessions). A pool miss is the cold path, never an error.

**Idle reaper** (`backend/internal/api/reaper.go`): no stream viewer and
no agent turn for 10 minutes → container paused; 30 minutes → destroyed
through `reapDestroy` — the exact Destroy semantics (metered, relay
stopped, port freed; one teardown law, two callers). The clock is
TouchSession (socket joins, agent turns — device chatter is the device
talking to itself). A viewer joining a paused container wakes it.
`last_active_at` defaults to `started_at`, so pre-DV4 rows age honestly.

## DV5 — the Maestro E2E gate (4d2ef0f)

`run_e2e` executes `.maestro/flows/*.yaml` against the session's device
(ANDROID_SERIAL, 3-minute kill bound per flow). Maestro over Espresso
for the same reason the ladder's gates are scripts: YAML the agent
writes without a build step. Honesty law: no device / no maestro / no
flows = passed:true + "skipped" summary; a failing flow = passed:false
with the flow name + output tail (the crash evidence). Device-gated
registration; `run_e2e` is in `isDeviceStep`, so its failures join the
run-scoped crash index.

## The invariants that carried through

- **Honest states**: unavailable is a message, never a fabricated frame,
  QR, or verdict. Every skip names why.
- **Bounded everything**: emitter 64, metro buffer 32, relay 8 conns +
  10-min idle, stream queue 3, e2e 3 min/flow, reaper 10m/30m, pool 1.
- **Validate at boundary**: QR decode proves encode; the scrcpy header
  parser is pinned against a synthetic socket; the reaper clock is tested
  against aged rows.
- **One teardown law**: user destroy and reaper destroy share the exact
  semantics, metering included.
- **Shadow, don't replace**: the JPEG path is the standing fallback
  behind the H.264 negotiation, never deleted.

## Follow-ups (not blockers)

- Install maestro on the host to light up run_e2e live (the gate skips
  honestly without it).
- Set `PUBLIC_HOST` in deployment for real phone deep links (localhost
  is the honest single-machine default).
- The H.264 path is untested against a live redroid encoder — the
  synthetic-socket header test pins the parse; a live smoke belongs to
  the next on-device session.
- Maestro on redroid needs a launcher intent; if `.maestro/` flows
  target the dev client, `appId` must match its package.
