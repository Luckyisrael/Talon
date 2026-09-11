---
name: talon-mobile
description: Solana Mobile Talon platform facts that the model is expected to know — hardware, software, network, dApp Store rules, Seed Vault constraints, and the SCL. Load when the build touches anything Talon-specific.
version: "Talon (Talon 1, Gen 1), Android 14/15, Solana Mobile dApp Store 2025+, SCL 0.5.x"
autoAttach: true
triggers: [path:app.json, path:android/app/src/main/AndroidManifest.xml, import:expo-constants, pkg:@solana-mobile, pkg:@solana/spl, pkg:@coral-xyz]
---

# Talon — platform facts the model must not get wrong

The build target is the **Solana Mobile Talon** — a phone, not a browser, not a
desktop, not the prior Saga. Facts here are the ones training data gets wrong or
omits. Where a fact is version-pinned, the version line in the front matter is
the source of truth; if it disagrees with what you see in `app.json` /
`android/app/build.gradle`, the live project wins.

## Hardware (Talon 1 / Gen 1)

- **AMOLED**, 6.36", 2670×1200, **460 PPI**, **120 Hz**. Design for a sharp,
  tall, one-hand-held panel, not a tablet. (See `talon-screen-design`.)
- **Snapdragon**-class SoC. Mid-tier GPU. Avoid heavy WebGL on load; use
  `expo-image` for everything visual and `FlatList` / `FlashList` for lists.
- **8 GB RAM, 128 GB storage** baseline. Your APK should stay well under
  100 MB installed; native modules bloat fast.
- Battery and thermal: 4K video and unbounded scroll stress-test the phone.
  Test on a real device, not just the emulator.

## Software

- **Android 14** at launch, with **Android 15** available. `targetSdk=35` is
  expected for new APKs in 2025+; do not submit with `targetSdk<34`.
- **React Native 0.83** on **Expo SDK 55** is the pinned baseline for every
  build (see `core.md`). Expo 56 features are not yet broadly available here.
- The build runs inside a **Redroid** container (Android-in-Docker) during
  development. The pre-installed **Mock MWA wallet** signs anything; on real
  hardware, MWA goes through a real wallet and the **Seed Vault**.

## Seed Vault (TEE-backed key custody)

Seed Vault is a **Trusted Execution Environment** that holds the seed for
installed wallets. It is not your app's problem to use directly, but it shapes
two important behaviours:

1. **Keys never leave the device.** Your app never sees, stores, or asks for a
   private key or seed phrase. All signing goes through MWA `transact()`. If
   your code reaches for a key, it is wrong. (See `wallet-mwa`.)
2. **Wallet apps can be uninstalled without losing the seed.** When the user
   re-installs a wallet, Seed Vault hands the same key material back. Don't
   cache "is wallet installed" — re-check on every connect.

The MWA **mock wallet** on Redroid skips Seed Vault; the mock is for the
dev loop, not for production behaviour.

## Mobile Wallet Adapter (MWA) — quick map

- The connection: `useMobileWallet()` from `@wallet-ui/react-native-kit`
  (4.x), backed by `@solana-mobile/mobile-wallet-adapter-protocol-kit`.
  Already preinstalled. (See `wallet-mwa` for the connect/sign patterns.)
- The session: the hook owns authorize/reauthorize — connect once, the
  account persists. Re-check on every connect; never cache "wallet
  installed" as permanent state.
- The cluster: devnet during development; mainnet for production. The user
  picks, not the app — but you need a sensible default and a way to switch.

## Solana Common Library (SCL)

 - A set of React Native utilities and Solana primitives shipped by
  Solana Mobile: token tables, account derivation helpers, SSS (Solana
  Stablecoin Standard) helpers, mobile-flavoured kit extensions.
 - Check the installed `features/` and `@wallet-ui/*` packages in your
   `node_modules` **before writing your own balance/display helpers** — most
   of them are already there.
 - The SCL version line in the front matter pins the API. If the installed
   version is older, follow the installed types.

## Solana Mobile dApp Store (submission rules)

These are the ones that get submissions rejected. Verify against the current
publisher docs before you finalise a release.

- **Icon**: 512×512 PNG, alpha allowed, no rounded-corner mask (the store
  applies its own).
- **Banner**: 1200×600 PNG/JPEG. One image, no animation.
- **Screenshots**: at least **3**, **1080×1080 or larger**, all the same
  orientation (portrait, mostly — landscape is allowed but unusual).
- **Manifest**: a JSON or YAML with name, slug, icon URL, screenshots, and
  build artifact URLs. The exact schema moves — pin to the current SCL
  version.
- **Single-orientation recommended** unless the app has a real reason for
  landscape. Lock orientation in `app.json`.
- **Real SOL cost** to publish. The user pays, not you. Make this a clear
  step in the Export panel, not a hidden charge.

## App linking and deep links

- For verified deep links (so tapping a URL opens your app), ship
  `android/app/src/main/res/values/assetlinks.json` with your package
  signature SHA-256. (See `app-linking` skill.)
- Register your custom URL scheme in `app.json` under
  `scheme: ["myapp"]` so `expo-linking` knows about it.
- Don't try to handle every link type — handle one and document it.

## App icons and adaptive icons

- Provide **1024×1024** in `assets/icon.png`. Expo generates the rest.
- For **adaptive icons** (Android 8+), provide `assets/adaptive-icon.png`
  and a background color. A solid `void` (near-black) or `paper`
  (off-white) background is the safest default.

## What not to assume

- **No browser**, ever. No `window`, no `document`, no DOM. Style with
  `StyleSheet.create`, not CSS.
- **No Node-only APIs** in runtime code. `Buffer` is shimmed by RN, but
  reach for `TextEncoder` / `Uint8Array` first.
- **No synchronous fetches**. The mobile network is unreliable and slow.
  Every fetch needs a timeout, a retry, and a visible pending state.
- **No large bundles**. Metro will not warn you; the user will, with a
  4-second white screen.

## When this skill is wrong

The Talon evolves. If a fact here disagrees with your installed
`node_modules/@solana-mobile/*` types, with the live publisher docs, or
with `app.json` / `build.gradle` in the project, **the live project wins**.
Open an issue, but in the meantime, follow the live code.
