import React from 'react';
import { StyleSheet, View } from 'react-native';
import { Screen, UIText } from '@/components/ui';
import { Spacing } from '@/constants/theme';

// Placeholder for tabs whose phase hasn't landed. Shows the tab name in
// the system voice so empty routes never render blank or crash.
export function ComingSoon({ name }: { name: string }) {
  return (
    <Screen>
      <View style={styles.body}>
        <UIText variant="heading" tone="hero">
          {name}
        </UIText>
        <UIText variant="body" tone="muted">
          Lands in its phase. The tab bar is live; this screen is next.
        </UIText>
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  body: { flex: 1, justifyContent: 'center', gap: Spacing.s12 },
});
