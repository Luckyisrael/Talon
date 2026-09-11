# Talon — Style Reference (Companion Mobile App)
> midnight precision instrument, in your pocket

**Theme:** dark (only — no light mode; the system is built on darkness as substrate)

**Platform:** React Native + Expo (expo-router, Expo UI where noted). This file is the mobile translation of the web `DESIGN.md` midnight system: same tokens, same discipline, re-expressed for touch, safe areas, and small screens. Where web and mobile diverge, mobile wins — this file overrules `DESIGN.md` for anything shipped in the app.

The companion app is the build agent's handheld surface: watch builds stream, approve plans, answer the agent's questions, check gate verdicts, and browse project memory — all in the Linear midnight language (void `#08090a`, paper type, one acid-lime `#e4f222` flashlight).

---

## What changes from web to mobile (read first)

| Web habit | Mobile rule |
|---|---|
| Hover states (underline on hover, hover fills) | **No hover exists.** Every interactive element needs a `pressed` state (`opacity 0.6` or background shift) via `Pressable` |
| `box-shadow` elevation stacks | **No box shadows in RN.** Elevation = hairline borders (`StyleSheet.hairlineWidth`, `#23252a`) + surface-level steps. The one exception (acid-lime CTA shadow) becomes a 1px `#c9d91e` bottom edge |
| 0.5px hairlines | `StyleSheet.hairlineWidth` (physical-pixel hairline on any DPI) |
| 1200px max-width, 96px section gaps | Full-bleed screens, 16px horizontal padding, 24–32px section gaps |
| Top nav bar | **Bottom tab bar** (4 tabs max) + per-screen stacked headers |
| 13px nav text, 12px metadata | Minimum 12px, but tappable rows are min **44px** tall (Apple HIG touch target) |
| Inter Variable with `font-feature-settings` | Inter via `expo-font` (`Inter_400Regular`, `Inter_500Medium`); tracking via `letterSpacing` (RN points, not em — convert: `em × fontSize`) |
| Berkeley Mono | `JetBrainsMono_400Regular` via `expo-font`, fallback `monospace`. Same role: IDs, hashes, token counts, timestamps |
| CSS custom properties | `constants/theme.ts` — `Colors`, `Type`, `Spacing`, `Radii` objects (exact values below) |
| Tailwind classes | `StyleSheet.create` + a `useTheme()` hook. No NativeWind dependency — the system is small enough to be explicit |

---

## Tokens — Colors

Identical palette to web. In `constants/theme.ts`:

| Name | Value | Token | Role (mobile) |
|------|-------|-------|---|
| Void | `#08090a` | `Colors.void` | Screen background — every screen starts here |
| Carbon | `#0f1011` | `Colors.carbon` | Card surfaces, tab bar background, sheet backgrounds |
| Obsidian | `#161718` | `Colors.obsidian` | Elevated panels, expanded cards, modal bodies |
| Graphite | `#23252a` | `Colors.graphite` | Hairline borders, dividers, ghost button outlines |
| Smoke | `#383b3f` | `Colors.smoke` | Higher-contrast separators, focused input borders |
| Ash | `#62666d` | `Colors.ash` | Muted body text, inactive tab icons, secondary metadata |
| Fog | `#8a8f98` | `Colors.fog` | Tertiary text, placeholders, icon fills |
| Mist | `#d0d6e0` | `Colors.mist` | Secondary headings, button text on dark |
| Bone | `#e5e5e6` | `Colors.bone` | High-contrast button text fills |
| Paper | `#ffffff` | `Colors.paper` | Primary headings, hero type |
| Acid Lime | `#e4f222` | `Colors.acidLime` | **The one chromatic action per screen** — primary CTA, active tab indicator, in-progress pulse |
| Pulse Green | `#27a644` | `Colors.pulseGreen` | Pass states, verified checks, success dots — supporting only |
| Coral Red | `#eb5757` | `Colors.coralRed` | Fail states, destructive actions, error banners — supporting only |
| Signal Teal | `#02b8cc` | `Colors.signalTeal` | Informational fills, syncing indicators |
| Iris Violet | `#6366f1` | `Colors.irisViolet` | Tier/plan badges |
| Lavender | `#8b5cf6` | `Colors.lavender` | Secondary badges, memory-type indicators |

