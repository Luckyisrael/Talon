import React from 'react';
import { StyleSheet, View } from 'react-native';
import Animated, {
  useAnimatedStyle,
  withDelay,
  withRepeat,
  withSequence,
  withTiming,
} from 'react-native-reanimated';
import { UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

function Dot({ delay }: { delay: number }) {
  const style = useAnimatedStyle(() => ({
    opacity: withDelay(
      delay,
      withRepeat(
        withSequence(withTiming(0.25, { duration: 400 }), withTiming(1, { duration: 400 })),
        -1,
        false,
      ),
    ),
  }));
  return <Animated.View style={[styles.dot, style]} />;
}

// ThinkingRow is the agent-is-working indicator: three pulsing fog dots +
// a mono elapsed timer. No shimmer gradients (DESIGN_MOBILE rule).
export function ThinkingRow({ elapsed }: { elapsed: string }) {
  return (
    <View style={styles.row} testID="thinking-row">
      <View style={styles.dots}>
        <Dot delay={0} />
        <Dot delay={150} />
        <Dot delay={300} />
      </View>
      <UIText variant="monoSm" tone="faint">
        {elapsed}
      </UIText>
    </View>
  );
}

const styles = StyleSheet.create({
  row: { flexDirection: 'row', alignItems: 'center', gap: Spacing.s12, paddingVertical: Spacing.s8 },
  dots: { flexDirection: 'row', gap: 5 },
  dot: { width: 6, height: 6, borderRadius: 3, backgroundColor: Colors.fog },
});
