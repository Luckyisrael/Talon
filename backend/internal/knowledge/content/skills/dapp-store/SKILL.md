---
name: dapp-store
description: Submission constraints for the Solana Mobile dApp Store — assets, manifest, orientation, real-SOL cost. Load when the build is preparing a release artifact or when the user mentions publishing, submitting, or shipping the app.
version: "dApp Store 2025+; verify the current publisher docs before finalising"
autoAttach: false
triggers: [path:app.json, content:icon, content:banner, content:slug, content:dapp, content:store, content:publish, content:submit]
---

# Solana Mobile dApp Store — what gets submissions rejected

The dApp Store is the **only** first-party distribution channel for
the Talon. Browser-side, this is a real submission with real SOL
cost, real asset rules, and a real review queue. Get the assets
right the first time.

## The submission shape

- **App name**: human-readable, ≤ 30 characters. Not the package id.
- **Slug**: lowercase, dash-separated, ≤ 32 characters. Becomes the
  store URL.
- **Icon**: 512×512 PNG, alpha allowed. **No rounded-corner mask**
  (the store applies its own).
- **Banner**: 1200×600 PNG or JPEG. One image, no animation.
- **Screenshots**: at least **3**, **1080×1080 or larger**, all the
  same orientation. Portrait is the default; landscape is fine if
  the app locks to landscape.
- **Category** (single select): Games, DeFi, Social, Productivity,
  NFTs, Other. Pick the closest.
- **Description**: ≤ 4000 characters. Plain text. No HTML, no
  Markdown.
- **Changelog**: free-form; write one per release, not per build.
- **Build artifacts**: at least one signed APK; an AAB is optional
  but recommended for staged rollouts.

## Asset rules that fail submissions

- Icon is a square with rounded corners baked in (store rejects
  because its own mask will be redundant).
- Banner has a logo and a slogan at the same time (too busy — store
  rejects, "promote a single message").
- Screenshots mix orientations (3 portrait + 1 landscape — store
  rejects, "all screenshots must match orientation").
- Screenshots show debug text, `console.log` output, a mock-wallet
  address, or a fake balance. **Take fresh screenshots from the
  real, deployed APK on a real device** (or the redroid container
  with a clean state). The mock wallet address is `1111…`; if your
  screenshot shows that, the submission will be flagged.
- Bundle id (`applicationId` in `android/app/build.gradle`) does not
  match the store slug.
- `targetSdk` is below 34. Set to 35 for 2025+.

## Build artifact rules

- Signed APK (`assembleRelease`) with a **release** keystore (not
  the debug keystore). The keystore stays in your repo or in CI
  secrets; never commit a `*.jks` or `*.keystore` file.
- The `versionCode` (integer) **monotonically increases** across
  submissions. Use `expo-version` or bump it manually in
  `app.json`.
- The `version` (semver) is a string. `1.0.0` for the first public
  release.
- Permissions in `AndroidManifest.xml` are **scoped** — only request
  what you use. `INTERNET` is fine, `WRITE_EXTERNAL_STORAGE` is not.

## The real SOL cost

Submitting the app to the dApp Store costs SOL. The exact amount
moves; check the publisher docs. Surface this to the user in the
Export panel as a clear, line-item step — not a hidden charge.

## What the app store review looks for

- The app launches and the first screen renders within 5 seconds on
  a real device.
- The wallet flow works end-to-end. The user can connect, sign, and
  see the result on-chain.
- No "coming soon" screens, no placeholder images, no debug
  banners.
- The app does not crash on rotation, on background, or on
  network loss.
- Privacy: the app does not request permissions it does not use.

## When in doubt

- Take screenshots from a clean build. Wipe `AsyncStorage`, clear
  the wallet session, restart the app, take the screenshot.
- Re-read the publisher docs the day before you submit. The schema
  and the asset sizes change.
- Ship to a test channel first. The dApp Store supports staged
  rollouts; use them.
