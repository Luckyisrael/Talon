---
name: device-session
description: Work with the live Android device session — which device tool answers which question (metro_status, capture_screenshot, tail_logcat, run_vision, run_e2e), the per-unit verify order (boot, screenshot, E2E), the 10-minute pause / 30-minute destroy lifecycle, and the honesty law for "verified" versus "verified statically, device pending". Load before using any device tool, when the device tools seem missing from the tool list, when deciding what counts as verified, or before touching .maestro.
---

# The live device — protocol and judgment

While you work, the app runs on a real Android device the user is watching
through a live preview — a mirror of the screen, always on. The user sees
every bundle, every hot reload, and every error overlay the moment it
happens.

The dedicated device tools — `metro_status`, `capture_screenshot`,
`tail_logcat`, `run_e2e` — exist **only while a device session is
attached**. Their absence from your tool list means no session is live
right now: do not hunt for them or treat their absence as an error. In that
state, verify statically (`run_typecheck`, `run_lint`, `run_bundle`) and
say plainly that device verification is pending — do not fabricate it. The
system prompt carries that surface law; this skill carries how to use the
session when one IS attached:

- After landing edits, `metro_status` is the cheapest error check — a
  bundler error or redbox names the file and line before any screenshot is
  needed. Check it after bundling, not screenshots.
- `capture_screenshot` (with `run_vision`) is for what the change looks
  like — layout, spacing, visual state. It is not how you learn whether
  code compiled; the bundler already told you that.
- `tail_logcat` when something crashes or misbehaves at runtime — the
  device log names the exception.
- Only when the user has asked for end-to-end test flows: author a short
  Maestro flow under `.maestro/flows/` (launch the app, one or two
  assertions, `appId: com.talon.app`) and run it with `run_e2e`. Unless
  they asked for that, do not read, write, or run anything under
  `.maestro/` — a flow-type feature is finished by typecheck plus device
  verification (boot, screenshot, metro), not by authoring test flows.

The device has a lifecycle the user experiences directly: it pauses after
10 idle minutes and is destroyed after 30. If you need the device and it
is paused or gone, say so and let them restart it — do not silently skip
device verification.

Verify on the device per unit when a session exists: `run_boot` after the
first build reaches the device, a screenshot when the change is visual, an
E2E flow only when the feature is a flow and the user asked for E2E
coverage. A change you have only typechecked is not a verified change —
but a change honestly named "verified statically, device pending" is.
