---
name: seeker-screen-design
description: Design screens that fit the Seeker device and feel finished. Load when building UI, laying out a screen, or deciding on visual and interaction details.
version: "react-native@0.83, react-native-safe-area-context@5.6.x"
autoAttach: false
triggers: []
---

# Designing for the Seeker

The Seeker is a tall, narrow, very sharp phone held in one hand: a 6.36" AMOLED,
2670×1200, 460 PPI, 120Hz screen running Android. Design for that, not for a tablet
or a browser.

## Layout

- Respect the safe area at **both** ends — the status bar and the gesture area.
  Wrap screens in `SafeAreaView` (from `react-native-safe-area-context`, installed)
  or apply `useSafeAreaInsets()` padding. Do not hardcode top/bottom padding.
- One-handed reach: put primary actions in the lower two-thirds. A button pinned to
  the very top is hard to reach on a screen this tall.
- Vertical rhythm over horizontal density. The screen is narrow — prefer stacked
  cards and full-width rows to multi-column grids.

## Touch & legibility

- Touch targets at least **44×44pt**. Space tappable rows so neighbours are not hit
  by accident.
- Body text no smaller than 14–16pt. At 460 PPI small text is crisp but still small.
- Default to a **dark** surface — it is an AMOLED panel and most Solana apps are dark.
  Use real contrast for text; avoid mid-grey on dark.

## Every interaction has all its states

- No async action without a visible **pending** state (spinner, disabled button,
  skeleton). A tap that does nothing visible reads as broken.
- No failure without a **next step** — a retry, a message, a way out. Never a dead end.
- No empty list without an **empty state** that says what would fill it.
- Wallet actions: show what is being signed before the sheet opens, and handle the
  user cancelling as a normal outcome (see the `wallet-mwa` skill).

## Rules

- Style with `StyleSheet.create`, not inline object literals rebuilt every render.
- Use `FlatList` for anything scrollable and list-shaped — not `ScrollView` with a
  `.map()`. It matters on a 120Hz screen.
- Test the layout at the top and bottom of the screen, not just the middle.
