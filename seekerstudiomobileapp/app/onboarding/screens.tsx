import React from 'react';
import { StyleSheet, View } from 'react-native';
import Animated, { FadeIn, FadeInDown } from 'react-native-reanimated';
import { useRouter } from 'expo-router';
import { Lamp, Layer, Wallet } from 'iconsax-react-native';
import { GhostButton, LimeButton, Screen, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';

function StepCaps({ of }: { of: string }) {
  return (
    <UIText variant="micro" tone="faint">
      {of} OF 3
    </UIText>
  );
}

const styles = StyleSheet.create({
  body: { flex: 1, justifyContent: 'flex-end', paddingBottom: Spacing.s32, gap: Spacing.s16 },
  foot: { gap: Spacing.s12, marginTop: Spacing.s8 },
  cards: { gap: Spacing.s12 },
  featureRow: { gap: 2 },
  medallion: {
    width: 72,
    height: 72,
    borderRadius: 36,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: Spacing.s16,
  },
  mockCard: {
    borderRadius: 12,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    padding: Spacing.s12,
    gap: Spacing.s8,
  },
  mockLine: { flexDirection: 'row', alignItems: 'center', gap: Spacing.s8 },
  mockDot: { width: 6, height: 6, borderRadius: 3 },
  mockBar: { height: 8, borderRadius: 4, backgroundColor: Colors.graphite, flex: 1 },
});

// Medallion: carbon circle, hairline border, fog glyph  the visual anchor
// that keeps onboarding from being buttons on void.
function Medallion({ icon }: { icon: 'welcome' | 'features' | 'connect' }) {
  const glyph = { welcome: Lamp, features: Layer, connect: Wallet } as const;
  const Glyph = glyph[icon];
  return (
    <View style={styles.medallion}>
      <Glyph size={30} color={Colors.fog} variant="Linear" />
    </View>
  );
}

// ── Screen 1: Welcome ─────────────────────────────────────────────
export function WelcomeScreen() {
  const router = useRouter();
  return (
    <Screen>
      <Animated.View entering={FadeIn.duration(400)}>
        <StepCaps of="1" />
      </Animated.View>
      <View style={styles.body}>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <Medallion icon="welcome" />
          <UIText variant="display" tone="hero">
            Build Solana apps by talking.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(160)}>
          <UIText variant="body" tone="muted">
            Describe the app. The agent plans it, writes it, and installs it on hardware you watch and approve.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(240)} style={styles.foot}>
          <LimeButton title="Continue" onPress={() => router.push('/onboarding/features')} />
        </Animated.View>
      </View>
    </Screen>
  );
}

// ── Screen 2: Features ────────────────────────────────────────────
const FEATURES = [
  { name: 'Plan', desc: 'Checklists of screens and features, settled before a line is written.' },
  { name: 'Build', desc: 'The agent writes code, verifies it, heals it. You watch the trace.' },
  { name: 'Device', desc: 'APKs install on real hardware. Boot, vision, and wallet gates run there.' },
];

export function FeaturesScreen() {
  const router = useRouter();
  return (
    <Screen>
      <StepCaps of="2" />
      <View style={styles.body}>
        <Medallion icon="features" />
        <UIText variant="heading" tone="hero">
          Three moves.
        </UIText>
        <View style={styles.cards}>
          {FEATURES.map((f, i) => (
            <Animated.View
              key={f.name}
              entering={FadeInDown.duration(400).delay(i * 90)}
              style={styles.featureRow}
            >
              <UIText variant="title" tone="hero">
                {f.name}
              </UIText>
              <UIText variant="bodySm" tone="muted">
                {f.desc}
              </UIText>
            </Animated.View>
          ))}
        </View>
        <View style={styles.foot}>
          <LimeButton title="Continue" onPress={() => router.push('/onboarding/connect')} />
          <GhostButton title="Skip" onPress={() => router.push('/onboarding/connect')} />
        </View>
      </View>
    </Screen>
  );
}

// ── Screen 3: Connect ─────────────────────────────────────────────
export function ConnectScreen() {
  const router = useRouter();
  return (
    <Screen>
      <StepCaps of="3" />
      <View style={styles.body}>
        <Medallion icon="connect" />
        <UIText variant="heading" tone="hero">
          One wallet to sign them.
        </UIText>
        {/* Product fragment: a mini approve sheet, the thing MWA shows. */}
        <View style={styles.mockCard}>
          <View style={styles.mockLine}>
            <View style={[styles.mockDot, { backgroundColor: Colors.acidLime }]} />
            <UIText variant="monoSm" tone="muted">
              approve · 0.42 SOL → 8x…3fQ2
            </UIText>
          </View>
          <View style={[styles.mockBar, { marginRight: 60 }]} />
          <View style={[styles.mockBar, { marginRight: 110 }]} />
        </View>
        <UIText variant="body" tone="muted">
          Seeker talks to the wallet app on your device through Mobile Wallet Adapter. Your keys never leave the wallet the app only ever asks it to sign.
        </UIText>
        <View style={styles.foot}>
          {/* Dummy: MWA wiring lands with the auth milestone. */}
          <LimeButton title="Connect wallet" onPress={() => router.push('/auth')} />
          <GhostButton title="Later" onPress={() => router.push('/auth')} />
        </View>
      </View>
      <UIText variant="caption" tone="faint" style={{ color: Colors.ash }}>
        Phantom, Solflare, or any MWA wallet works.
      </UIText>
    </Screen>
  );
}
