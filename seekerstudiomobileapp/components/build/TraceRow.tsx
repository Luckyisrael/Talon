import React from 'react';
import { StyleSheet, View } from 'react-native';
import { Ionicons } from '@expo/vector-icons';
import { SubtleCard, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

export type TraceStatus = 'running' | 'done' | 'error';

const statusColor: Record<TraceStatus, string> = {
  running: Colors.acidLime,
  done: Colors.pulseGreen,
  error: Colors.coralRed,
};

const statusIcon: Record<TraceStatus, 'ellipse' | 'checkmark-circle' | 'alert-circle'> = {
  running: 'ellipse',
  done: 'checkmark-circle',
  error: 'alert-circle',
};

// TraceRow is one tool call in the build stream: verb + target + status.
// Mirrors the web AgentTrace rows (grouped, compact, mono detail).
export function TraceRow({
  verb,
  target,
  detail,
  status,
}: {
  verb: string;
  target: string;
  detail?: string;
  status: TraceStatus;
}) {
  return (
    <SubtleCard>
      <View style={styles.row}>
        <Ionicons name={statusIcon[status]} size={16} color={statusColor[status]} />
        <UIText variant="bodySm" tone="hero" style={styles.verb}>
          {verb}
        </UIText>
        <UIText variant="bodySm" tone="muted" numberOfLines={1} style={styles.target}>
          {target}
        </UIText>
      </View>
      {detail ? (
        <UIText variant="monoSm" tone="faint" numberOfLines={2} style={styles.detail}>
          {detail}
        </UIText>
      ) : null}
    </SubtleCard>
  );
}

const styles = StyleSheet.create({
  row: { flexDirection: 'row', alignItems: 'center', gap: Spacing.s8 },
  verb: { fontWeight: '500' },
  target: { flex: 1 },
  detail: { marginTop: Spacing.s8 },
});
