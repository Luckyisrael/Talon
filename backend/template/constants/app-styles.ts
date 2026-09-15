/**
 * Style library — every screen's visual language, resolved from theme tokens.
 *
 * Anti-slop discipline enforced here:
 *   - fonts from ONE family, sizes from a fixed ladder, weights from {400,600,700}
 *   - numerals tabular where money or counts matter
 *   - buttons keep a 44pt+ hit area via `minHeight` + `paddingVertical`
 *   - surfaces separated by elevation and tone, not stacked hairlines
 *
 * Screens compose these; they do not reach back into theme.ts for hexes.
 */

import { StyleSheet } from 'react-native'

import { dark, radius, spacing } from '@/constants/theme'
import type { Theme } from '@/constants/theme'

/**
 * Styles are built once against the dark ramp. Light-mode is the same geometry
 * with resurfaced colours — no layout branch per theme.
 */
export function createStyles(t: Theme = dark) {
  return StyleSheet.create({
    screen: {
      flex: 1,
      backgroundColor: t.background,
      paddingHorizontal: spacing.lg,
      paddingVertical: spacing.lg,
      gap: spacing.lg,
    },

    /** A vertically stacked group — the default rhythm of every screen. */
    stack: {
      gap: spacing.md,
    },

    /** Tight grouping inside a card or between a label and its control. */
    stackTight: {
      gap: spacing.sm,
    },

    card: {
      backgroundColor: t.surface,
      borderRadius: radius.md,
      padding: spacing.lg,
      gap: spacing.sm,
    },

    /** A grouped row inside a card: label left, value or control right. */
    row: {
      flexDirection: 'row',
      alignItems: 'center',
      justifyContent: 'space-between',
      minHeight: 44,
      gap: spacing.md,
    },

    /** Inset divider — one per group max, and only where rows need separating. */
    rowSeparator: {
      height: StyleSheet.hairlineWidth,
      backgroundColor: t.hairline,
      marginVertical: spacing.xs,
    },

    title: {
      color: t.text,
      fontSize: 28,
      fontWeight: '700',
      letterSpacing: -0.5,
    },

    subtitle: {
      color: t.textSecondary,
      fontSize: 15,
      lineHeight: 22,
    },

    sectionTitle: {
      color: t.textSecondary,
      fontSize: 13,
      fontWeight: '600',
      textTransform: 'uppercase',
      letterSpacing: 0.6,
    },

    body: {
      color: t.text,
      fontSize: 16,
      lineHeight: 24,
    },

    /** Monospace for addresses, hashes, versions — anything machine-shaped. */
    mono: {
      color: t.textSecondary,
      fontSize: 14,
      lineHeight: 20,
      fontVariant: ['tabular-nums'],
    },

    /** Money and counts never shift width as digits change. */
    numeric: {
      color: t.text,
      fontSize: 17,
      fontWeight: '600',
      fontVariant: ['tabular-nums'],
    },

    label: {
      color: t.textSecondary,
      fontSize: 15,
    },

    /** Primary action — the ONE accent. One per screen. */
    buttonPrimary: {
      backgroundColor: t.accent,
      borderRadius: radius.sm,
      minHeight: 52,
      paddingHorizontal: spacing.lg,
      alignItems: 'center',
      justifyContent: 'center',
    },

    buttonPrimaryText: {
      color: t.accentText,
      fontSize: 16,
      fontWeight: '700',
    },

    /** Secondary action — quiet, never competes with the primary. */
    buttonGhost: {
      backgroundColor: 'transparent',
      borderRadius: radius.sm,
      minHeight: 52,
      paddingHorizontal: spacing.lg,
      alignItems: 'center',
      justifyContent: 'center',
      borderWidth: 1,
      borderColor: t.hairline,
    },

    buttonGhostText: {
      color: t.text,
      fontSize: 16,
      fontWeight: '600',
    },

    /** Destructive — disconnect, remove. Used only where it means it. */
    buttonDanger: {
      backgroundColor: 'transparent',
      borderRadius: radius.sm,
      minHeight: 52,
      paddingHorizontal: spacing.lg,
      alignItems: 'center',
      justifyContent: 'center',
      borderWidth: 1,
      borderColor: t.danger,
    },

    buttonDangerText: {
      color: t.danger,
      fontSize: 16,
      fontWeight: '600',
    },

    /** Pending state — every async action shows one, no exceptions. */
    pending: {
      color: t.textTertiary,
      fontSize: 15,
    },
  })
}

export type AppStyles = ReturnType<typeof createStyles>
