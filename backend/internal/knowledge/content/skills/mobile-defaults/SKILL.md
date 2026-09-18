---
name: mobile-defaults
description: Mobile-first defaults every Talon app should have — safe areas, keyboard handling, network loss, slow CPU, low-data mode, status bar. Load when the build is producing a screen and "happy path only" is the default, because the user will hit every other path on a real phone.
version: "RN 0.83, react-native-safe-area-context 5.6.x, expo-status-bar 3.x, NetInfo 7.x"
autoAttach: true
triggers: [path:app/**, path:components/**, content:NetInfo, content:useSafeAreaInsets, content:StatusBar, content:AppState, content:keyboard-controller, content:AbortController]
---

# Mobile defaults — the paths you only find on a real phone

Every screen in a Talon app has to survive these by default. None
of them are on the happy path; all of them happen on a real device
the first week.

## Safe areas

- Safe-area geometry lives in `mobile-craft` — follow it. This skill covers
  only what craft doesn't: a modal sheet (e.g. `@expo/ui` `BottomSheet`)
  needs its own safe-area handling on the bottom edge.

## Keyboard

- Keyboard tracking goes through `react-native-keyboard-controller`
  (animated values that follow the real keyboard) — never
  `KeyboardAvoidingView` with guessed durations, never a keyboard listener
  plus a hardcoded animation. See `mobile-motion` for the pattern.
- `keyboardShouldPersistTaps="handled"` on scrollable lists that
  contain `TextInput`, so a tap outside the keyboard does not
  dismiss-then-reopen the input.
- `returnKeyType="done"` on the last input of a form, and wire
  it to submit.

## Status bar

- `StatusBar` from `expo-status-bar` (preinstalled) is enough
  for colour and translucency. `style="light"` for dark surfaces,
  `style="dark"` for light ones.
- Under SDK 54+ edge-to-edge enforcement the OS owns the status-bar
  background — `androidStatusBar.backgroundColor` in `app.json` is ignored.
  Control style only, never background.

## Network loss

- A real mobile user **will** lose the network. The screen must
  show a "no connection" state, not a spinner that never
  resolves.
- Use `@react-native-community/netinfo` (verify install) for
  reachability. Subscribe at the screen root, render a banner
  when offline.
- Every fetch needs a timeout. `AbortController` with a
  10-second timeout is the default; raise to 30 for RPC calls.
- Retry with exponential backoff (1s, 2s, 4s) on transient
  errors, max 3 attempts, then surface a retry button. Don't
  retry on 4xx.

## Slow CPU

- Initial render must not block on a heavy computation. Move
  parsing, sorting, and crypto off the first paint.
- Defer non-critical work until the transition settles (interaction-aware
  scheduling, not a fixed timeout) for work that can wait until animations
  finish.
- `useMemo` for derived data the screen re-computes on every
  render (lists, sort, search).

## Low-data mode

- A user on cellular has a real cost per MB. Default to
  thumbnail-quality images, lazy-load the full image on
  press.
- `expo-image` does this for you if you set
  `priority: "low"` on the placeholder and `priority: "high"`
  on the full image.

## Backgrounding

- `AppState` from `react-native` tells you when the app
  backgrounds. Pause animations, pause polling, persist
  unsaved input.
- On a wallet app: the user can switch to the wallet app to
  approve a sign request, then back to yours. Don't
  re-authorize on every foreground; reuse the `auth_token`.

## Rotation and orientation

- Lock orientation in `app.json` (`"orientation": "portrait"`)
  unless you have a real reason to support landscape. Most
  Solana apps are portrait-only.
- If you do support landscape, test **all** screens, including
  modals. The keyboard, the safe area, the bottom sheet all
  change shape.

## Permissions

- Ask for a permission at the moment the user takes the action
  that needs it, not on launch. (Camera on "scan", location on
  "find nearby", notifications on "enable alerts".)
- If the user denies, show a clear next step — "open settings"
  with a `Linking.openSettings()` call.
