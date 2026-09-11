---
name: rn-best-practices
description: React Native performance and UI best practices — list virtualization, memoized rows, GPU-only animations, expo-image, native navigators, Pressable, safe areas, correct conditional rendering. Load when building lists, animating, or optimizing screens.
version: "React Native 0.83 / Expo SDK 55 — condensed from Vercel Labs' RN rules"
autoAttach: true
triggers: [content:FlatList, import:@shopify/flash-list, pkg:@shopify/flash-list, import:@legendapp/list, import:expo-image]
license: MIT (Vercel Labs — see ATTRIBUTION.md)
source: https://github.com/vercel-labs/agent-skills (skills/react-native-skills)
---

# React Native best practices

A checklist of high-impact RN/Expo practices, ordered by how much they matter on a
120Hz device. This condenses Vercel Labs' React Native rules; where their advice and
this project's `talon-screen-design` skill differ, the reconciled Talon guidance is
below. For the full per-rule examples, see `ATTRIBUTION.md` (upstream repo).

## List performance (CRITICAL)

- **Never render a list as `ScrollView` + `.map()`.** It mounts every child upfront.
  Use a virtualized list, which mounts only what's visible.
- **The ladder:** `FlatList` (preinstalled, already virtualized) is the baseline and
  the right default for most Talon screens. For **large, unbounded, or heavy** lists
  (infinite feeds, big catalogs), go one step further and install a recycling
  virtualizer — **FlashList** (`@shopify/flash-list`) or **LegendList**
  (`@legendapp/list`) — which recycles item views for lower memory and faster scroll.
- **Memoize rows.** Wrap the item component in `React.memo` and pass it **primitives**,
  not freshly-built objects, so memoization holds.
- **Stabilize references.** Hoist `renderItem`, `keyExtractor`, and callbacks out of
  render (or `useCallback`); don't pass inline object/style literals into `renderItem`.
- **Keep items light.** Move expensive work (date formatting, derived values) out of
  the row and precompute it. Use item types for heterogeneous lists.
- Use compressed/appropriately-sized images in rows (see expo-image below).

## Animation (HIGH)

- **Animate only `transform` and `opacity`** — they run on the compositor. Animating
  layout props (`width`, `height`, `top`, `margin`) forces layout on every frame and
  drops frames.
- Prefer `useDerivedValue` for values computed from other shared values.
- Drive press animations with `Gesture.Tap` / `GestureDetector` rather than
  `Pressable` state when the press itself is animated.
- This project uses Reanimated v4 — see the **`reanimated-v4`** skill for the APIs.

## Navigation (HIGH)

- **Use native navigators.** expo-router's default `Stack` is already native-stack
  (`UINavigationController` / Fragment) — don't add `@react-navigation/stack` on top.
  Native gives you platform transitions, large titles, and correct safe areas for free.
- Prefer native header options (`title`, `headerSearchBarOptions`) over custom header
  components. See the **`expo-router-nav`** skill for this project's routing.

## UI patterns (HIGH)

- **`expo-image` for all images** (preinstalled): memory-efficient caching, blurhash
  placeholders, progressive loading. Use `placeholder`, `contentFit`, `transition`,
  `cachePolicy`, and `recyclingKey` (for list rows) — don't use RN's built-in `Image`.
- **`Pressable` over `TouchableOpacity`** — more control, better feedback states.
- Prefer **native modals, menus, and sheets** where possible — a native context menu
  (e.g. Zeego) or an `@expo/ui` `BottomSheet` beats a hand-rolled overlay (see the
  **`expo-ui`** skill).
- Handle safe areas in scroll views; use `contentInset`/`contentInsetAdjustmentBehavior`
  for content under headers. Use `onLayout` to measure, not the imperative `measure()`.
- Style with `StyleSheet.create` (or Nativewind if installed) — never rebuild inline
  style objects every render. This matches `talon-screen-design`.

## State & rendering (MEDIUM)

- **Wrap every string in `<Text>`.** A bare string under a `<View>` crashes RN.
- **Don't use falsy `&&` for conditional rendering.** `{count && <Badge/>}` renders a
  stray `0` when `count` is 0 — use `{count > 0 && <Badge/>}` or a ternary.
- Minimize what each component subscribes to; pass a stable dispatcher for callbacks;
  show a fallback on first render rather than blocking.
- If React Compiler is enabled, destructure functions from props/hooks so it can
  memoize them, and read Reanimated shared values through the compiler-safe pattern.

## Configuration (LOW)

- Install custom fonts via an Expo config plugin, not runtime loading.
- Keep native dependencies in the app package (monorepos) and on a single version.
- Hoist `Intl.*` formatter creation out of render — constructing them is expensive.

---

*Condensed from Vercel Labs' `react-native-skills` (MIT). This is a checklist; the
upstream repo has a full incorrect-vs-correct example per rule — see `ATTRIBUTION.md`.*
