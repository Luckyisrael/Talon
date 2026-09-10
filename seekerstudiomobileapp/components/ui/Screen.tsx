import React from 'react';
import { StyleSheet, View, type ViewProps } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Colors, Spacing } from '@/constants/theme';

// Screen wraps every app screen: void canvas, safe-area top, 16px gutters.
// Design-system component (not a screen itself) — all screens compose it.
export function Screen({ children, style, padded = true, ...rest }: ViewProps & { padded?: boolean }) {
  const insets = useSafeAreaInsets();
  return (
    <View
      style={[
        styles.base,
        { paddingTop: insets.top, paddingBottom: insets.bottom },
        padded && { paddingHorizontal: Spacing.s16 },
        style,
      ]}
      {...rest}
    >
      {children}
    </View>
  );
}

const styles = StyleSheet.create({
  base: {
    flex: 1,
    backgroundColor: Colors.void,
  },
});
