---
name: mobile-navigation
description: Navigation semantics for Talon apps — push vs replace vs dismissTo, modal vs sheet vs overlay, the one-way doors where back must not exist, back-blocking, tabs as peers, and deep-link stack integrity. Load when wiring routes, adding a screen to app/, choosing presentation for a flow, or guarding sign-in/paywall/onboarding states.
version: "appllama-app-design-skill@1.3.0 navigation laws — adapted; Appllama MIT"
autoAttach: true
triggers: [path:app/**, import:expo-router, content:router.push, content:router.replace, content:router.dismissTo, content:presentation, content:Stack.Protected, content:usePreventRemove]
license: MIT (Appllama — see ATTRIBUTION.md)
source: https://github.com/appllama/appllama-skills (skills/appllama-app-design-skill/SKILL.md)
---

# Mobile navigation — semantics, not screens

Users feel navigation in ten seconds and a screenshot cannot show it. Every
transition answers three questions: what is the destination to here, must the
user be able to come back, and what does back (the chevron, the edge swipe,
the Android hardware button) do afterwards. File-based routes under `app/`
are the whole picture — see `expo-router-nav` for the file grammar; this skill
is the *judgment* on top of it.

## The laws

1. **Push goes deeper, replace moves on.** `router.push` when the user will
   want to return here; `router.replace` / `<Redirect>` when coming back
   would land in a state the world has moved past; `router.dismissTo(href)`
   for "finish this flow and land on X". Back undoes *navigation*, never
   *events*.
2. **Presentation is meaning.** A self-contained task with steps →
   `presentation: 'modal'` with its own stack and its own Cancel/Done; a
   short interruption (picker, filters, item options) → a sheet with detents
   and drag-to-dismiss; immersive content → `fullScreenModal` with an
   explicit Close; something floating over a still-visible screen (confirm
   card, lightbox, coach mark) → a transparent overlay; destructive confirms
   → an action sheet; item actions → the native context menu; share, web, or
   photo picking → the system controller, never a rebuilt route. **A sheet
   that grows a second step was a modal all along; if a link could open it,
   it is a route, not a `useState` sheet.**
3. **One-way doors leave the stack.** Sign-in on a wall app, finished
   onboarding (Skip included), a purchase, a completed session: guard with
   `Stack.Protected` and land with `replace`, so back can never re-enter the
   old state — Android back from Home exits the app, never shows Login; a
   paid paywall never re-opens. But keep the user's *place*: sign-in
   demanded by one action (save, follow, buy) is a modal over the screen
   that completes the action where it was tapped, and a paywall opened from
   a feature dismisses back onto the feature, unlocked.
4. **Back is blocked in exactly two cases** — an irreversible request in
   flight (seconds, with visible progress) and unsaved work in a modal (ask
   first), both via `usePreventRemove`. Transient in-screen state (selection
   mode, an expanded search, an open in-screen sheet) consumes the first
   back, then back leaves. Anything else that traps back — a funnel, a
   rating prompt — is a defect.
5. **Tabs are peers.** No slide between tabs, each tab keeps its own stack,
   re-tapping the active tab pops to its root, full-attention screens
   (composer, checkout) live in the root stack *above* the tabs. Deep links
   land with a real stack underneath; cold start lands by state, splash held
   until session state has resolved — never a Login flash before Home.
6. **Study the grammar, not just the pixels.** When walking a winning flow,
   note what each step *is* — push, modal, sheet — and copy that
   consistency.

## Talon's wallet-specific door

Connecting a wallet is not a one-way door and must not be guarded like one:
a connected account is session state the user can revoke at any time. Wire
connecting as a modal or inline action that returns to the screen that asked,
never a `replace` onto Home. Disconnecting lands back on the same screen in
its unauthenticated state. See `wallet-mwa`.
