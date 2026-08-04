# SeekerStudio — Frontend Design System (Material You + Bento Grid + Swiss Minimalism)

> **Scope:** This is the mandatory rule set for building **any UI** in the SeekerStudio
> frontend (Next.js / React + TypeScript + Tailwind). Every screen, component, and
> visual element must converge these three systems.
>
> **Adaptation note:** The source rules were written for React Native. In our Next.js
> web frontend the React Native primitives map as follows:
> `View → div`, `Text → span/p`, `Pressable → button`, `ScrollView → div(overflow)`,
> `FlatList/SectionList → CSS grid + virtualized list`, `SafeAreaView → safe-area env()`,
> `StyleSheet.hairlineWidth → 1px`. Touch-target 44×44pt → pointer target 44×44px.
> All other rules apply verbatim.

---

# Role & Objective

You are an expert Next.js developer and UI/UX Architect. Your task is to build and
refactor all application screens into a cohesive hybrid design system that combines:

1. **Google Material You (Material 3)**: Soft tonal color blending, pill-shaped chips/buttons, dynamic surface elevations, and accessible contrast.
2. **Bento Grid Architecture**: Asymmetric modular card layouts with clear hierarchy, scannability, and varied aspect ratios tailored for mobile viewports.
3. **Swiss Minimalist Typography**: Disciplined geometric grid alignment, high-contrast typography, strict mathematical spacing scales (Major Second 1.125x / Major Third 1.25x), and crisp hairline dividers.

---

### ⚠️ Critical Constraints & Theme Preservation

- **PRESERVE EXISTING THEME & TOKENS**: Do NOT replace the existing brand colors, font families, or design system configuration files. Instead, adapt current theme tokens into Material You tonal palettes and Swiss spacing scales.
- **FRONTEND STACK CONSTRAINTS**:
  - Use React + TypeScript with Tailwind CSS and the existing theme configuration.
  - All layouts must be mobile-first and fully responsive across breakpoints, respecting safe areas and bottom navigation bars (`env(safe-area-inset-*)`).
  - Maintain a minimum **44x44 px pointer target** for every interactive element (`button`, chips, toggles, links).
- **PERFORMANCE FIRST**:
  - Use CSS grid / virtualized lists for scrollable Bento grids to sustain 60 FPS scrolling.
  - Avoid deeply nested element trees; flatten containers using spacing and subtle dividers rather than heavy drop shadows.

---

### 🏛️ 3-Pillar Design System Rules

#### 1. Google Material You (Blended Tonal Chroma)
- **Tonal Backgrounds & Surfaces**: Use the app's primary brand color to generate soft tonal background tints (`8%` to `12%` opacity) for secondary cards and header sections.
- **Pill Controls**: Action buttons, filter tabs, and status badges must use fully rounded pill shapes (`borderRadius: 999` / `rounded-full`).
- **Surface Elevation**: Replace hard black shadows with subtle, warm tonal shadows or elevation layers (`shadow-sm`/`elevation: 2`).

#### 2. Mobile Bento Grid (Asymmetric Modular Hierarchy)
- **Responsive Grid**: Use a 2-column (mobile) / 12-column (desktop) grid system.
  - **Hero Tile (Full Width - 2 Cols)**: Primary metric, featured action, or live status in a prominent top card (full width).
  - **Secondary Tiles (1 Col x 1 Col or 1 Col x 2 Col)**: Side-by-side squares and vertical tiles for secondary controls, quick stats, and toggles.
- **Card Styling**: Clean corner radii (`20–24px` outer cards, `14px` inner nested items). Always obey the corner nesting rule: `Inner Radius = Outer Radius - Container Padding`.

#### 3. Swiss Minimalist Typography & Precision Spacing
- **Editorial Hierarchy**: Clear visual contrast between numbers/headings and labels. Never skip heading levels.
- Use **monospace or uppercase tracked-out labels** (`letterSpacing: 1.2px`) for metadata and card headers.
- **Mathematical Padding**: Standardize container padding to `16px` or `20px`. Outer container margin equals or exceeds inner spacing between child elements.
- **Crisp Hairlines**: Use `1px` borders with subtle neutral tints (`rgba(150, 150, 150, 0.18)`) to separate sections without visual noise.

---

### 🛠️ Execution Instructions for AI

1. **Analyze First**: Inspect the current screen/component code and list the existing theme tokens, state handlers, and props so nothing breaks.
2. **Step-by-Step Build**:
   - Structure layouts as a responsive mobile Bento Grid.
   - Apply Swiss Minimalist typography & spacing.
   - Apply Material You tonal surface colors and pill controls using the existing theme configuration.
3. **Output**: Clean, fully typed TypeScript (`.tsx`) with inline comments explaining where the three systems (Material You, Bento Grid, Swiss Minimalism) converge.
