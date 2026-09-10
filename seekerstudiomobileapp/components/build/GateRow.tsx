import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import { SubtleCard, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

export type GateState = 'pass' | 'fail' | 'running' | 'skipped';

const dot: Record<GateState, string> = {
  pass: Colors.pulseGreen,
  fail: Colors.coralRed,
  running: Colors.acidLime,
  skipped: Colors.fog,
};

// GateRow is one verify-gate verdict: 44px+ row, status dot, gate name,
// right-aligned mono detail. Tappable to expand the tool output in mono.
export function GateRow({
  name,
  state,
  detail,
  output,
}: {
  name: string;
  state: GateState;
  detail: string;
  output?: string;
}) {
  const [open, setOpen] = React.useState(false);
  return (
    <Pressable onPress={() => setOpen((v) => !v)}>
      <SubtleCard>
        <View style={styles.row}>
          <View style={[styles.dot, { backgroundColor: dot[state] }]} />
          <UIText variant="bodySm" tone="hero" style={styles.name}>
            {name}
          </UIText>
          <UIText variant="monoSm" tone="muted">
            {detail}
          </UIText>
        </View>
        {open && output ? (
          <UIText variant="monoSm" tone="faint" style={styles.output}>
            {output}
          </UIText>
        ) : null}
      </SubtleCard>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    minHeight: 44,
  },
  dot: { width: 8, height: 8, borderRadius: 4 },
  name: { flex: 1, fontWeight: '500' },
  output: { marginTop: Spacing.s8 },
});
