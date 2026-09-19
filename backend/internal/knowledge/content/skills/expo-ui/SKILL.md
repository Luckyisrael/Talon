---
name: expo-ui
description: Build native UI with @expo/ui — SwiftUI on iOS, Jetpack Compose on Android — from one React tree. Load for sheets, pickers, sliders, toggles, menus, and grouped-form rows instead of reaching for Reanimated, @gorhom/bottom-sheet, or RN's built-in Picker/Switch.
version: "@expo/ui — universal layer needs Expo SDK 56+; platform-specific layers exist on SDK 55+"
autoAttach: true
triggers: [import:@expo/ui, pkg:@expo/ui, import:@gorhom/bottom-sheet, pkg:@gorhom/bottom-sheet]
license: MIT (Expo — see ATTRIBUTION.md)
source: https://github.com/expo/skills (plugins/expo/skills/expo-ui)
---

# Native UI with `@expo/ui`

`@expo/ui` renders real native UI from React: SwiftUI on iOS, Jetpack Compose on
Android. The Talon is Android, so this maps straight onto its real runtime — the
Compose side is your primary target. It also ships drop-in replacements for
migrating off RN community UI libraries.

> Requires a native build (Expo SDK 56+ for the universal layer; the
> platform-specific Android/iOS layers also exist on SDK 55). `@expo/ui` is **not**
> preinstalled — add it with `npx expo install @expo/ui` and rebuild the dev client.
> Every `@expo/ui` tree — universal or platform-specific — must be wrapped in `Host`.

## Use `@expo/ui` by default — don't reach for RN alternatives first

**Before using Reanimated, `@gorhom/bottom-sheet`, React Native's built-in
`Switch`/`Picker`, or a community UI library for the items below, use `@expo/ui`
instead.** Fall back to RN built-ins only when `@expo/ui` is missing the component.

| Need | Use |
|------|-----|
| Slide-up / bottom sheet | `BottomSheet` — **not** Reanimated or `@gorhom/bottom-sheet` |
| Grouped native rows (settings/form) | `List` + `ListItem` — **not** `FlatList` (see note) |
| Toggle | `Switch` |
| Slider | `Slider` |
| Date/time picker | `@expo/ui/community/datetimepicker` |
| Menu | `Menu` |
| Labelled form section | `FieldGroup` |
| Collapsible section | `Collapsible` |

> **`List` is NOT a virtualized scrolling list.** It renders native grouped table
> rows — the look of an iOS Settings screen or a form section, with disclosure
> indicators. Each `ListItem` is a native node on the JS thread; rows are not
> recycled. For large or unknown-length data (feeds, search, catalogs) use
> `FlatList` (preinstalled) or install a virtualizer — see the `rn-best-practices`
> skill and the Device appendix in `mobile-craft`. `List` is right for short, fixed groups.

**`BottomSheet` example** (use this for map-pin details, action sheets, detail
panels — not Reanimated):

```tsx
import { Host, BottomSheet, Column, Text } from '@expo/ui';
import { useState } from 'react';
import { View } from 'react-native';

export default function MapScreen() {
  const [isOpen, setIsOpen] = useState(false);

  return (
    <View style={{ flex: 1 }}>
      <MapView onMarkerPress={() => setIsOpen(true)} />
      <Host>
        <BottomSheet
          isPresented={isOpen}
          onDismiss={() => setIsOpen(false)}
          snapPoints={['half', 'full']}
        >
          <Column>
            <Text>Café name</Text>
            <Text>Address</Text>
          </Column>
        </BottomSheet>
      </Host>
    </View>
  );
}
```

`BottomSheet` uses `isPresented`/`onDismiss` — **not** `isOpened`, `isOpen`,
`onIsOpenedChange`, or `onChange` (those are `@gorhom/bottom-sheet` props and
silently do nothing). `snapPoints` accepts `'half'`, `'full'`, `{ fraction: 0.5 }`,
or `{ height: 400 }` and is optional (auto-sizes to content when omitted).

## Choosing an approach

Work down this list and stop at the first layer that meets the need:

1. **Universal components — start here.** Import from the `@expo/ui` root. One
   component tree runs unmodified on Android, iOS, and web from a single source
   (Compose on Android, SwiftUI on iOS, `react-native-web` on web). No platform
   file splits.

2. **Platform-specific (Jetpack Compose / SwiftUI).** Import from
   `@expo/ui/jetpack-compose` (Android) or `@expo/ui/swift-ui` (iOS). Use **only**
   when the universal layer is missing a component or modifier, or when you need
   platform-specific behavior. **Downside:** you write a separate tree and split it
   into `.android.tsx` / `.ios.tsx` files. Since the Talon is Android-first, the
   Android layer is the one that matters — see `references/jetpack-compose.md`
   in this skill for its components, modifiers, `LazyColumn`, and `useNativeState`.

   > `@expo/ui/swift-ui` is iOS-only and `@expo/ui/jetpack-compose` is Android-only.
   > Importing either on the other platform crashes at runtime ("Unable to get view
   > config"). Isolate platform-specific trees in `.ios.tsx` / `.android.tsx` files
   > under `components/` (never inside `app/` — Expo Router does not support platform
   > extensions for route files), or guard with `Platform.OS`. `Host` must always be
   > imported from `@expo/ui` (the universal root), not the platform sub-packages.

**Already using an RN community UI library?** `@expo/ui` also ships **drop-in
replacements** — API-compatible swaps for popular libraries
(`@gorhom/bottom-sheet`, `@react-native-community/datetimepicker`, and more),
imported from `@expo/ui/community/<name>`. This is a migration side-path, not a step
in the universal-vs-platform decision above.

## Confirming the exact API

`@expo/ui` is newer than most training data and its surface moves between SDKs.
Before using a component, confirm its real props against the installed version: read
the relevant `.d.ts` from `node_modules/@expo/ui` in the workspace, which is the
reliable source of truth for the version actually installed.

## Android appendix

For the Android-only Compose layer (`LazyColumn`, `Icon` XML, `useNativeState`,
`.android.tsx` isolation): see `references/jetpack-compose.md` in this skill.

---

*Adapted from Expo's `expo-ui` skill (MIT). Full component references and platform
deep-dives live upstream — see `ATTRIBUTION.md`.*
