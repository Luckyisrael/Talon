import React from 'react';
import { Text as RNText, type TextProps } from 'react-native';
import { Colors, Type, type TextStyleName } from '@/constants/theme';
import { useAppFonts } from '@/lib/fonts';

// UIText renders any design-system text style. `tone` picks the color;
// `family` resolves loaded Inter (or the system fallback — never blocks).
export function UIText({
  variant = 'body',
  tone = 'body',
  style,
  children,
  ...rest
}: TextProps & {
  variant?: TextStyleName;
  tone?: 'hero' | 'body' | 'muted' | 'faint' | 'accent' | 'danger' | 'success';
}) {
  const { inter, mono } = useAppFonts();
  const colors = {
    hero: Colors.paper,
    body: Colors.mist,
    muted: Colors.fog,
    faint: Colors.ash,
    accent: Colors.acidLime,
    danger: Colors.coralRed,
    success: Colors.pulseGreen,
  } as const;
  const base = Type[variant];
  const family =
    variant === 'mono' || variant === 'monoSm'
      ? { fontFamily: mono }
      : { fontFamily: inter };
  return (
    <RNText style={[base, family, { color: colors[tone] }, style]} {...rest}>
      {children}
    </RNText>
  );
}

// Mono is the code-adjacent shortcut: IDs, hashes, token counts, timestamps.
export function Mono(props: Omit<React.ComponentProps<typeof UIText>, 'variant'>) {
  return <UIText variant="mono" tone="muted" {...props} />;
}
