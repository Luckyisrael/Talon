import React from 'react';
import { Pressable, StyleSheet, TextInput, View, type TextInputProps, type ViewProps } from 'react-native';
import { Colors, Layout, Radii, Spacing, Type } from '@/constants/theme';
import { UIText } from './Text';

// Card is the showcase surface: carbon fill, 12px radius, no border.
// Separation is tonal (carbon on void), never an outline, web Taste B rule.
export function Card({ children, style, ...rest }: ViewProps) {
  return (
    <View style={[styles.card, style]} {...rest}>
      {children}
    </View>
  );
}

// SubtleCard is the near-invisible nested panel: trace rows, memory entries,
// per-round log lines.
export function SubtleCard({ children, style, ...rest }: ViewProps) {
  return (
    <View style={[styles.subtle, style]} {...rest}>
      {children}
    </View>
  );
}

// Pill is for filter chips and tier badges. Taller than web (touch), same look.
export function Pill({
  label,
  active,
  onPress,
}: {
  label: string;
  active?: boolean;
  onPress?: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={!onPress}
      style={({ pressed }) => [
        styles.pill,
        active && styles.pillActive,
        pressed && styles.pillPressed,
      ]}
    >
      <UIText variant="label" tone={active ? 'hero' : 'body'}>
        {label}
      </UIText>
    </Pressable>
  );
}

type BadgeTone = 'neutral' | 'success' | 'error' | 'running' | 'info';

// Badge is inline metadata: status tags, gate states, tier labels. Running
// uses an acid-lime DOT, never a full lime fill.
export function Badge({ label, tone = 'neutral' }: { label: string; tone?: BadgeTone }) {
  const dot: Record<BadgeTone, string | null> = {
    neutral: null,
    success: Colors.pulseGreen,
    error: Colors.coralRed,
    running: Colors.acidLime,
    info: Colors.signalTeal,
  };
  const color = dot[tone];
  return (
    <View style={styles.badge}>
      {color && <View style={[styles.dot, { backgroundColor: color }]} />}
      <UIText variant="label" tone="muted">
        {label}
      </UIText>
    </View>
  );
}

// Field is the form input: 16px minimum (iOS zooms smaller on focus),
// 48px tall, mist text, graphite hairline that brightens on focus. The style
// prop layers onto the base, it never replaces it: spreading props after
// style used to wipe the text color, which is how the composer once typed
// invisible black on void.
export function Field({ style, ...props }: TextInputProps) {
  const [focused, setFocused] = React.useState(false);
  return (
    <TextInput
      placeholderTextColor={Colors.fog}
      selectionColor={Colors.acidLime}
      cursorColor={Colors.acidLime}
      onFocus={(e) => {
        setFocused(true);
        props.onFocus?.(e);
      }}
      onBlur={(e) => {
        setFocused(false);
        props.onBlur?.(e);
      }}
      style={[styles.field, focused && styles.fieldFocused, style]}
      {...props}
    />
  );
}

const styles = StyleSheet.create({
  card: {
    backgroundColor: Colors.carbon,
    borderRadius: Radii.cards,
    padding: Layout.cardPadding,
  },
  subtle: {
    backgroundColor: 'rgba(255,255,255,0.02)',
    borderRadius: Radii.buttons,
    padding: Spacing.s12,
  },
  pill: {
    borderRadius: Radii.pills,
    backgroundColor: 'rgba(255,255,255,0.05)',
    paddingHorizontal: 14,
    paddingVertical: 8,
    minHeight: Layout.minTouch,
    justifyContent: 'center',
  },
  pillActive: { backgroundColor: 'rgba(228,242,34,0.14)' },
  pillPressed: { opacity: 0.7 },
  badge: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 6,
    backgroundColor: 'rgba(255,255,255,0.05)',
    borderRadius: Radii.badges,
    paddingHorizontal: 6,
    paddingVertical: 2,
    alignSelf: 'flex-start',
  },
  dot: { width: 6, height: 6, borderRadius: 3 },
  field: {
    minHeight: Layout.primaryButtonHeight,
    borderRadius: Radii.inputs,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    backgroundColor: 'rgba(255,255,255,0.02)',
    color: Colors.mist,
    paddingHorizontal: 14,
    fontSize: 16,
  },
  fieldFocused: { borderColor: Colors.mist },
});
