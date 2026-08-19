---
name: core
description: Non-negotiable platform invariants for every Seeker build. Always in effect.
alwaysApply: true
---

These rules hold on every turn. They are constraints, not suggestions — the harness
enforces the security-critical ones and will reject a tool call that breaks them.

## Wallet & keys

- The only wallet stack is Mobile Wallet Adapter (MWA). Wallet work goes through
  `transact()` from `@solana-mobile/mobile-wallet-adapter-protocol-web3js`.
- Never install or import browser wallet packages — no `@solana/wallet-adapter-*`,
  no `wallet-standard`, no Phantom/Solflare SDKs. They cannot run on this device.
- Never ask for, store, generate, or log a private key or seed phrase. Signing
  happens inside the wallet app; the app never sees key material.
- When you build wallet, signing, or transaction code, consult the `wallet-mwa` skill.

## Platform

- React Native only. Use `View`, `Text`, `Pressable`, `ScrollView`, `FlatList`,
  `TextInput`, `Image`, `Switch`, `Modal`, `SafeAreaView`. There is no DOM: no
  `div`/`span`/`button`, no `className`, no CSS files, no `document`/`window`.
  Style with `StyleSheet.create`.
- Navigation is expo-router (file-based, under `app/`). Do not add react-navigation
  directly — expo-router already sits on top of it.
- `index.js` (`import 'expo-router/entry'`) is the native entry point and must
  always exist. Never delete it.
- TypeScript throughout. Props get an interface. `any` is a bug — use `unknown`
  and narrow.

## Scope

- Build what was asked and what that change requires — nothing else. Do not add
  screens, features, or refactors the user did not request.
- Do not touch `package.json`, `app.json`, `tsconfig.json`, or `babel.config.js`
  unless the task actually requires it.

## Packages

- A set of packages ships preinstalled with every project (listed below). Never
  reinstall them, and never list them as new dependencies. The list below is
  generated from the real project template and supersedes any package list that
  appears elsewhere in these instructions.
- Reach for `install_package` only for something the feature genuinely needs and
  that is not already present.
