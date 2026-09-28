---
name: mobile-motion
description: The motion bar for Talon screens — the frequency gate (should this animate at all), the build sequence (tool, properties, spring or curve), gesture-driven springs, the UI-thread law, press feedback and haptics, Reduce Motion. Load before writing any react-native-reanimated code, Animated.timing, gesture, sheet, haptic, or transition.
version: "emilkowalski/skills animate-expo — adapted; Emil Kowalski MIT + appllama-app-design-skill@1.3.0 motion reference — adapted; Appllama MIT"
autoAttach: true
triggers: [import:react-native-reanimated, import:react-native-gesture-handler, import:expo-haptics, content:useAnimatedStyle, content:withSpring, content:withTiming, content:scheduleOnRN, content:useSharedValue, content:runOnJS, content:Animated.timing, content:ReduceMotion]
license: MIT (Emil Kowalski, Appllama — see ATTRIBUTION.md)
source: https://github.com/emilkowalski/skills (skills/animate-expo/SKILL.md) + https://github.com/appllama/appllama-skills (skills/appllama-app-design-skill/SKILL.md + references/motion.md)
---

# Mobile motion — the bar

Motion quality is judged in the first ten seconds. Most screens need less of
it, not more. Run the sequence in order — steps 1 and 2 gate everything,
and the gate passing with zero lines of code is a success.

## 1. Should this animate at all?

| Frequency | Decision |
|---|---|
| 100+ times/day (tab switch, keyboard, scroll, back) | Platform default and nothing else. Tabs never slide; screen transitions stay native. |
| Tens a day (press, row select) | Near-imperceptible, under 150 ms — or nothing. |
| Occasional (sheets, modals, toasts, onboarding steps) | Standard motion. |
| Rare, first-time moments | Delight is allowed. |

When unsure, the strongest move is to **delete the animation**. Data the
user is reading never moves for style.

## 2. Name the purpose in one word

Feedback, spatial continuity, state change, preventing a jarring cut,
explanation, delight. Can't name it? Don't build it.

## 3. Pick the cheapest tool that works

| Need | Tool |
|---|---|
| Press, toggle, color, a value flipping — no gesture | Reanimated CSS transition (`transitionProperty` in the style) |
| Loop, multi-stage, or plays on mount with no state change | Reanimated CSS animation (`animationName` keyframes) |
| Mounting/unmounting element, list reflow | Layout animations (`entering` / `exiting`), never on recycled list rows |
| Anything a finger touches, or derived from scroll | `useSharedValue` + `Gesture` + `useAnimatedStyle` |
| Screen to screen | Native stack options in Expo Router. Never hand-roll this. |
| UI tracking the keyboard | `react-native-keyboard-controller` (preinstalled — never a listener plus a guessed duration) |

A press scale is a CSS transition; a drag is a shared value. Using a worklet
for a two-state toggle is the mobile equivalent of installing a motion
library for a fade. Reanimated, not core `Animated`, for anything a finger
touches.

## 4. Properties: transform and opacity, nothing else

Everything else re-runs Yoga every frame for the node *and its siblings*.
The one exception is an absolutely positioned element with no children (a
tab pill, a progress fill) — out of flow, so nothing else re-lays out.

- Enter from `scale(0.95)` + fade, never `scale(0)`. Menus grow from their
  trigger. Exits are faster than entrances and leave the way they came in.
- `transform` is an array and order matters — keep translate first unless
  the multiplication is wanted.
- Never animate Android `elevation` (re-renders the shadow every frame) —
  crossfade a pre-shadowed layer instead.
- **Never animate a header's height** — translate inside a fixed clip.

## 5. Spring if a finger was involved, timing otherwise

Springs carry velocity through interruptions; timing curves restart. The
designer form (duration is perceptual):

```ts
const SNAP = { duration: 400, dampingRatio: 1 } // default settle, no overshoot
const PLAYFUL = { duration: 300, dampingRatio: 0.8 } // one soft overshoot, celebrations only
const SHEET = { duration: 300, dampingRatio: 0.8, velocity } // seeded with the gesture's velocity
```

Bounce only when the gesture carried momentum. Add `overshootClamping: true`
where passing a hard edge is forbidden. If a finger was involved: capture
the live value on grab, hand release velocity into the spring, rubber-band
past boundaries, stay grabbable mid-flight.

Timing, for everything without a finger on it — strong curves only,
built-ins are too weak:

```ts
import { Easing } from 'react-native-reanimated';
const EASE_OUT = Easing.bezier(0.23, 1, 0.32, 1); // entering, exiting, default
const EASE_IN_OUT = Easing.bezier(0.77, 0, 0.175, 1); // on-screen movement
```

Under 300 ms. **Never ease-in on an entrance.** Press feedback lands on
press-**in**, 100–150 ms: scale 0.97 on buttons and cards, a background
highlight (never scale) on list rows, opacity on bar buttons.

## 6. The thread law

Gesture → animation never hops the JS thread. One render per frame is the
top cause of RN jank:

- Shared value → `useAnimatedStyle`; React never re-renders. Never
  `setState` from a gesture or scroll handler.
- `scheduleOnRN` (Reanimated 4's `runOnJS`) only at gesture end — never per
  frame in `onUpdate`; use `useAnimatedReaction` at a threshold instead.
- Never read a shared value during render (a snapshot that silently
  desyncs), never write one either. Touch them in worklets, handlers, and
  effects only — `.get()` / `.set()`, the compiler-safe form.
- Functions called from a worklet need `'worklet'` as the first line, or
  they throw on device while passing in the debugger.
- Worklets and shared values (`react-native-worklets` 0.7.4,
  `react-native-reanimated` 4.2.1) are installed. `babel-preset-expo`
  configures the worklets Babel plugin automatically — touch
  `babel.config.js` only in a bare project without that preset.

## 7. Press, not hover — plus haptics

Every hover affordance is redesigned, not ported: feedback on press-in,
commit on press-out. 44×44pt minimum touch targets (`hitSlop` grows the
target, never the visual). Android ripple only in a Material-styled app —
same scale on both platforms otherwise.

Haptics (`expo-haptics`, preinstalled) are punctuation, never the only
feedback: selection tick as a value passes a step, light impact as
something snaps home, success/error for outcomes — same frame as the
visual, one per user action, never on scroll, never in loops. From a
worklet: `scheduleOnRN(Haptics.selectionAsync)`.

## 8. Reduce Motion

Spatial motion collapses to cross-fades; native transitions stay the
system's. Reduced motion means fewer and gentler, never zero — keep the
opacity and color changes that explain a state change:

```tsx
import { useReducedMotion, ReduceMotion } from 'react-native-reanimated';
withSpring(0, { duration: 300, dampingRatio: 0.8, reduceMotion: ReduceMotion.System });
```

Never animate to a hardcoded height under text scaling — measure with
`onLayout`, or animate a transform instead.

## 9. Setup that silently breaks motion

When the animation just doesn't run, check in order: versions match the SDK
(`npx expo install`, never plain npm for these packages);
`GestureHandlerRootView` wraps the app or gestures do nothing with no
error; Reanimated 4 requires the New Architecture; judge feel on a release
build on the slowest supported device — dev builds and Expo Go hide
exactly the jank being hunted.

## 10. The bar

60 fps through the hero flow, measured on a **release build**. Watch once
for feel, once frame by frame, and again next day with fresh eyes. On
Talon, the automated evidence is the verify ladder: `run_boot` for the
cold start, the screenshot for stills, `run_interact` for the flow.
Profile, do not guess.
