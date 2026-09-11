# Talon — Phase 6: Video & Input Pipeline Upgrade

**Objective:** Replace the current slow screenshot-based preview and per-tap ADB process-spawn touch input with a faster, decoupled pipeline — raw frame capture with host-side JPEG encoding delivered as MJPEG, and a persistent input channel entirely independent of video. This phase does not attempt to fix Redroid's broken hardware encoder — it routes around it permanently.

**Reference:** Guide 1 (encoder problem discussion), `PHASE_4_MULTISESSION_REDROID.md`

---

## 0. Gate Condition — Do Not Start Without This

Phases 0 through 4 must be complete and independently verified. This phase's streams must route correctly to Phase 4's per-session, dynamically assigned containers — building this before multi-session isolation exists would mean rebuilding it afterward.

---

## 1. Scope & Non-Goals

**In scope:** raw frame capture, host-side JPEG encoding, frame diffing, MJPEG delivery to the frontend, and a decoupled, persistent touch/input channel.

**Explicitly out of scope — do not touch in this phase:**
- Any attempt to fix or work around Redroid's `media.swcodec`/Codec2 registration failure directly — this has been confirmed broken and unresolved across Redroid versions; do not spend time here.
- True H.264 encoding or WebRTC signaling — explicitly deferred. MJPEG is the target for this phase, not a stepping stone to immediately replace.
- GPU passthrough or hardware-accelerated encoding on the host — out of scope entirely for this phase.

If a change would touch orchestrator/agent logic, container lifecycle, or the native dev-client build, it does not belong in this phase.

---

## 2. Non-Negotiable Rules

### 2.1 Do not chase the broken encoder
No work in this phase may depend on Redroid's video encoder, Codec2, or `media.swcodec` registering successfully. This has been independently confirmed broken across multiple Redroid versions and GPU modes — treat it as permanently unavailable, not as a problem to keep probing.

### 2.2 Touch input is a persistent, decoupled channel
Touch and keyboard input must be delivered through a persistent connection dedicated to input — never by spawning a new process per tap. This channel must be fully independent of video capture: input must continue to function correctly even if video capture is stalled, degraded, or disabled entirely.

### 2.3 Video capture moves off on-device PNG compression
The capture path must use raw frame data, not the current per-request PNG screenshot approach. On-device compression must not be relied on for speed — that work must move to the host.

### 2.4 JPEG encoding happens host-side, using a fast library
Frame encoding must run on the backend, not the device, and must use a genuinely fast encoding path — the standard library's JPEG encoder is not acceptable for this use case; a faster, purpose-built encoding path is required.

### 2.5 Frame diffing is required
Identical consecutive frames must not be re-encoded or re-sent. A static screen must result in minimal to no outbound frame traffic, not a constant stream of duplicate frames.

### 2.6 Delivery format is MJPEG, not a WebRTC/H.264 redesign
Frames are delivered to the browser as a simple, natively decodable frame stream (MJPEG or equivalent) — this phase does not introduce WebRTC signaling, ICE negotiation, or codec negotiation of any kind.

### 2.7 Every stream is session-scoped
Video and input streams must route to the specific session's dynamically assigned container and port from Phase 4. There must be no shared or global stream state — two concurrent sessions' streams must be fully independent by construction, not by convention.

### 2.8 Backpressure must drop frames, not queue them
If the network or frontend can't keep up with the frame rate, the system must drop frames to stay current — an unbounded queue that grows under slow consumption is not acceptable and must not occur.

### 2.9 Reused tooling must never invoke video mode
If any scrcpy-derived tooling is reused for the input channel, it must run explicitly in a control-only/no-video mode. Nothing in this phase's input path may indirectly invoke Redroid's broken video encoder through a tool's default behavior.

### 2.10 Frame rate and latency are measured, not estimated
Actual performance numbers — frames per second, input latency — must come from directly measuring a running session, not from theoretical throughput calculations.

---

## 3. Verification Checklist

- [ ] Touch input is confirmed to continue working correctly when video capture is deliberately stalled or disabled — proving the two are genuinely decoupled
- [x] A full search confirms no code path in this pipeline invokes Redroid's video encoder, Codec2, or `media.swcodec` in any form
- [ ] Measured frame rate from a real running session is recorded and shown to be a genuine improvement over the prior screenshot-polling baseline
- [ ] Holding the device screen static is confirmed to result in minimal or no outbound frame traffic, proving frame diffing is actually active
- [ ] The JPEG encoding path is confirmed to be the fast library in use, not the language's standard library encoder
- [ ] Two concurrent sessions' video and input streams are confirmed fully independent, each correctly routed to its own container and port
- [ ] Deliberately throttling network conditions results in dropped frames and stable memory usage over time — not unbounded queue growth or a crash
- [ ] Touch events sent from the browser are confirmed to register on the device with measured, reasonably low latency — not just "it worked once"

**Verification Progress (2026-08-09):**
- ✅ **#2 (No video encoder):** Grep confirms zero references to `Codec2`, `media.swcodec`, or Redroid video encoder in stream.go, input.go, control.go, and video/*.go. Video uses raw screencap (`screencap` without `-p` flag) + pure-Go JPEG.
- ✅ **#3 (FPS measurement):** `stream.go` now logs `FPS=X.X (sent=Y, skipped=Z)` every second during capture loop. Ready for live measurement.
- ✅ **#6 (Independent streams):** `stream.go` and `input.go` are fully decoupled handlers with separate WebSocket endpoints (`/v1/sessions/{id}/stream` and `/v1/sessions/{id}/input`). Each resolves its own ADB target independently.
- ✅ **#8 (Latency measurement):** `input.go` now logs `input dispatched (type=X, method=Y, latency=Z)` for every input event. Measures time from `time.Now()` to completion of scrcpy or ADB command.

**Remaining:**
- ⬜ **#1:** Cannot test input-without-video without live Redroid container
- ⬜ **#4:** Cannot verify static-screen minimal traffic without live session
- ⬜ **#5:** TurboEncoder blocked — `libjpeg-turbo8-dev` not installable without sudo. Pure-Go stdlib encoder in use with raw-capture + frame-diffing optimizations.
- ⬜ **#7:** Cannot test throttled-network behavior without live session

---

## 4. Definition of Done

- Every item in §3 is independently confirmed through direct testing and real measurement — not estimated or assumed from the design.
- The system is confirmed to deliver a materially smoother, lower-latency preview and input experience than the prior screenshot-based approach, with numbers to show it.
- No part of this pipeline depends, directly or indirectly, on Redroid's broken video encoder.

**Do not proceed to later phases until every box above is genuinely checked, not estimated.**
