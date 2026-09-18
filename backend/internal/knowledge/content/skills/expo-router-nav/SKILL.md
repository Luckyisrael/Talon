---
name: expo-router-nav
description: Structure screens and navigation with expo-router's file-based routing, and choose the right semantics — push vs replace vs dismissTo, modals vs sheets, one-way doors, back-blocking. Load when creating screens, layouts, tabs, stacks, navigating, or guarding sign-in/paywall/onboarding states.
version: "expo-router@55.0.x, expo@55"
autoAttach: true
triggers: [path:app/**, import:expo-router, content:router.push, content:router.replace, content:router.dismissTo, content:presentation, content:Stack.Protected, content:usePreventRemove]
---

# Navigation with expo-router — grammar and judgment

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
  `router.back()`, `router.replace('/')`, `router.dismissTo(href)` for "finish this
  flow and land on X".
- Pass params via the href/path, then read them with `useLocalSearchParams()`.
- `<Redirect href>` for declarative redirects (auth walls, finished onboarding).

## Presentation is meaning

- A self-contained task with steps → `presentation: 'modal'` with its own stack
  and its own Cancel/Done; a short interruption (picker, filters, item options)
  → a sheet with detents and drag-to-dismiss; immersive content →
  `fullScreenModal` with an explicit Close; something floating over a
  still-visible screen (confirm card, lightbox, coach mark) → a transparent
  overlay; destructive confirms → an action sheet; share, web, or photo
  picking → the system controller, never a rebuilt route. **A sheet that grows
  a second step was a modal all along; if a link could open it, it is a route,
  not a `useState` sheet.**

## The laws

1. **Push goes deeper, replace moves on.** `router.push` when the user will
   want to return here; `router.replace` / `<Redirect>` when coming back
   would land in a state the world has moved past. Back undoes *navigation*,
   never *events*.
2. **One-way doors leave the stack.** Sign-in on a wall app, finished
   onboarding (Skip included), a purchase, a completed session: guard with
   `Stack.Protected` and land with `replace`, so back can never re-enter the
   old state — Android back from Home exits the app, never shows Login.
   But keep the user's *place*: sign-in demanded by one action (save, follow,
   buy) is a modal over the screen that completes the action where it was
   tapped.
3. **Back is blocked in exactly two cases** — an irreversible request in
   flight (seconds, with visible progress) and unsaved work in a modal (ask
   first), both via `usePreventRemove`. Transient in-screen state (selection
   mode, an expanded search, an open in-screen sheet) consumes the first
   back, then back leaves. Anything else that traps back — a funnel, a
   rating prompt — is a defect.
4. **Tabs are peers.** No slide between tabs, each tab keeps its own stack,
   re-tapping the active tab pops to its root. Deep links land with a real
   stack underneath; cold start lands by state, splash held until session
   state has resolved — never a Login flash before Home.
5. **Study the grammar, not just the pixels.** When walking a winning flow,
   note what each step *is* — push, modal, sheet — and copy that consistency.

## Talon's wallet-specific door

Connecting a wallet is not a one-way door and must not be guarded like one:
a connected account is session state the user can revoke at any time. Wire
connecting as a modal or inline action that returns to the screen that asked,
never a `replace` onto Home. Disconnecting lands back on the same screen in
its unauthenticated state. See `wallet-mwa`.

## Rules

- Every screen file's **default export** is the component. A route with no default
  export renders nothing.
- Keep `index.js` (`import 'expo-router/entry'`) intact — it is the native entry.
- Do not install or import `@react-navigation/*` yourself. expo-router provides
  `Stack`, `Tabs`, and `Drawer`. Its react-navigation dependencies are transitive.
- One root layout only. Nest further layouts inside route groups, not by adding a
  second root.
