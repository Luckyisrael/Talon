# AGENTS.md — Project conventions

This file teaches the build agent this project's conventions. Edit it freely:
the agent re-reads it before each round of work, so a saved change takes
effect on the next turn. Nothing here overrides the platform's core rules —
those are enforced by the harness.

## Architecture

- Monorepo layout: Expo Router app under `app/`, shared UI in `components/`,
  feature modules under `features/`, constants and helpers where their names
  say they belong.
- Navigation is file-based through `app/` — a new screen is a new file under
  `app/`, never a new navigation library.
- `index.js` is the native entry point; it must always exist.

## Wallet & keys

- The only wallet stack is Mobile Wallet Adapter (MWA) via
  `@solana-mobile/mobile-wallet-adapter-protocol-web3js`.
- Never add browser wallet packages (`@solana/wallet-adapter-*`,
  `wallet-standard`, Phantom/Solflare SDKs) — they cannot run on-device.
- Key material stays inside the wallet app. This app never sees, stores, or
  logs a private key or seed phrase.

## Style

- TypeScript throughout; props get an interface. `any` is a bug — use
  `unknown` and narrow.
- React Native primitives only (`View`, `Text`, `Pressable`, …). There is no
  DOM here.
- `StyleSheet.create` for styling; no CSS files, no `className`.

## Design language — the system this app ships in

The design language is not invented per screen. It lives in `constants/theme`
(the tokens) and `constants/app-styles` (the styles built from them), and
every screen resolves through it. The `mobile-craft` skill carries the full
bar; this is the short version:

- **Colours** come from `useTheme()` — never an inline hex, never a fifth
  accent invented for one screen. The accent is the template's accent, spent
  on the primary action per screen and the active state. Neutrals carry the
  rest. Light and dark both render correctly before a screen is done.
- **Icons** come from `@expo/vector-icons` only — it is preinstalled, there is
  nothing to install. One glyph family for the whole app. Never an emoji as
  an icon, never a second icon package (`react-native-vector-icons`,
  `lucide-react-native`, …) — they duplicate an installed dependency.
- **Shape lock**: one corner-radius scale (`constants/theme.ts`), never a
  radius outside it.
- **Typography**: one display size per screen, the fixed ladder below it,
  weights from {400, 600, 700}. Tabular numerals
  (`fontVariant: ['tabular-nums']`) for anything that counts or prices.
- **Buttons** are `<ActionButton />`, not the platform `<Button>` — the
  platform control's colour, radius, and hit area are not ours.
- **Every interaction ships its full state cycle**: pending state on every
  async action, a next step on every failure, an empty state on every empty
  list.
- **Slop is countable**: before a screen is done, accent hues are 1, icon
  families are 1, emoji in chrome is 0, gradients without a brand reason
  are 0.

## Motion

The frequency gate comes first: tab switches, keyboard, scroll, and back
stay native and animate nothing. When unsure, delete the animation. Springs
only where a finger was involved; timing under 300 ms with a strong ease-out
otherwise. Never ease-in on an entrance.

## Navigation

Push when the user will want to return; `replace` when coming back would land
in a state the world has moved past. A sheet that grows a second step was a
modal all along. Wallet connection is revocable session state — it returns to
the screen that asked, it is never a one-way door.

## Packages

- Check `package.json` before installing: the template ships many packages
  already, and reinstalling one is an error, not a preference.
- `@expo/vector-icons` is preinstalled and is the only icon source in a Talon
  app. Do not add an icon package.
- The design tokens (`constants/theme.ts`) and styles
  (`constants/app-styles.ts`) are the system; adding a colour library or a
  second design system is not.

## When this file is wrong

If a convention here conflicts with how the code actually works, fix the
code or fix this file — never both silently. This file is the project's
memory of its own decisions.
