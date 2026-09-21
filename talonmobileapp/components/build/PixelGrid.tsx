import React from 'react';
import { StyleSheet, View } from 'react-native';
import Animated, {
  Easing,
  useAnimatedStyle,
  withDelay,
  withRepeat,
  withSequence,
  withTiming,
} from 'react-native-reanimated';
import { UIText } from '@/components/ui';
import { Colors } from '@/constants/theme';

// PixelGrid is the LoadingState replica (web: components/ui/LoadingState):
// a 3x3 grid of 4px cells, fog-dim at rest, flashing acid lime as the
// chevron wavefront passes. Drive = square cells, Dots = round cells,
// Orbit = comet lapping the perimeter. Paired with a pulsing label (RN has
// no bg-clip-text shimmer) and a live 0.1s mono timer.
export type PixelVariant = 'Drive' | 'Dots' | 'Orbit';

const CHEVRON = Array.from({ length: 9 }, (_, i) => {
  const r = Math.floor(i / 3);
  const c = i % 3;
  return (c + Math.abs(r - 1)) * 90;
});

const ORBIT_ORDER = [0, 1, 2, 5, 8, 7, 6, 3];
const ORBIT = Array.from({ length: 9 }, (_, i) => {
  const k = ORBIT_ORDER.indexOf(i);
  return k === -1 ? null : k * 110;
});

const CELL = 4;
const GAP = 1.5;

function PixelCell({ delay, dur, round }: { delay: number | null; dur: number; round: boolean }) {
  // Two layers: static fog-dim base + lime overlay flashing opacity as the
  // wavefront passes, the web's fog-rest/lime-flash read, exactly.
  const flash = useAnimatedStyle(() => {
    if (delay === null) return { opacity: 0 };
    return {
      opacity: withDelay(
        delay,
        withRepeat(
          withSequence(
            withTiming(1, { duration: dur / 2, easing: Easing.inOut(Easing.ease) }),
            withTiming(0, { duration: dur / 2, easing: Easing.inOut(Easing.ease) }),
          ),
          -1,
          false,
        ),
      ),
    };
  }, [delay, dur]);
  const shape = round ? styles.round : styles.square;
  return (
    <View style={[{ width: CELL, height: CELL, backgroundColor: Colors.fog, opacity: 0.15 }, shape]}>
      <Animated.View
        style={[{ width: CELL, height: CELL, backgroundColor: Colors.acidLime }, shape, flash]}
      />
    </View>
  );
}

function PixelLabel({ label }: { label: string }) {
  const style = useAnimatedStyle(() => ({
    opacity: withRepeat(withTiming(0.45, { duration: 700, easing: Easing.inOut(Easing.ease) }), -1, true),
  }));
  return (
    <Animated.View style={style}>
      <UIText variant="bodySm" tone="body" style={styles.label}>
        {label}
      </UIText>
    </Animated.View>
  );
}

function useElapsed(): string {
  const [ds, setDs] = React.useState(0);
  React.useEffect(() => {
    const t = setInterval(() => setDs((d) => d + 1), 100);
    return () => clearInterval(t);
  }, []);
  const total = ds / 10;
  if (total < 60) return `${total.toFixed(1)}s`;
  return `${Math.floor(total / 60)}m ${(total % 60).toFixed(1)}s`;
}

export function PixelGrid({ label, variant = 'Drive' }: { label?: string; variant?: PixelVariant }) {
  const delays = variant === 'Orbit' ? ORBIT : CHEVRON;
  const dur = variant === 'Orbit' ? 950 : 650;
  const round = variant === 'Dots';
  const elapsed = useElapsed();
  return (
    <View style={styles.row} testID="pixel-grid">
      <View style={styles.grid}>
        {delays.map((delay, i) => (
          <PixelCell key={i} delay={delay} dur={dur} round={round} />
        ))}
      </View>
      {label ? <PixelLabel label={label} /> : null}
      <UIText variant="monoSm" tone="faint">
        {elapsed}
      </UIText>
    </View>
  );
}

const styles = StyleSheet.create({
  row: { flexDirection: 'row', alignItems: 'center', gap: 10 },
  grid: { width: CELL * 3 + GAP * 2, flexDirection: 'row', flexWrap: 'wrap', gap: GAP },
  square: { borderRadius: 1 },
  round: { borderRadius: 2 },
  label: { fontWeight: '500' },
});
