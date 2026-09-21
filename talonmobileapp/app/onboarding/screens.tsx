import React from 'react'
import { StyleSheet, View } from 'react-native'
import Animated, { FadeInDown } from 'react-native-reanimated'
import { useRouter } from 'expo-router'
import { Code, Mobile, TaskSquare } from 'iconsax-react-native'
import { GhostButton, LimeButton, Screen, ShotSlot, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useAppStore } from '@/stores/app'

// Onboarding, second pass: the product shot sits in the middle, half height,
// dissolving into the void at its base. Type leads, the button closes.
// Progress is a compact segment group pinned top right. Screenshot slots are
// labeled placements with their expected size, captions below the frame.

function Progress({ step }: { step: 1 | 2 | 3 }) {
  return (
    <View style={styles.progressRow} accessibilityLabel={`Step ${step} of 3`}>
      <View style={styles.progress}>
        {[1, 2, 3].map((i) => (
          <View key={i} style={[styles.seg, i <= step && styles.segOn]} />
        ))}
      </View>
    </View>
  )
}

const styles = StyleSheet.create({
  body: { flex: 1, gap: Spacing.s16, paddingBottom: Spacing.s32 },
  progressRow: { flexDirection: 'row', justifyContent: 'flex-end' },
  progress: { flexDirection: 'row', gap: 6, width: 72 },
  seg: { flex: 1, height: 3, borderRadius: 2, backgroundColor: Colors.graphite },
  segOn: { backgroundColor: Colors.acidLime },
  middle: { flex: 1, justifyContent: 'center' },
  foot: { gap: Spacing.s12, marginTop: 'auto' },
  rows: { gap: Spacing.s16 },
  howRow: { flexDirection: 'row', gap: Spacing.s12, alignItems: 'flex-start' },
  howIcon: {
    width: 40,
    height: 40,
    borderRadius: 12,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  howText: { flex: 1, gap: 2 },
})

// Screen 1: the promise, shown before told.
export function WelcomeScreen() {
  const router = useRouter()
  return (
    <Screen>
      <Progress step={1} />
      <View style={styles.body}>
        <Animated.View entering={FadeInDown.duration(450)} style={styles.middle}>
          <ShotSlot fill shot="build-stream.png" size="1170×2532" source={require('../../assets/build-stream.png')} />
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <UIText variant="display" tone="hero">
            Build Solana apps by talking.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(160)}>
          <UIText variant="body" tone="muted">
            Describe the app. The agent plans it, writes it, and verifies it on hardware.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(240)} style={styles.foot}>
          <LimeButton title="Continue" onPress={() => router.push('/onboarding/features')} />
        </Animated.View>
      </View>
    </Screen>
  )
}

// Screen 2: how it works, three rows with icons, never numbered. The order
// on screen is the product order, no markers needed.
const HOW = [
  {
    icon: TaskSquare,
    name: 'Plan',
    desc: 'Screens and features settled into a checklist before a line is written.',
  },
  {
    icon: Code,
    name: 'Build',
    desc: 'The agent writes the code and verifies it. You watch the trace.',
  },
  {
    icon: Mobile,
    name: 'Device',
    desc: 'The dev client on your phone reloads from Metro after each turn.',
  },
]

export function FeaturesScreen() {
  const router = useRouter()
  return (
    <Screen>
      <Progress step={2} />
      <View style={styles.body}>
        <Animated.View entering={FadeInDown.duration(450)} style={styles.middle}>
          <ShotSlot fill shot="how-it-works.png" size="1170×2532" source={require('../../assets/how-it-works.png')} />
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <UIText variant="heading" tone="hero">
            Three moves.
          </UIText>
        </Animated.View>
        <View style={styles.rows}>
          {HOW.map((f, i) => {
            const Icon = f.icon
            return (
              <Animated.View key={f.name} entering={FadeInDown.duration(400).delay(140 + i * 90)} style={styles.howRow}>
                <View style={styles.howIcon}>
                  <Icon size={20} color={Colors.mist} variant="Bulk" />
                </View>
                <View style={styles.howText}>
                  <UIText variant="title" tone="hero">
                    {f.name}
                  </UIText>
                  <UIText variant="bodySm" tone="muted">
                    {f.desc}
                  </UIText>
                </View>
              </Animated.View>
            )
          })}
        </View>
        <Animated.View entering={FadeInDown.duration(400).delay(420)} style={styles.foot}>
          <LimeButton title="Continue" onPress={() => router.push('/onboarding/connect')} />
          <GhostButton title="Skip" onPress={() => router.push('/onboarding/connect')} />
        </Animated.View>
      </View>
    </Screen>
  )
}

// Screen 3: the wallet handshake. The approve sheet is shown, never drawn:
// a labeled slot holds its place until the real screenshot lands.
export function ConnectScreen() {
  const router = useRouter()
  const markOnboarded = useAppStore((s) => s.markOnboarded)
  function leave(next: '/auth') {
    void markOnboarded().finally(() => router.push(next))
  }
  return (
    <Screen>
      <Progress step={3} />
      <View style={styles.body}>
        <Animated.View entering={FadeInDown.duration(450)} style={styles.middle}>
          <ShotSlot
            fill
            shot="wallet-approve.png"
            size="1170×2532"
            source={require('../../assets/wallet-approve.png')}
          />
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <UIText variant="heading" tone="hero">
            Sign with your wallet.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(160)}>
          <UIText variant="body" tone="muted">
            Talon asks your wallet app to sign through Mobile Wallet Adapter. Keys never leave the wallet.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(240)} style={styles.foot}>
          <LimeButton title="Connect wallet" onPress={() => leave('/auth')} />
          <GhostButton title="Later" onPress={() => leave('/auth')} />
        </Animated.View>
        <UIText variant="caption" tone="faint">
          Phantom, Solflare, or any MWA wallet works.
        </UIText>
      </View>
    </Screen>
  )
}
