---
name: motion-review
description: Review animation and motion code against the Talon motion bar — flags sluggish, mis-originated, janky, or purposeless motion with fixes. Load when reviewing a diff that touches animation, gestures, sheets, haptics, or transitions. Does not write features or review non-motion code.
version: "emilkowalski/skills review-animations — adapted; Emil Kowalski MIT"
autoAttach: false
license: MIT (Emil Kowalski — see ATTRIBUTION.md)
source: https://github.com/emilkowalski/skills (skills/review-animations/SKILL.md)
---

# Motion review — the bar, enforced

You review motion against `mobile-motion`, which is the single source of
rule values (curves, durations, spring configs). This skill is the method:
default to flagging, approval is earned. A transition that runs but feels
sluggish, lands from the wrong origin, fires too often, or drops frames is
a regression, not a pass.

Output findings as a table, one row per issue — never a list:

| Before | After | Why |
|---|---|---|
| `withTiming(..., { easing: Easing.in })` on entrance | `Easing.bezier(0.23, 1, 0.32, 1)` | ease-in delays the exact moment watched |
| `scale: 0` entrance | `scale(0.95)` + fade | nothing appears from nothing |

## The ten checks

Every animation in the diff is measured against these. A violation is a finding.

1. **Justified motion.** One-word purpose (feedback, spatial continuity,
   state change, preventing a jarring cut, explanation, delight) or it
   does not ship. "Looks cool" on a frequently-seen element is a block.
2. **Frequency-appropriate.** 100+/day actions get platform default and
   nothing; tens/day get near-imperceptible; occasional gets standard.
3. **Responsive easing.** Entrances use strong ease-out
   (`Easing.bezier(0.23, 1, 0.32, 1)`); ease-in on UI is a block.
4. **Sub-300 ms UI.** Slower needs a stated reason. Navigation matches the
   platform, everything else beats it.
5. **Origin and physicality.** Menus grow from their trigger, never center
   (modals exempt); entrances start at `scale(0.95)` + fade, never
   `scale(0)`.
6. **Interruptibility.** Gesture-driven motion uses springs that retarget
   from current state; rapidly-triggered UI uses interruptible transitions,
   never restarting keyframes.
7. **UI-thread only.** `transform` and `opacity`; no `setState` in gesture
   or scroll handlers; no shared read/write during render; `.get()` /
   `.set()` form; `'worklet'` first line in worklet-called functions.
8. **Reduced motion honored.** Gentler, not zero — opacity and color stay,
   translation/scale/parallax/overshoot go.
9. **Asymmetric timing.** Deliberate phases run slower, system responses
   snap. Symmetric press-and-release timing is a finding.
10. **Cohesion.** Motion matches the app: crisp and fast here, never
    bouncy without a gesture behind it. When unsure, deleting wins.

## Escalate on sight

`PanResponder`; `setState` per gesture frame; `runOnJS` (use
`scheduleOnRN`, and only at gesture end); shared read/write during render;
core `Animated` on anything a finger touches; layout-property animation
(`height`, `margin`, `flex`, `top`); `BlurView` intensity or Android
`elevation` animation; `entering` on recycled list rows; hand-rolled
screen transitions or tab slides; haptics per frame, unpaired, or as the
only feedback; feel judged anywhere but a release build on the slowest
supported device.

## Remedy in this order

Delete → reduce → fix easing → fix origin → make interruptible → move to
the UI thread. Then verify on the ladder: `run_boot` for cold-start
motion, the screenshot for stills, `run_interact` for flows. State the
verdict per finding: block or approve.
