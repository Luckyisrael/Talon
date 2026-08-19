---
name: expo-router-nav
description: Structure screens and navigation with expo-router's file-based routing. Load when creating screens, layouts, tabs, stacks, or navigating between routes.
version: "expo-router@55.0.x, expo@55"
autoAttach: true
triggers: [path:app/**, import:expo-router]
---

# Navigation with expo-router

Routing is **file-based**: a file under `app/` is a route. There is no central
navigator config and you do not import react-navigation directly.

## The layout

- `app/_layout.tsx` — root layout. Wraps every screen; put providers and the root
  `<Stack>` or `<Tabs>` here.
- `app/index.tsx` — the entry screen (route `/`).
- `app/profile.tsx` — a static route (`/profile`).
- `app/token/[id].tsx` — a dynamic route (`/token/abc`). Read the param with
  `useLocalSearchParams()`.
- `app/(tabs)/_layout.tsx` — a tab group. Parentheses are a layout group and do not
  appear in the URL.

```tsx
// app/_layout.tsx
import { Stack } from 'expo-router';

export default function RootLayout() {
  return <Stack screenOptions={{ headerShown: false }} />;
}
```

```tsx
// app/token/[id].tsx
import { useLocalSearchParams } from 'expo-router';
import { Text, View } from 'react-native';

export default function TokenDetail() {
  const { id } = useLocalSearchParams<{ id: string }>();
  return <View><Text>{id}</Text></View>;
}
```

## Navigating

- Declarative: `<Link href="/token/abc">` from `expo-router`.
- Imperative: `import { useRouter } from 'expo-router'` then `router.push('/token/abc')`,
  `router.back()`, `router.replace('/')`.
- Pass params via the href/path, then read them with `useLocalSearchParams()`.

## Rules

- Every screen file's **default export** is the component. A route with no default
  export renders nothing.
- Keep `index.js` (`import 'expo-router/entry'`) intact — it is the native entry.
- Do not install or import `@react-navigation/*` yourself. expo-router provides
  `Stack`, `Tabs`, and `Drawer`. Its react-navigation dependencies are transitive.
- One root layout only. Nest further layouts inside route groups, not by adding a
  second root.
