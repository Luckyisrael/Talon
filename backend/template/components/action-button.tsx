/**
 * ActionButton — the one button component this app uses.
 *
 * The platform's <Button> renders a system control whose colour, radius, and
 * hit area are not ours. Every tappable surface in a Talon app is a Pressable
 * with a 44pt+ hit area and a visible pressed state instead.
 *
 * One accent, one ghost, one danger per screen — variants are not decorative
 * choices: primary is the single next action, ghost is everything else.
 */

import { ActivityIndicator } from 'react-native'
import { Pressable, Text } from 'react-native'
import React from 'react'

import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'

type Variant = 'primary' | 'ghost' | 'danger'

export function ActionButton({
  label,
  onPress,
  variant = 'ghost',
  disabled = false,
  loading = false,
}: {
  label: string
  onPress: () => void
  variant?: Variant
  disabled?: boolean
  /** Async actions show a spinner and lock; a tap that does nothing reads broken. */
  loading?: boolean
}) {
  const t = useTheme()
  const s = createStyles(t)

  const busy = loading || disabled
  const container =
    variant === 'primary' ? s.buttonPrimary : variant === 'danger' ? s.buttonDanger : s.buttonGhost
  const text =
    variant === 'primary' ? s.buttonPrimaryText : variant === 'danger' ? s.buttonDangerText : s.buttonGhostText

  return (
    <Pressable
      onPress={onPress}
      disabled={busy}
      hitSlop={{ top: 6, bottom: 6, left: 6, right: 6 }}
      style={({ pressed }) => [container, pressed && { opacity: 0.6 }]}
      accessibilityRole="button"
      accessibilityState={{ disabled: busy, busy: loading }}
    >
      {loading ? (
        <ActivityIndicator color={variant === 'primary' ? t.accentText : t.textSecondary} />
      ) : (
        <Text style={text}>{label}</Text>
      )}
    </Pressable>
  )
}
