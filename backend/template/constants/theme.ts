/**
 * Design tokens — the single source of truth for the app's visual language.
 *
 * The anti-slop contract, baked into the template so every generated screen
 * starts inside a coherent system instead of inventing a fresh one per file:
 *   - ONE accent family (lime), used only for the primary action per screen
 *   - ONE neutral ramp (carbon), all surfaces and text pulled from it
 *   - ONE spacing scale, a 4pt rhythm
 *   - ONE radius scale, two values max (cards 16, controls 10)
 *   - Light + dark both defined day one; resolve via useColorScheme()
 *
 * Screens import styles from app-styles.tsx, never the raw hexes. Extending
 * means adding to this file, not a seventh inline colour.
 */

import { useColorScheme } from 'react-native'

export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 32,
} as const

export const radius = {
  sm: 10,
  md: 16,
  full: 9999,
} as const

export type Theme = {
  /** Page background — sits behind safe areas. */
  background: string
  /** Raised surface — cards, sheets, grouped rows. */
  surface: string
  /** Surface one step up — the top of a stack, or a selected row. */
  surfaceHighlight: string
  /** Hairline on the surface's own tone — one per screen, sparingly. */
  hairline: string
  /** Primary text, headings. */
  text: string
  /** Secondary text, labels, captions. */
  textSecondary: string
  /** Tertiary text, timestamps, de-emphasised numbers. */
  textTertiary: string
  /** The ONE accent. Primary action per screen, active states, focus. */
  accent: string
  /** Accent text on dark surfaces — keeps contrast legal. */
  accentText: string
  /** Success — verified states only. */
  success: string
  /** Destructive — disconnect, remove. Never decorative. */
  danger: string
}

export const dark: Theme = {
  background: '#08090a',
  surface: '#161718',
  surfaceHighlight: '#1d1f21',
  hairline: '#23252a',
  text: '#f2f3f5',
  textSecondary: '#a8b0bd',
  textTertiary: '#62666d',
  accent: '#e4f222',
  accentText: '#08090a',
  success: '#27a644',
  danger: '#eb5757',
}

export const light: Theme = {
  background: '#f7f8fa',
  surface: '#ffffff',
  surfaceHighlight: '#f0f2f5',
  hairline: '#e3e6ea',
  text: '#0d0f11',
  textSecondary: '#4c5159',
  textTertiary: '#8a8f98',
  accent: '#7a8e16',
  accentText: '#ffffff',
  success: '#1e7a34',
  danger: '#c84545',
}

/**
 * The theme the app renders in. `useColorScheme()` is the system signal;
 * there is no theme toggle to build or keep in sync.
 */
export function useTheme(): Theme {
  const scheme = useColorScheme()
  return scheme === 'light' ? light : dark
}
