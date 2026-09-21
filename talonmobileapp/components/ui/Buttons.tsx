import React from 'react';
import { Pressable, StyleSheet, type PressableProps, type StyleProp, type ViewStyle } from 'react-native';
import * as Haptics from 'expo-haptics';
import { Colors, Layout, Radii } from '@/constants/theme';
import { UIText } from './Text';

type ButtonProps = Omit<PressableProps, 'style' | 'children'> & {
  title: string;
  haptic?: boolean;
  style?: StyleProp<ViewStyle>;
};

// LimeButton is the ONE chromatic action per screen: full-width, 48px,
// acid-lime fill with a 1px bottom edge (the RN translation of the web
// inset-shadow stack). Disabled renders smoke/ash, never greyed lime.
export function LimeButton({ title, disabled, haptic = true, style, ...rest }: ButtonProps) {
  return (
    <Pressable
      disabled={disabled}
      onPressIn={() => {
        if (haptic && !disabled) void Haptics.selectionAsync();
      }}
      style={({ pressed }) => [
        styles.lime,
        pressed && !disabled && styles.pressed,
        disabled && styles.disabled,
        style,
      ]}
      {...rest}
    >
      <UIText
        variant="body"
        style={[styles.limeText, disabled && styles.disabledText]}
      >
        {title}
      </UIText>
    </Pressable>
  );
}

// GhostButton is every secondary action: transparent, hairline graphite
// border, mist text. Pressed brightens the border, never fills lime.
export function GhostButton({ title, disabled, style, ...rest }: ButtonProps) {
  return (
    <Pressable
      disabled={disabled}
      style={({ pressed }) => [
        styles.ghost,
        pressed && !disabled && styles.ghostPressed,
        disabled && styles.ghostDisabled,
        style,
      ]}
      {...rest}
    >
      <UIText variant="body" tone={disabled ? 'faint' : 'body'}>
        {title}
      </UIText>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  lime: {
    minHeight: Layout.primaryButtonHeight,
    borderRadius: Radii.buttons,
    backgroundColor: Colors.acidLime,
    borderBottomWidth: 1,
    borderBottomColor: Colors.acidLimeEdge,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 16,
  },
  pressed: { opacity: 0.85 },
  disabled: { backgroundColor: Colors.smoke, borderBottomColor: Colors.smoke },
  limeText: { color: Colors.acidLimeInk, fontWeight: '500' },
  disabledText: { color: Colors.ash },
  ghost: {
    minHeight: Layout.minTouch,
    borderRadius: Radii.buttons,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 14,
  },
  ghostPressed: {
    borderColor: Colors.smoke,
    backgroundColor: 'rgba(255,255,255,0.04)',
  },
  ghostDisabled: { opacity: 0.5 },
});
