import React from 'react';
import { StyleSheet, View } from 'react-native';
import Animated, { FadeIn } from 'react-native-reanimated';
import { useRouter } from 'expo-router';
import { Screen, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

// Splash: mark + floor wash, then auto-routes to onboarding. No decisions
// here — it only covers font/splash load and hands off.
export default function SplashScreen() {
  const router = useRouter();
  React.useEffect(() => {
    const t = setTimeout(() => router.replace('/onboarding/welcome'), 1200);
    return () => clearTimeout(t);
  }, [router]);

  return (
    <Screen padded={false} style={styles.center}>
      {/* Floor wash: layered low-opacity mist (no gradient dep) */}
      <View style={styles.floorOuter} pointerEvents="none">
        <View style={styles.floorInner} pointerEvents="none" />
      </View>
      <Animated.View entering={FadeIn.duration(500)} style={styles.mark}>
        <View style={styles.dot} />
        <UIText variant="heading" tone="hero">
          Seeker
        </UIText>
      </Animated.View>
      <Animated.View entering={FadeIn.duration(500).delay(200)}>
        <UIText variant="caption" tone="muted">
          midnight precision instrument
        </UIText>
      </Animated.View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  center: {
    alignItems: 'center',
    justifyContent: 'center',
    gap: Spacing.s12,
  },
  floorOuter: {
    position: 'absolute',
    bottom: -120,
    width: 420,
    height: 420,
    borderRadius: 210,
    backgroundColor: Colors.mist,
    opacity: 0.05,
    alignItems: 'center',
    justifyContent: 'center',
  },
  floorInner: {
    width: 260,
    height: 260,
    borderRadius: 130,
    backgroundColor: Colors.mist,
    opacity: 0.5,
  },
  mark: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s12,
  },
  dot: {
    width: 14,
    height: 14,
    borderRadius: 7,
    backgroundColor: Colors.acidLime,
  },
});