```ts
// constants/theme.ts
export const Colors = {
  void: '#08090a',
  carbon: '#0f1011',
  obsidian: '#161718',
  graphite: '#23252a',
  smoke: '#383b3f',
  ash: '#62666d',
  fog: '#8a8f98',
  mist: '#d0d6e0',
  bone: '#e5e5e6',
  paper: '#ffffff',
  acidLime: '#e4f222',
  acidLimeEdge: '#c9d91e', // 1px bottom edge on the CTA (replaces the web shadow stack)
  acidLimeInk: '#08090a',  // text on acid-lime
  pulseGreen: '#27a644',
  coralRed: '#eb5757',
  signalTeal: '#02b8cc',
  irisViolet: '#6366f1',
  lavender: '#8b5cf6',
} as const;
```

---

## Tokens — Typography

### Inter — primary typeface via `expo-font`
- **Files:** `Inter_400Regular`, `Inter_500Medium` (two weights only — the 300/590 web weights collapse: 300→400, 590→500 on mobile for binary size)
- **Fallback:** system font stack (San Francisco / Roboto) if fonts fail to load — never block render on fonts
- **Tracking conversion:** web uses em; RN `letterSpacing` is in points. `points = em × fontSize`. E.g. display `-0.022em × 40 = -0.88pt`

### JetBrains Mono — code-adjacent text
- **File:** `JetBrainsMono_400Regular`, fallback `monospace`
- **Role:** build IDs, wallet addresses (truncated middle-ellipsis), token counts (`149K in · 2K out`), timestamps, gate names, round numbers

### Mobile type scale

Screen widths kill the web's 48–72px display sizes. Cap display at 40px; everything else shifts down one step.

| Role | Size | Weight | Line Height | Tracking (pt) | Token | Use |
|------|------|--------|-------------|---------------|-------|-----|
| display | 40 | 500 | 1.05 | -0.88 | `Type.display` | Onboarding hero headline only |
| heading | 32 | 500 | 1.1 | -0.7 | `Type.heading` | Screen titles, empty-state headlines |
| subheading | 24 | 500 | 1.25 | -0.29 | `Type.subheading` | Section titles, card titles |
| title | 20 | 500 | 1.3 | -0.24 | `Type.title` | List headers, sheet titles |
| body-lg | 17 | 400 | 1.5 | 0 | `Type.bodyLg` | Emphasis blocks, question cards |
| body | 16 | 400 | 1.5 | 0 | `Type.body` | Default body copy |
| body-sm | 15 | 400 | 1.55 | -0.17 | `Type.bodySm` | Secondary copy, trace rows |
| caption | 13 | 400 | 1.35 | 0 | `Type.caption` | Metadata under titles |
| label | 12 | 400 | 1.4 | 0 | `Type.label` | Tab labels, badges, eyebrow labels |
| micro | 10 | 500 | 1.5 | +0.4 | `Type.micro` | Eyebrow kickers (`BUILD 42`, `GATE: BOOT`), letter-spaced caps |
| mono | 12 | 400 (JB Mono) | 1.5 | 0 | `Type.mono` | Hashes, IDs, token counts |
| mono-sm | 10 | 400 (JB Mono) | 1.5 | 0 | `Type.monoSm` | Timestamps, round numbers |

```ts
export const Type = {
  display:    { fontSize: 40, lineHeight: 42, letterSpacing: -0.88, fontWeight: '500' },
  heading:    { fontSize: 32, lineHeight: 35, letterSpacing: -0.7,  fontWeight: '500' },
  subheading: { fontSize: 24, lineHeight: 30, letterSpacing: -0.29, fontWeight: '500' },
  title:      { fontSize: 20, lineHeight: 26, letterSpacing: -0.24, fontWeight: '500' },
  bodyLg:     { fontSize: 17, lineHeight: 26, fontWeight: '400' },
  body:       { fontSize: 16, lineHeight: 24, fontWeight: '400' },
  bodySm:     { fontSize: 15, lineHeight: 23, letterSpacing: -0.17, fontWeight: '400' },
  caption:    { fontSize: 13, lineHeight: 18, fontWeight: '400' },
  label:      { fontSize: 12, lineHeight: 17, fontWeight: '400' },
  micro:      { fontSize: 10, lineHeight: 15, letterSpacing: 0.4, fontWeight: '500' },
  mono:       { fontSize: 12, lineHeight: 18, fontFamily: 'JetBrainsMono_400Regular' },
  monoSm:     { fontSize: 10, lineHeight: 15, fontFamily: 'JetBrainsMono_400Regular' },
} as const;
```

---

## Tokens — Spacing & Shapes

Base unit 4px, same ladder, trimmed for small screens (drop 80/96/128 — nothing on mobile spaces that far):

```ts
export const Spacing = { s4: 4, s8: 8, s12: 12, s16: 16, s20: 20, s24: 24, s32: 32, s48: 48, s64: 64 } as const;
export const Radii = {
  small: 2,    // tiny ticks
  badges: 4,   // badges, tags
  buttons: 6,  // buttons, inputs
  inputs: 6,
  cards: 12,   // cards, sheets, modals — the max radius, same as web
  pills: 9999, // pills, tab indicators
} as const;
```

