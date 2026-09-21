import React from 'react';
import { StyleSheet, View, type ViewProps } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { Colors, Spacing } from '@/constants/theme';

// Screen wraps every app screen: void canvas, safe-area top, 16px gutters.
// Design-system component (not a screen itself), all screens compose it.
// topInset defaults on (standalone screens under the notch); tab screens
// set it off because TopNav already consumes the top inset, otherwise the
// notch gap doubles and the content sits too far down.
export function Screen({ children, style, padded = true, topInset = true, ...rest }: ViewProps & { padded?: boolean; topInset?: boolean }) {
  const insets = useSafeAreaInsets();
  return (
    <View
      style={[
        styles.base,
        { paddingTop: topInset ? insets.top : 0, paddingBottom: insets.bottom },
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
