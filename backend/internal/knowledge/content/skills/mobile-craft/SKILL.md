---
name: mobile-craft
description: The design bar for every screen Talon ships — anti-slop laws, one accent / one grey / one shape scale, native fidelity, typography, safe areas, and the state architecture behind screens that feel great. Load when building or polishing ANY screen, deciding colour, type, spacing, iconography, or visual hierarchy.
version: "appllama-app-design-skill@1.3.0 — adapted; Appllama MIT"
autoAttach: true
triggers: [path:app/**, path:components/**, path:features/**, path:constants/**, content:StyleSheet.create, content:tabular-nums, content:SafeAreaView, content:Pressable, content:@expo/vector-icons]
license: MIT (Appllama — see ATTRIBUTION.md)
source: https://github.com/appllama/appllama-skills (skills/appllama-app-design-skill/SKILL.md)
---

# Mobile craft — the design bar

Every Talon screen sits on a phone next to the best-designed apps in the world,
and the user compares within seconds. This skill is the bar and the method for
clearing it. Talon-specific overrides win where they conflict (the device is a
2670×1200 AMOLED Android at 460 PPI — see the Device appendix below for geometry).

## Prime directive: study before you draw

Never design a screen from imagination when you can study how top apps solved
the same one. Real shipping apps encode thousands of hours of iteration.
Before writing UI:

1. Study how the category's winners build this screen — layout skeleton,
   hierarchy, control choices, spacing rhythm, where the primary CTA sits,
   what gets an illustration vs plain text, how progress is communicated.
2. Extract the **pattern, not the pixels**. Same proven skeleton, this
   product's voice. Copying 1:1 is lazy and legally risky; ignoring every
   convention users already know is worse.
3. Then design the screen.

## The platform's real runtime

- Expo + Expo Router, React Native, TypeScript. `process.env.EXPO_OS` over
  `Platform.OS` for compile-time checks.
- `@expo/vector-icons` is preinstalled and is the **only icon source** — see
  the Icon law below.
- `react-native-reanimated` for motion, `react-native-gesture-handler` for
  gestures, `expo-image` for images, `react-native-safe-area-context` for
  insets. Never hard-code notch numbers.

## Native fidelity laws

Violating any is a finding, not a style preference.

1. **Semantic tokens, both themes, day one.** Every screen resolves colour
   through `@/constants/theme` — never an inline hex, never a sixth accent
   appearing in screen seven. Light and dark both render correctly before a
   screen is "done". Never pass semantic colour objects into Reanimated
   animated styles — resolve to strings first.
2. **Native controls over rebuilt ones.** Switch, Slider, pickers, menus:
   use the native control (`@expo/ui`) or a faithful wrapper. A rebuilt
   toggle that animates 50 ms differently than the platform's reads fake.
3. **Icons are vector icons.** `@expo/vector-icons` is preinstalled and is
   the ONLY icon source in a Talon app — never an emoji-as-icon, never a
   hand-drawn SVG of something the set already has, never a second icon
   family imported alongside it. Pick ONE glyph family per app
   (`MaterialIcons`, `Ionicons`, `Feather`, …) and stay in it — mixing
   three families on one screen is a defect. Icons inherit the text colour
   by default; size them from the type scale (16/20/24/28), and give every
   icon a label when it is the sole affordance.
4. **Typography is hierarchy.** One display size per screen, a fixed type
   ladder below it, weights from {400, 600, 700}. Tabular numerals
   (`fontVariant: ['tabular-nums']`) for anything that counts, times, or
   prices — money that reflows width as digits change reads as broken.
   `selectable` on data users may copy.
5. **Continuous corners.** `borderCurve: 'continuous'` on every rounded
   rectangle — the cheapest "feels native" win that exists.
6. **Shadows via `boxShadow`**, not the legacy `shadow*`/`elevation` props.
   Shadows express elevation logic, never decoration; one elevation system
   per app.
7. **Spacing rhythm.** One base unit (4), never left. Prefer `gap` over
   margin stacking. ScrollView padding goes in `contentContainerStyle`,
   never on the ScrollView itself.
8. **Safe areas are part of the design.** Verify with content scrolled under
   the status bar, that the bottom CTA clears the home-indicator gesture
   area, and that nothing is clipped at either end of this tall screen.
9. **Navigation titles belong to the navigator.** Use the stack's native
   title rather than a hand-rolled header.
10. **Haptics are punctuation** (`expo-haptics`, preinstalled). Selection
    tick when a value passes a step, light impact when something snaps
    home, success/error for outcomes — on the same frame as the visual, one
    per user action, never the only feedback, never on scroll, never in
    loops.
11. **Format numbers like a product, not a database**: 1.4M, 38k, $4.99.
    Trim trailing zeros. `toLocaleString()` for money.

## Anti-slop laws — default bans

AI-built apps share a look, and users file it under "template" within seconds.
Each is a default ban; an override needs a brand reason you can articulate.

1. **No AI-default styling.** Purple/indigo gradient CTAs with a glow,
   glassmorphism on every card, mesh-gradient heroes, confetti for minor
   events, sparkles in headings — that is the model's house style, not
   design. Palette and materials come from the reference screens you
   studied, never from unprompted priors.
2. **One accent, locked.** The template's accent is THE accent on every
   screen. Neutrals carry the app; the accent is spent where the money is —
   the primary action, the active state, progress.
3. **One grey family.** Warm greys or cool greys — never both in one app.
4. **Shape lock.** One corner-radius scale, stated as a rule and never
   violated. Mixed radii without a stated rule read as assembled-from-parts.
5. **No emoji as iconography.** Icons are vector icons (fidelity law 3).
   Emoji appear only where the product's voice is genuinely playful, in
   content, never in chrome.
6. **One label per intent.** "Get started", "Start now", and "Begin" are the
   same intent — pick one phrasing and use it everywhere.
7. **Emphasis stays in the family.** Emphasize with weight or italic of the
   same typeface; injecting a serif into a sans headline is amateur.
8. **Ship full state cycles, not the happy path.** Static-successful-state-
   only is the default failure mode: skeletons match the final layout's
   shape, empty states are composed and say how to fill them, errors are
   inline and specific with a next step. No async action without a visible
   pending state; no failure without a way out.
9. **The slop pre-flight is mechanical.** Before a screen is done, count:
   distinct accent hues (1), distinct corner radii (all from the stated
   scale), icon families (1), emoji in chrome (0), gradients without a
   brand reason (0), duplicate labels for one intent (0). A failed count
   is a fix, not a judgment call.

## State architecture

Screens that feel great are screens whose state is boring:

- **Server state** in TanStack Query (preinstalled): caching, retries,
  optimistic updates. Never `useEffect` + `fetch`.
- **Client state** in a small atomic store. Broad "app state" contexts cause
  the re-render cascades that make UIs feel heavy.
- **Ephemeral UI state** (open/closed, focus, scroll) stays local.
- **Optimistic by default**: taps reflect instantly, reconcile in the
  background, roll back loudly on failure.
- Uncontrolled `TextInput`s for high-frequency typing; controlled inputs are
  a top-3 cause of typing jank.

## Perceived performance

- Skeletons only for content whose shape you know; otherwise progressive
  reveal. Never a full-screen spinner for a partial update.
- `FlatList` for list-shaped content, stable keys, memoized rows.
- Preload the next screen's data on press-in, not on navigation-complete.
- `expo-image` with `recyclingKey` in lists, placeholder blurhash.

## Icons — the one rule

**Every icon in a Talon app comes from `@expo/vector-icons`.** It is
preinstalled; there is nothing to install and no permission to ask for.

```tsx
import { MaterialIcons } from '@expo/vector-icons'
<MaterialIcons name="verified" size={20} color={t.success} />
```

One glyph family for the whole app, chosen once and named in the design
tokens. Do not add `react-native-vector-icons`, `lucide-react-native`, or any
second icon package — they are duplicates of an installed dependency. If the
glyph the screen needs is missing from the chosen family, switch family for
that icon only via the same package, never the package itself. Emoji and
Unicode symbols are never icons.

## Device appendix — the Talon panel

All craft laws above hold; this section adds what the hardware demands.
(Merged from `talon-screen-design`: geometry and reach are device facts,
everything else it taught was restated here.)
- **One-handed tall panel.** 6.36" AMOLED, 2670×1200, 460 PPI, 120 Hz.
  Primary actions live in the **lower two-thirds** — anything critical at
  the top edge is unreachable one-handed. Design for the panel, not a
  tablet and not a browser.
- **Touch targets at least 44×44pt**, body text no smaller than 14–16pt,
  on a 460 PPI screen with rendering at full sharpness.
- **Dark-first, not dark-only.** AMOLED rewards true dark surfaces with
  real contrast — default to dark, spend the accent where the fidelity
  law says. The both-themes law above still holds: light must render
  without breakage, it just isn't the lead.
- **StyleSheet.create, not inline object literals** — inline styles defeat
  the token discipline and re-allocate every render.

## Emotion and flow — designed, not decorated

A screen is remembered by two moments: its **peak** (most intense) and its
**end** (last impression). Name both before building — a wallet approval
that *feels* safe converts; one that feels sketchy doesn't, whatever the
copy says. Motion is a trust signal in high-stakes flows (signing, money,
health): one deliberate confirmation animation beats three decorative ones
(see `mobile-motion` frequency gate).

- **Thumb zone.** Primary actions live in the bottom third — reachable
  one-handed on the tall panel (see Device appendix). Anything critical at
  the top edge might as well not exist.
- **Selection over input.** Prefer tappable options with an Other/free-text
  fallback over blank fields, especially one-time setup. Every keystroke on
  glass is friction; reserve text fields for repeated, precise entry.
- **Never-blank states.** Loading, empty, error, and success are all
  composed screens, not spinners: recent items, popular choices, or guidance
  plus a CTA. Waits and failures get uplifting, specific microcopy ("Still
  syncing — your balance is safe") never a dead spinner or a bare error code.
- **Type discipline.** One family for UI, one mono for numerals — max four
  sizes, two weights. Hierarchy comes from size, weight, and opacity steps
  (100/80/60), never from bolding everything.

## Invisible details — the compound interest

Most of what makes software feel great is never consciously noticed. When a
control behaves exactly as assumed, attention stays on the task — that is
the goal. Each rule below is small; together they are the difference between
built and crafted. (Distilled from Emil Kowalski's design-engineering notes;
motion specifics live in `mobile-motion`.)

- **Presses answer on press-in.** Scale 0.97 over 100–160 ms on any
  pressable, commit visuals on release. Waiting for the tap to complete
  before showing anything reads as dead.
- **Nothing appears from nothing.** Entrances start at `scale(0.95)` plus
  fade, never `scale(0)`. Popovers and menus grow from their trigger
  (`transform-origin` at the trigger); modals stay centered.
- **Bridge ugly crossfades with blur.** Two states overlapping read as two
  objects swapping; a 2 px blur during the transition blends them into one
  perceived change. Keep blur small — heavy blur is expensive on device.
- **Stagger entrances 30–80 ms apart**, never all at once, never blocking
  interaction while the cascade plays.
- **Remedial order when motion feels off:** delete it first; then reduce
  (shorter, smaller, fewer properties); then fix the easing; then the
  origin; then make it interruptible. Never start by adding more motion.