### Layout
- **Screen padding:** 16px horizontal, respects `SafeAreaView` (top) + tab bar (bottom)
- **Section gap:** 24–32px (web's 96px doesn't exist here)
- **Card padding:** 16px (web's 24px breathes down)
- **Element gap:** 8px (unchanged)
- **Touch target:** min 44×44pt on every tappable — rows, icon buttons, tab items
- **Hairlines:** `StyleSheet.hairlineWidth`, color `graphite`; separators `smoke` only between major sections

---

## Components

### Primary Action Button (Acid Lime)
The one chromatic button per screen. Full-width on phones (thumb reach), min height 48px.
`bg acidLime, text acidLimeInk, radius buttons(6), Inter 500 16px, 1px bottom edge acidLimeEdge` (replaces the web inset-shadow stack — RN has no inset shadows). Pressed: `opacity 0.85`. Disabled: `bg smoke, text ash` (never greyed lime — a dead neon looks broken).

### Ghost / Outline Button
Transparent, 1px `graphite` border (hairlineWidth on small buttons), text `mist`, radius 6, min height 44px. Pressed: border `smoke`, background `rgba(255,255,255,0.04)`.

### Pill Button
`rgba(255,255,255,0.05)` fill, text `mist`, radius pills, padding 8px 14px (taller than web's 4px for touch), Inter 13px. Used for filter chips (All / Screens / Features), tier badges.

### Card (Showcase)
`bg carbon`, radius cards(12), 1px hairline `graphite` border, padding 16px. Houses build-status summaries, gate verdict lists, plan checklists. No shadow — the border is the elevation (same as web).

### Card (Subtle)
`bg rgba(255,255,255,0.02)`, radius 6, padding 12px. Trace rows, memory entries, per-round log lines.

### Text Input
`bg rgba(255,255,255,0.02)`, 1px `graphite`, text `mist`, radius 6, padding 12px 14px, min height 48px, Inter 16px (iOS zooms inputs under 16px on focus — **never go below 16px**). Focused: border `mist`. Placeholder: `fog`.

### Badge / Status Tag
`bg rgba(255,255,255,0.05)`, text `fog`, radius badges(4), padding 2px 6px, Inter 12px. Status variants: `pulseGreen` dot + text for pass/verified, `coralRed` for fail, `acidLime` dot (never full fill) for running/in-progress, `fog` for pending/skipped.

### Bottom Tab Bar
`bg carbon` with top hairline `graphite`, 4 tabs: **Build** (agent + trace), **Plan** (checklist), **Device** (preview/screenshot), **More** (memory, export, settings). Active: `acidLime` 2px top indicator + `paper` icon; inactive: `ash` icon + 12px label. Height 56px + safe-area inset. No badges with counts except a single lime dot for "build running".

### Build Status Header (per screen top)
Eyebrow `micro` caps (`BUILD 42 · ROUND 7`), 32px heading (project name), caption row (`verified (static only — boot could not run)` in amber when gates skipped — mirrors the web's partial-verified state). This is the mobile equivalent of the web command bar.

### Question Card (ask_user)
`bg obsidian`, radius 12, hairline border, padding 16px. Question in `bodyLg`, choices as full-width ghost buttons stacked vertically (min 48px each — no side-by-side buttons on phones), optional free-text input on top. Approve/answer actions use the acid-lime primary (the single chromatic action rule holds inside the card).

### Gate Verdict Row
44px+ row: status dot (8px, pulseGreen/coralRed/acidLime/fog) + gate name (Inter 500 15px `mist`) + right-aligned mono detail (`1.2s`, `passed`, `unavailable: no device`). Tappable → expands to the tool output in mono 12px. This is the crash-index / SkippedGates surface on mobile.

### Plan Checklist Row
Checkbox square (20px, radius 4, hairline border; checked = acid-lime fill + void check) + screen/feature name + status badge. Sections: Screens, Features. Completed rows dim to `ash`.

### Streaming Prose / Thinking Row
Agent text streams in `body` 16px; thinking indicator = three pulsing dots (`fog`, 400ms stagger) + elapsed mono timer (`0:42`). No shimmer gradients (RN LinearGradient only for the splash floor — see below).

### Splash / Onboarding Floor
The one allowed gradient: void `#08090a` → `#d0d6e0` wash at 8% opacity behind the hero mark (same role as the web hero floor, dialed down for OLED). Used on splash + onboarding carousel only, never in app chrome.

---

## Screens (expo-router map)

```
app/
  index.tsx              Splash: mark + `Talon` 40px display, floor wash, auto-routes
  onboarding/
    welcome.tsx          Display headline, what the agent builds, acid-lime CTA
    features.tsx         3-row feature list (Plan / Build / Device), ghost "Skip"
    connect.tsx          Wallet connect explainer + MWA button (auth entry)
  auth/
    login.tsx            Email input + acid-lime Continue (16px inputs, no OAuth buttons v1)
    verify.tsx           6-box code entry (48px boxes, mono 20px, auto-advance)
  (tabs)/
    _layout.tsx          Bottom tabs: Build / Plan / Device / More
    build.tsx            Status header + streaming trace + composer (multiline input + lime send)
    plan.tsx             Plan checklist sections + triage card (decision/date/exposure)
    device.tsx           Phone-framed preview, screenshot button, gate verdict list
    more.tsx             Memory list, export panel entry, tier badge, sign out
  project/[id].tsx       Project switcher sheet (bottom sheet, carbon, radius 12 top)
  session/[id].tsx       Deep link: session with device attached (from push)
```

### Onboarding (3 screens, swipeable, x-of-3 micro caps)
1. **Welcome** — display 40px "Build Solana apps by talking.", body copy, lime CTA "Start building"
2. **Features** — three subtle cards: *Plan* (checklists), *Build* (agent writes code), *Device* (installs on hardware). Ghost "Skip"
3. **Connect** — wallet explainer (why: dApp Store submission), MWA button (lime), ghost "Later" → `(tabs)/build`

### Auth (2 screens)
- **Login** — 32px heading "Welcome back", email field (keyboardType email, autoCapitalize none), lime Continue, caption "Magic link — no passwords"
- **Verify** — "Check your inbox" + 6 mono boxes; wrong code shakes (translateX keyframe) + coral caption; resend ghost button with 30s cooldown mono countdown

---

## Do's and Don'ts (mobile deltas on top of the web list)

### Do
- Use `Pressable` with `opacity 0.6` pressed states everywhere — there is no hover to fall back on
- Keep inputs at 16px+ (iOS focus zoom), buttons at 48px, rows at 44px+
- Use `hairlineWidth` for all structural borders; reserve 1px `smoke` for focused/active edges only
- Render one acid-lime action per screen — inside Question Cards it is the Answer/Approve button
- Truncate wallet addresses middle-ellipsis in mono (`8x...3fQ2`) — never wrap them
- Mirror the web's partial-verified copy exactly: "verified (static only — …)" so cross-platform status reads identically

### Don't
- Do not use `box-shadow` / `elevation` for card separation — hairlines only (Android elevation renders grey glows that break the midnight system)
- Do not use side-by-side buttons — stack full-width (thumb reach, 320px minimum widths)
- Do not use 10–13px text on anything tappable — 12px labels are fine, tappables need 44px rows regardless of text size
- Do not auto-play anything on the Device tab — screenshots render on demand (battery + data)
- Do not put more than 4 tabs in the tab bar — More holds the overflow
- Do not use haptics decoratively — `selection` on toggle, `notificationSuccess/Error` on build verdicts only

---

## Agent Prompt Guide (for the builder agent)

**Quick Color Reference:** canvas `#08090a` · card `#0f1011` · hairline `#23252a` · body `#d0d6e0` · muted `#8a8f98` · accent `#e4f222` (one per screen) · success `#27a644` · fail `#eb5757`

**Example component prompts:**

1. **Onboarding welcome:** `SafeAreaView` void canvas. Display 40/500 `paper`, tracking -0.88. Body 16px `fog`. Full-width lime CTA (48px, radius 6, ink text). Splash floor gradient at 8% behind the mark. Micro caps `1 OF 3` top-left.
2. **Build status header:** micro eyebrow (`BUILD 42 · ROUND 7`), 32px project name, caption row with amber partial-verified note when gates skipped. 16px screen padding.
3. **Question card:** obsidian card, radius 12, bodyLg question, choices as stacked full-width ghost buttons (48px), lime Approve. No side-by-side layout.
4. **Gate verdict list:** 44px+ rows, 8px status dot, 15px/500 gate name, right mono detail. Expandable to mono 12px output. Skipped gates render `fog` with "could not run" copy.
5. **Bottom tabs:** carbon bar, graphite top hairline, 4 tabs, active = lime 2px top indicator + paper icon, inactive ash. 56px + safe-area.

**Screen-build prompts (expo-router):** always specify route path, header (title + back), and which shared components compose it. New screens reuse `Card`, `GhostButton`, `LimeButton`, `Badge`, `Mono` from `components/ui/` — never inline one-off styles for chrome.
