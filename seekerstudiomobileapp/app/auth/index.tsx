import React from 'react';
import { Pressable, StyleSheet, View } from 'react-native';
import Animated, { FadeInDown } from 'react-native-reanimated';
import { useRouter } from 'expo-router';
import { AntDesign } from '@expo/vector-icons';
import { GhostButton, Screen, UIText } from '@/components/ui';
import { Colors, Layout, Radii, Spacing } from '@/constants/theme';

// Auth is dummy until the backend grows OAuth: the buttons render the real
// flow's shape (provider → consent → session) but go nowhere. No API calls.
function ProviderButton({
  label,
  mark,
  onPress,
}: {
  label: string;
  mark: React.ReactNode;
  onPress: () => void;
}) {
  return (
    <Pressable
      onPress={onPress}
      style={({ pressed }) => [styles.provider, pressed && styles.providerPressed]}
    >
      {mark}
      <UIText variant="body" tone="hero" style={styles.providerText}>
        {label}
      </UIText>
    </Pressable>
  );
}

export default function AuthScreen() {
  const router = useRouter();
  // Dummy handlers: replaced by expo-auth-session + backend OAuth exchange.
  const dummy = (provider: string) => () => {
    // eslint-disable-next-line no-console
    console.log(`[dummy] OAuth with ${provider} — backend support pending`);
  };

  return (
    <Screen>
      <View style={styles.body}>
        <Animated.View entering={FadeInDown.duration(450)}>
          <UIText variant="heading" tone="hero">
            Sign in to Seeker
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <UIText variant="body" tone="muted">
            Your builds, devices, and memory follow your account. OAuth only — no passwords to lose.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(160)} style={styles.buttons}>
          <ProviderButton
            label="Continue with GitHub"
            mark={<AntDesign name="github" size={20} color={Colors.paper} />}
            onPress={dummy('github')}
          />
          <ProviderButton
            label="Continue with Google"
            mark={
              <UIText variant="title" tone="hero" style={styles.gMark}>
                G
              </UIText>
            }
            onPress={dummy('google')}
          />
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(240)}>
          <UIText variant="caption" tone="faint">
            Nothing leaves your device until you sign in. Sessions stay on this phone.
          </UIText>
        </Animated.View>
        <View style={styles.foot}>
          {/* Guest enters the app with no session: tabs render with dummy
              data until auth lands. Still zero API calls. */}
          <GhostButton title="Explore without an account" onPress={() => router.replace('/(tabs)/build')} />
        </View>
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  body: { flex: 1, justifyContent: 'flex-end', paddingBottom: Spacing.s32, gap: Spacing.s16 },
  buttons: { gap: Spacing.s12, marginTop: Spacing.s8 },
  provider: {
    minHeight: Layout.primaryButtonHeight,
    borderRadius: Radii.buttons,
    backgroundColor: Colors.carbon,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: Colors.graphite,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    gap: Spacing.s12,
    paddingHorizontal: 16,
  },
  providerPressed: { borderColor: Colors.smoke, opacity: 0.8 },
  providerText: { fontWeight: '500' },
  gMark: { width: 20, textAlign: 'center' },
  foot: { marginTop: Spacing.s8 },
});
