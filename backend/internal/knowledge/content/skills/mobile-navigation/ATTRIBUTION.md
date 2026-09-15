# Attribution — mobile-navigation

Adapted from Appllama's **appllama-app-design-skill** v1.3.0 "Navigation
laws" (MIT).

- **Source:** https://github.com/appllama/appllama-skills — `skills/appllama-app-design-skill/SKILL.md`
- **License:** MIT (Appllama / Antmind Ventures)
- **Vendored:** 2026-09-15, v1.3.0

## What changed from the original

- **iOS-only presentation names.** The original names iOS presentation styles
  and `formSheet` detents. Talon is Android-only; the same semantics map to
  expo-router's `presentation` options with the device's bottom-sheet
  behaviour. The *judgment* — which presentation means which thing — is
  unchanged.
- **`Stack.Protected` / `usePreventRemove`.** Kept as written; both are
  expo-router APIs that work on this stack.
- **New: the wallet door.** The original's one-way-door examples (sign-in,
  purchase) would be misapplied to MWA wallet connection, which is
  revocable session state rather than an irreversible transition. A
  wallet-specific note keeps connecting and disconnecting on the screen that
  asked.
- **Dropped.** iOS edge-swipe specifics; the Android hardware back button is
  this device's equivalent and is named in the laws where back behaviour
  matters.
