import React from 'react'
import { StyleSheet, View } from 'react-native'
import Animated, { FadeIn } from 'react-native-reanimated'
import { useRouter } from 'expo-router'
import AsyncStorage from '@react-native-async-storage/async-storage'
import { Screen, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { getToken } from '@/lib/auth'
import { ONBOARDED_KEY } from '@/stores/app'

// Splash: mark + floor wash, then routes once: first launch tours
// onboarding, afterwards authenticated users land straight on Build and
// everyone else on sign-in. No tour replays, no signed-in user rewalks it.
export default function SplashScreen() {
  const router = useRouter()
  React.useEffect(() => {
    let alive = true
    void (async () => {
      const [seen, token] = await Promise.all([
        AsyncStorage.getItem(ONBOARDED_KEY).catch(() => null),
        getToken(),
        new Promise((resolve) => setTimeout(resolve, 900)),
      ])
      if (!alive) return
      if (seen !== '1') {
        router.replace('/onboarding/welcome')
      } else if (token) {
        router.replace('/(tabs)/build')
      } else {
        router.replace('/auth')
      }
    })()
    return () => {
      alive = false
    }
  }, [router])

  return (
    <Screen padded={false} style={styles.center}>
      {/* Floor wash: layered low-opacity mist (no gradient dep) */}
      <View style={styles.floorOuter} pointerEvents="none">
        <View style={styles.floorInner} pointerEvents="none" />
      </View>
      <Animated.View entering={FadeIn.duration(500)} style={styles.mark}>
        <View style={styles.dot} />
        <UIText variant="heading" tone="hero">
          Talon
        </UIText>
      </Animated.View>
      <Animated.View entering={FadeIn.duration(500).delay(200)}>
        <UIText variant="caption" tone="muted">
          midnight precision instrument
        </UIText>
      </Animated.View>
    </Screen>
  )
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
})
