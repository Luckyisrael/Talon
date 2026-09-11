---
name: expo-ui-jetpack-compose
description: Platform-specific Android UI with @expo/ui/jetpack-compose — real Jetpack Compose components, modifiers, LazyColumn, and useNativeState. Load when the universal @expo/ui layer lacks a component or you need native Android behavior. Android-only — isolate in .android.tsx files or Platform.OS guards.
version: "@expo/ui/jetpack-compose (Expo SDK 55+); useNativeState needs react-native-worklets (preinstalled)"
autoAttach: true
triggers: [import:@expo/ui/jetpack-compose]
license: MIT (Expo — see ATTRIBUTION.md)
source: https://github.com/expo/skills (plugins/expo/skills/expo-ui/references/jetpack-compose.md)
---

# Android-native UI with `@expo/ui/jetpack-compose`

The Talon is an Android device, so this layer targets its real runtime directly —
real Jetpack Compose driven from React. Reach for it when the universal `@expo/ui`
components don't cover what you need on Android; start from the **`expo-ui`** skill
(the universal layer) first, and drop to this one only for the gap.

> **Android only.** Code importing from `@expo/ui/jetpack-compose` crashes on iOS
> with "Unable to get view config". Since the Talon is Android-first this is a mild
> constraint, not a real cost — but still isolate it: put the tree in an
> `.android.tsx` component file, or guard with `Platform.OS === 'android'`. `Host`
> must be imported from `@expo/ui` (the universal root), **not** from
> `@expo/ui/jetpack-compose`.
>
> The platform-specific layer exists on **Expo SDK 55**, whereas the universal
> `@expo/ui` layer needs SDK 56+ — so on an SDK 55 baseline this Compose layer may be
> usable where the universal layer is not yet. Confirm against the installed version.

## File placement with Expo Router

**Do not put `.android.tsx` files inside `app/`.** Expo Router does not support
platform-extension suffixes for route files and throws a "no fallback sibling"
render error. Place platform-specific components in `components/` (outside the route
tree) and import them from a regular route:

```
components/ProductList.android.tsx   ← Compose tree lives here
app/product-list.tsx                 ← regular Expo Router route, imports it
```

```tsx
// app/product-list.tsx
import ProductList from '../components/ProductList';
export default ProductList;
```

Or keep one route file and branch on `Platform.OS`:

```tsx
import { Platform } from 'react-native';
const ComposeList =
  Platform.OS === 'android'
    ? require('../components/ProductList.android').default
    : null;
```

## Writing a Compose tree

- Components import from `@expo/ui/jetpack-compose`; modifiers from
  `@expo/ui/jetpack-compose/modifiers`.
- Every Compose tree must be wrapped in `Host`. Use `<Host matchContents>` for
  intrinsic sizing, or `<Host style={{ flex: 1 }}>` when you need an explicit size
  (e.g. as the parent of a `LazyColumn`).

```tsx
import { Host } from '@expo/ui';                                  // Host: universal root
import { Column, Button, Text } from '@expo/ui/jetpack-compose';
import { fillMaxWidth, paddingAll } from '@expo/ui/jetpack-compose/modifiers';

export default function Hello() {
  return (
    <Host matchContents>
      <Column verticalArrangement={{ spacedBy: 8 }} modifiers={[fillMaxWidth(), paddingAll(16)]}>
        <Text style={{ typography: 'titleLarge' }}>Hello</Text>
        <Button onPress={() => console.log('pressed')}>Press me</Button>
      </Column>
    </Host>
  );
}
```

- `@expo/ui`'s API mirrors Jetpack Compose and Material Design 3 — lean on that
  mental model when choosing components and modifiers.
- `RNHostView` embeds React Native components inside a Compose tree — wrap any RN
  child in `<RNHostView>`.
- If a needed composable or modifier is missing, it can be added via a local Expo
  module — **confirm with the user before extending.**

## Key components

- **`LazyColumn`** — a scrollable Compose list. Wrap in `<Host style={{ flex: 1 }}>`.
  **Not for large lists:** each item is a JSX node on the JS thread and slows down at
  scale. For big or unbounded data, use RN's `FlatList` or a virtualizer instead (see
  `rn-best-practices`).
- **`Icon`** — `<Icon source={require('./wifi.xml')} size={24} />` with Android XML
  vector drawables. Get them from Material Symbols (fonts.google.com/icons → Android →
  download the XML), save under `assets/` (e.g. `assets/icons/wifi.xml`). Metro
  bundles `.xml` assets automatically — no config changes.

## `useNativeState`

Creates observable state that updates synchronously on the UI thread via worklets —
immediate native updates without waiting for a React render. Requires
`react-native-worklets` (**preinstalled** in this project); without it, updates fall
back to React and flicker remains. Best for real-time interactions (e.g. a text field
that masks or formats input as the user types).

- `ObservableState.value` is readable/writable from worklets; `onChange` fires a
  worklet listener on change.

## Confirming the exact API

This surface is newer than most training data. Before using a component or modifier,
confirm its real props against the installed version: read the relevant
`{ComponentName}/index.d.ts` from `node_modules/@expo/ui/jetpack-compose` in the
workspace — the most reliable source of truth. Expo's docs list the components at
`docs.expo.dev/versions/latest/sdk/ui/jetpack-compose/`.

---

*Adapted from the `jetpack-compose.md` reference inside Expo's `expo-ui` skill (MIT).
The upstream `list-components.js` helper script is not vendored — read the installed
type files instead. See `ATTRIBUTION.md`.*
