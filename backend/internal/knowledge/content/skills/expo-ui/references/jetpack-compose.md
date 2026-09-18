# Jetpack Compose appendix (Android-only layer)

Demoted from standalone skill: same import root, one platform, not enough
unique surface to justify its own file. Read when the universal `@expo/ui`
layer lacks a component or you need native Android behavior.

- **Android only.** Anything importing `@expo/ui/jetpack-compose` crashes on
  iOS ("Unable to get view config"). Isolate in `.android.tsx` files or
  `Platform.OS` guards. Never put `.android.tsx` inside `app/` — Expo Router
  throws; place them in `components/` and import from a regular route.
- **`Host` comes from `@expo/ui`** (universal root), never from the
  jetpack-compose package. `<Host matchContents>` for intrinsic sizing,
  `<Host style={{ flex: 1 }}>` as a `LazyColumn` parent.
- Components/modifiers import from `@expo/ui/jetpack-compose` (and
  `.../modifiers`); mental model is Jetpack Compose + Material 3.
- **`LazyColumn` is not for large data** — items are JSX nodes on the JS
  thread. Big/unbounded lists stay on RN `FlatList` (see `rn-best-practices`).
- **`Icon`** takes Android XML vector drawables (`assets/icons/*.xml` from
  Material Symbols); Metro bundles `.xml` automatically.
- **`useNativeState`** gives worklet-synchronous UI-thread state (needs
  `react-native-worklets`, preinstalled). Best for real-time interactions
  like masking input as it types.
- Confirm props against installed
  `node_modules/@expo/ui/jetpack-compose` `.d.ts` — this surface is newer
  than training data.

*Adapted from Expo's `expo-ui` skill jetpack-compose reference (MIT).*
