# Attribution — mobile-craft

This skill is adapted from Appllama's **appllama-app-design-skill** v1.3.0
(MIT). The original is authored for iOS-primary, Claude Code / Cursor-style
developer agents; this copy is reconciled against Talon's actual stack and
device.

- **Source:** https://github.com/appllama/appllama-skills — `skills/appllama-app-design-skill/SKILL.md`
- **License:** MIT (Appllama / Antmind Ventures)
- **Vendored:** 2026-09-15, v1.3.0

## What changed from the original

- **Icons.** The original has two icon paths (SF Symbols on iOS, Material
  Symbols on Android). Talon is Android-only and ships `@expo/vector-icons`
  preinstalled, so the single source is that package and one glyph family per
  app. The Icon section is new, replacing the original's `expo-image`
  `sf:`-source guidance.
- **Native fidelity law 1.** The original points at iOS semantic colour
  objects (`Color.ios.label`). Talon resolves through `@/constants/theme`
  instead; the law is the same (semantic tokens, both themes).
- **Native fidelity law 2.** The original references iOS control names.
  Talon's equivalent is `@expo/ui` (see the `expo-ui` skill) with the
  Jetpack Compose layer on this device.
- **Lists.** The original recommends `@shopify/flash-list`. Talon's template
  and `rn-best-practices` skill standardize on `FlatList`; that wins here.
- **Simulator loop.** The original's loop is manual (Xcode Simulator,
  `xcrun simctl` recordings, frame scrubbing). Talon has the automated
  equivalent in the verify ladder — `run_boot`, `run_vision`, `run_interact` —
  and the loop's *checklist* is what this skill carries. The vision gate
  asks the craft questions the original's simulator pass asked by hand.
- **Dropped.** iOS Dynamic Island specifics, SF Symbols, `expo-av` migration
  notes, MMKV (AsyncStorage is what the template ships), and the Appllama
  MCP research tooling (the study-before-you-draw *method* is kept; the paid
  MCP is not wired into Talon's agent).

## 2026-09-21 addition — invisible details (Emil Kowalski, MIT)

- **Source:** https://github.com/emilkowalski/skills — `skills/emil-design-eng/SKILL.md` (component principles, blur-bridge, stagger, remedial order)
- **What changed.** A short closing section distilled from the 674-line
  original: press-in feedback, no-`scale(0)` entrances, trigger-anchored
  origins, blur-bridged crossfades, 30–80 ms staggers, delete-first
  remediation. Deliberately excluded: gesture physics and spring tables
  (owned by `mobile-motion`), clip-path tricks, Sonner doctrine, and all
  web-only material (WAAPI, hover media queries, Framer shorthands).
