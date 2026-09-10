import React from 'react';
import { ScrollView, StyleSheet, View } from 'react-native';
import { Badge, Card, GhostButton, Screen, SubtleCard, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

const MEMORIES = [
  { type: 'convention', text: 'Tip presets are [0.1, 0.5, 1.0] SOL — settled in plan.' },
  { type: 'decision', text: 'No QR flow for v1 — camera dep deferred.' },
  { type: 'warning', text: 'Never treat MWA base64 address as base58.' },
];

export default function MoreScreen() {
  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <View style={styles.titles}>
          <UIText variant="micro" tone="faint">
            ACCOUNT
          </UIText>
          <UIText variant="heading" tone="hero">
            More
          </UIText>
        </View>
        <Badge label="pro" tone="info" />
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {/* Usage meter: prompts + device minutes, dummy values. */}
          <Card>
            <View style={styles.meterHead}>
              <UIText variant="title" tone="hero">
                Usage
              </UIText>
              <UIText variant="monoSm" tone="muted">
                12 / 200 prompts
              </UIText>
            </View>
            <View style={styles.bar}>
              <View style={[styles.fill, { flex: 12 }]} />
              <View style={{ flex: 188 }} />
            </View>
            <UIText variant="caption" tone="muted">
              38 device minutes · resets in 21 days
            </UIText>
          </Card>

          <UIText variant="title" tone="hero">
            Memory
          </UIText>
          {MEMORIES.map((m, i) => (
            <SubtleCard key={i}>
              <UIText variant="label" tone="faint" style={styles.memType}>
                {m.type.toUpperCase()}
              </UIText>
              <UIText variant="bodySm" tone="body">
                {m.text}
              </UIText>
            </SubtleCard>
          ))}

          <Card>
            <View style={styles.export}>
              <UIText variant="title" tone="hero">
                Release export
              </UIText>
              <UIText variant="bodySm" tone="muted">
                Signed release APK plus the dApp Store submission bundle. You submit with your own publisher wallet.
              </UIText>
              <GhostButton title="Start release build" onPress={() => {}} />
              <UIText variant="caption" tone="faint">
                Publishing costs real SOL (~0.2 for fees + storage).
              </UIText>
            </View>
          </Card>

          <GhostButton title="Sign out" onPress={() => {}} />
        </View>
      </ScrollView>
    </Screen>
  );
}

const styles = StyleSheet.create({
  head: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    marginBottom: Spacing.s4,
  },
  titles: { gap: 8, flex: 1 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  meterHead: {
    flexDirection: 'row',
    alignItems: 'baseline',
    justifyContent: 'space-between',
  },
  bar: {
    flexDirection: 'row',
    height: 6,
    borderRadius: 3,
    backgroundColor: Colors.graphite,
    overflow: 'hidden',
    marginVertical: Spacing.s8,
  },
  fill: { backgroundColor: Colors.acidLime, borderRadius: 3 },
  memType: { marginBottom: 2 },
  export: { gap: Spacing.s12 },
});
