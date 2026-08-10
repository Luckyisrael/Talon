# SeekerStudio — Frontend Design System

> **Scope:** This is the mandatory rule set for building **any UI** in the SeekerStudio
> frontend (Next.js / React + TypeScript + Tailwind). Every screen, component, and
> visual element must follow these rules.
>
> **Adaptation note:** The source rules were written for React Native. In our Next.js
> web frontend the React Native primitives map as follows:
> `View → div`, `Text → span/p`, `Pressable → button`, `ScrollView → div(overflow)`,
> `FlatList/SectionList → CSS grid + virtualized list`, `SafeAreaView → safe-area env()`,
> `StyleSheet.hairlineWidth → 1px`. Touch-target 44×44pt → pointer target 44×44px.
> All other rules apply verbatim.

---

## 1. Design Language Hierarchy

Three design traditions inform this system. They are **not equal** — each has a clear role:

| Role | Design Tradition | Where It Applies |
|---|---|---|
| **Structural backbone** | Swiss Minimalism | Grid alignment, typographic hierarchy, spacing discipline, layout geometry — everywhere |
| **Surface treatment** | Material You (Material 3) | Tonal color blending on cards/elevations, pill-shaped controls, dynamic surfaces — applied within Swiss structure |
| **Marketing playfulness** | Bento Grid | Asymmetric, varied-ratio card layouts — landing page and dashboard project grid **only** |

### Rules

1. **The IDE workspace never uses Bento-style asymmetry.** A dense, professional tool needs predictable, aligned regions. Swiss grid governs IDE layout.
2. **Material You tonal surfaces are a material treatment applied within Swiss structure** — cards and elevated surfaces use tonal blending, but the grid and spacing discipline stays Swiss.
3. **Bento Grid is reserved strictly for marketing surfaces** — the landing page hero/features section and dashboard project grid. Nowhere else.

---

## 2. Color System

### 2.1 Accent Color Rule

**Amber (`--accent: #F5A623`) is the only general-purpose accent color.** It is used for:
- Primary actions (submit buttons, approve, CTA)
- Focus states and active indicators
- General UI emphasis and highlights

### 2.2 Solana Brand Colors — Contextual Only

Solana purple (`--solana-purple: #9945FF`) and Solana green (`--solana-green: #14F195`) appear **only** on screens/elements that are specifically about Solana:
- Network badge or chain indicator
- Wallet connection status
- Transaction confirmation
- Solana-specific feature cards (e.g., "Solana Native" on the landing page)
- Solana-branded suggestion chips

**If a screen has no Solana-specific concept, it contains zero purple or green.** Replace decorative purple/green with amber or `text-secondary`.

### 2.3 Full Palette

| Token | Hex | Usage |
|---|---|---|
| `--background` | `#0A0A0A` | Screen backdrop |
| `--surface-1` | `#111111` | Lowest surface |
| `--surface-2` | `#161616` | Card surface |
| `--surface-3` | `#1A1A1A` | Elevated surface |
| `--surface-4` | `#222222` | Higher elevation |
| `--surface-5` | `#2A2A2A` | Highest surface |
| `--text-primary` | `#FFFFFF` | Headings, primary text |
| `--text-secondary` | `#9CA3AF` | Body text |
| `--text-muted` | `#6B7280` | Labels, metadata |
| `--accent` | `#F5A623` | Primary action (amber) |
| `--accent-hover` | `#E6951A` | Accent hover state |
| `--success` | `#22C55E` | Success status |
| `--error` | `#EF4444` | Error status |
| `--warning` | `#F59E0B` | Warning status |
| `--solana-purple` | `#9945FF` | Solana-specific only |
| `--solana-green` | `#14F195` | Solana-specific only |

---

## 3. Spacing Scale

4px → 8px → 12px → 16px → 20px → 24px → 32px → 40px → 48px → 64px

Mathematical padding: standardize container padding to `16px` or `20px`. Outer container margin equals or exceeds inner spacing between child elements.

---

## 4. Border Radius

| Token | Value | Usage |
|---|---|---|
| `--radius-sm` | 8px | Small elements |
| `--radius-md` | 12px | Cards, buttons |
| `--radius-lg` | 16px | Large cards |
| `--radius-xl` | 20px | Outer containers |
| `--radius-full` | 9999px | Pill shapes |

**Corner nesting rule:** `Inner Radius = Outer Radius - Container Padding`. Example: outer card with 20px radius and 16px padding → inner nested item gets 4px radius.

---

## 5. Typography

- **Primary:** Geist Sans (headings, body)
- **Mono:** Geist Mono (code, metadata, labels)
- **Tracking:** Uppercase labels use `letterSpacing: 1.2px` (monospace uppercase)
- **Hierarchy:** Never skip heading levels. Clear visual contrast between numbers/headings and labels.

---

## 6. Critical Constraints

- **44×44px minimum** pointer targets for all interactive elements (`button`, chips, toggles, links)
- **1px hairline borders** with `rgba(150, 150, 150, 0.18)` to separate sections without visual noise
- **Mobile-first** responsive design with safe-area awareness

---

## 7. Interactive States

Every interactive element must have four deliberately styled states:

| State | Treatment |
|---|---|
| **Default** | Base appearance |
| **Hover** | Color shift (typically `hover:bg-surface-4` or `hover:bg-accent-hover`) |
| **Active/Pressed** | Scale feedback (`active:scale-[0.98]`) or darker shade |
| **Disabled** | Reduced opacity (`disabled:opacity-50`) + `cursor-not-allowed` |

Focus states use a 2px amber outline (`focus-ring` utility class).

---

## 8. Motion

Motion is restrained, fast, and purposeful — never decorative.

- **Micro-interactions** (hover, focus): 80ms (`--duration-fast`)
- **State changes** (tab switch, toggle): 150ms (`--duration-normal`)
- **Panel transitions** (resize, modal): 200ms (`--duration-slow`)
- **Easing:** `cubic-bezier(0.4, 0, 0.2, 1)` everywhere

Required animations:
- Chat messages and trace rows animate in as they stream (slide-up)
- Panel resize transitions smoothly
- File tree expand/collapse transitions
- Modal open/close transitions
- Hover and active states on every interactive element

---

## 9. State Coverage

Every view must cover all three non-happy states:

| State | Requirement |
|---|---|
| **Loading** | Skeleton UI or spinner — never a blank screen |
| **Error** | Clear message + retry action — never silent failure |
| **Empty** | Deliberate, inviting design that guides the user — never a blank void |

Real-time connections (chat SSE, phone preview WS, terminal WS) must show genuine connection status indicators — never fail silently while the UI looks normal.

---

## 10. Execution Instructions for AI

1. **Analyze First**: Inspect the current screen/component code and list the existing theme tokens, state handlers, and props so nothing breaks.
2. **Step-by-Step Build**:
   - Structure layouts using Swiss grid alignment and spacing.
   - Apply Material You tonal surfaces and pill controls within that structure.
   - Apply Bento Grid only for marketing surfaces (landing, dashboard grid).
3. **Verify**: Check that every interactive element has all four states, every data view has loading/error/empty states, and Solana colors appear only in Solana-specific contexts.
4. **Output**: Clean, fully typed TypeScript (`.tsx`).
