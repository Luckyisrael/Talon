---
name: mobile-motion
description: The motion bar for Talon screens — the frequency gate (should this animate at all), gesture-driven springs seeded with finger velocity, narrative timing curves, worklets and the UI thread, Reduce Motion. Load before writing any react-native-reanimated code, Animated.timing, gesture, sheet, or transition.
version: "appllama-app-design-skill@1.3.0 motion reference — adapted; Appllama MIT"
autoAttach: true
triggers: [import:react-native-reanimated, import:react-native-gesture-handler, content:useAnimatedStyle, content:withSpring, content:withTiming, content:scheduleOnRN, content:useSharedValue, content:runOnJS, content:Animated.timing]
license: MIT (Appllama — see ATTRIBUTION.md)
source: https://github.com/appllama/appllama-skills (skills/appllama-app-design-skill/SKILL.md + references/motion.md)
---

# Mobile motion — the bar

Motion quality is judged in the first ten seconds. Most screens need less of
it, not more. Decide in this order.

## The frequency gate comes first

- **Met 100+ times a day** (tab switch, keyboard, scroll, back) → the
  platform default and nothing else. Tabs never slide; screen transitions
  stay native.
- **Tens a day** (press, row select) → near-imperceptible, under 150 ms.
- **Occasional** (sheets, modals, toasts) → standard motion.
- **Rare, first-time moments** → delight is allowed.

Passing this gate with zero lines of code is a success. When unsure, the
strongest move is to **delete the animation**. Every animation needs its
purpose named in one word — feedback, spatial continuity, state change,
preventing a jarring cut, explanation, delight — or it does not get built.
Data the user is reading never moves for style.

## Two families, never mixed

| Family | Driver | Curve |
|---|---|---|
| **Responsive** (user is touching it) | Gesture position/velocity | Spring, seeded with the gesture's velocity |
| **Narrative** (system initiated) | Time | `withTiming`, ease-out, 150–350 ms |

Mixing them is the top tell of non-native motion: a sheet that closes on a
fixed 300 ms timing after a fling feels dead; a button that springs for 800 ms
on tap feels like a toy.

## Springs

```ts
// The designer form (duration is perceptual): critically damped, no oscillation
const SNAP = { duration: 400, dampingRatio: 1 }
// Playful: one soft overshoot — use sparingly (celebrations, a mascot)
const PLAYFUL = { duration: 300, dampingRatio: 0.8 }
```

If a finger was involved, it's a spring: capture the live value on grab, hand
the release velocity into the spring, pick the target from projected momentum
so a flick commits, rubber-band past boundaries, stay grabbable mid-flight. One
vocabulary per app; bounce only when the gesture carried momentum.

## Everything else is timing

- Under 300 ms, strong ease-out: `Easing.bezier(0.23, 1, 0.32, 1)` — the
  built-in curves are too weak; **never ease-in on an entrance**.
- Press feedback lands on press-**in**, 100–150 ms: scale 0.97 on buttons and
  cards, a background highlight (never scale) on list rows, opacity on bar
  buttons.
- Exits are faster than entrances and leave the way they came in. Enter from
  `scale(0.95)` + fade, never `scale(0)`. Menus grow from their trigger.
- **Never animate a header's height** — translate inside a fixed clip.

## The thread law

Gesture → animation never hops the JS thread. Worklets and shared values
(`.get()` / `.set()`; `scheduleOnRN` — Reanimated 4's `runOnJS` — only at
gesture end), `transform` and `opacity` only, no `entering` on recycled list
rows, keyboard-tracking UI via `react-native-keyboard-controller` never a
keyboard listener plus a guessed duration.

## Reduce Motion

Spatial motion collapses to cross-fades; native transitions stay the system's.
The device setting is respected, not approximated.

## The bar

60 fps through the hero flow, measured on a **release build** — dev builds and
 Expo Go hide exactly the jank being hunted. Watch the recording once for feel,
once frame by frame, and again next day with fresh eyes. On Talon, the
automated evidence is the verify ladder: `run_boot` for the cold start, the
screenshot for stills, `run_interact` for the flow. Profile, do not guess.
