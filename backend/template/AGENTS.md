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

## Packages

- Check `package.json` before installing: the template ships many packages
  already, and reinstalling one is an error, not a preference.

## When this file is wrong

If a convention here conflicts with how the code actually works, fix the
code or fix this file — never both silently. This file is the project's
memory of its own decisions.
