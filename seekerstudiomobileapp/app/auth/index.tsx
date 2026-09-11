import React, { useEffect, useState } from 'react';
import { ActivityIndicator, Pressable, StyleSheet, View } from 'react-native';
import Animated, { FadeInDown } from 'react-native-reanimated';
import { useRouter } from 'expo-router';
import * as WebBrowser from 'expo-web-browser';
import { AntDesign } from '@expo/vector-icons';
import { GhostButton, Screen, UIText } from '@/components/ui';
import { Colors, Layout, Radii, Spacing } from '@/constants/theme';
import { authApi } from '@/lib/api';

// The real OAuth flow: expo-web-browser opens the backend's provider start
// URL with the expokit:// redirect. The backend 302s to the provider, the
// provider lands back on expokit://auth/callback?code=…, and the app
// exchanges the single-use code for the JWT — the token never rides a URL.
//
// The provider list is the server's honest answer: a provider without
// credentials renders disabled with its reason, never a dead button.

function ProviderButton({
  label,
  mark,
  onPress,
  disabled,
  note,
}: {
  label: string;
  mark: React.ReactNode;
  onPress: () => void;
  disabled?: boolean;
  note?: string;
}) {
  return (
    <Pressable
      onPress={onPress}
      disabled={disabled}
      style={({ pressed }) => [styles.provider, pressed && !disabled && styles.providerPressed, disabled && styles.providerOff]}
    >
      {mark}
      <View style={styles.providerTextWrap}>
        <UIText variant="body" tone={disabled ? 'muted' : 'hero'} style={styles.providerText}>
          {label}
        </UIText>
        {!!note && (
          <UIText variant="caption" tone="faint">
            {note}
          </UIText>
        )}
      </View>
    </Pressable>
  );
}

export default function AuthScreen() {
  const router = useRouter();
  const [providers, setProviders] = useState<{ id: string; name: string }[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  // One fetch on mount: the honest provider list gates every button.
  useEffect(() => {
    let alive = true;
    authApi
      .providers()
      .then((data) => alive && setProviders(data.providers ?? []))
      .catch(() => alive && setProviders([]));
    return () => {
      alive = false;
    };
  }, []);

  async function signIn(providerID: string) {
    setBusy(true);
    setError('');
    try {
      // The redirect lands on expokit://auth/callback with the code; the
      // callback screen does the exchange.
      const result = await WebBrowser.openAuthSessionAsync(
        authApi.startURL(providerID, 'expokit://auth/callback?provider=' + providerID),
        'expokit://auth/callback',
      );
      if (result.type === 'cancel') {
        // The user backed out of consent — not an error worth showing.
        return;
      }
      // 'success' means the browser returned to the app; the callback
      // screen handles the code. Nothing to do here.
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not open the sign-in browser.');
    } finally {
      setBusy(false);
    }
  }

  const allProviders = [
    { id: 'github', name: 'GitHub' },
    { id: 'google', name: 'Google' },
  ];
  const isOn = (id: string) => providers?.some((p) => p.id === id) ?? false;

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
          {allProviders.map((p) => {
            const configured = providers === null ? null : isOn(p.id);
            return (
              <ProviderButton
                key={p.id}
                label={`Continue with ${p.name}`}
                mark={
                  p.id === 'github' ? (
                    <AntDesign name="github" size={20} color={configured === false ? Colors.ash : Colors.paper} />
                  ) : (
                    <UIText variant="title" tone={configured === false ? 'muted' : 'hero'} style={styles.gMark}>
                      G
                    </UIText>
                  )
                }
                disabled={configured !== true || busy}
                note={configured === false ? 'not configured on this server' : undefined}
                onPress={() => signIn(p.id)}
              />
            );
          })}
          {providers !== null && providers.length === 0 && (
            <UIText variant="caption" tone="muted" style={styles.errorText}>
              No providers configured. An operator must set AUTH_GITHUB_CLIENT_ID/SECRET or AUTH_GOOGLE_CLIENT_ID/SECRET.
            </UIText>
          )}
          {!!error && (
            <UIText variant="caption" tone="muted" style={styles.errorText}>
              {error}
            </UIText>
          )}
          {busy && <ActivityIndicator color={Colors.acidLime} style={styles.spinner} />}
        </Animated.View>
 <Animated.View entering={FadeInDown.duration(450).delay(240)}>
          <UIText variant="caption" tone="faint">
            Nothing leaves your device until you sign in. Sessions stay on this phone.
          </UIText>
        </Animated.View>
        <View style={styles.foot}>
          {/* Guest enters the app with no session: tabs render signed-out
              states and the first real action routes here. */}
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
  providerOff: { opacity: 0.45 },
  providerTextWrap: { alignItems: 'center' },
  providerText: { fontWeight: '500' },
  gMark: { width: 20, textAlign: 'center' },
  foot: { marginTop: Spacing.s8 },
  errorText: { textAlign: 'center' },
  spinner: { marginTop: 4 },
});
