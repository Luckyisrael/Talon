import React, { useEffect, useState } from 'react';
import { ActivityIndicator, Platform, Pressable, ScrollView, StyleSheet, View } from 'react-native';
import Animated, { FadeInDown } from 'react-native-reanimated';
import { useRouter } from 'expo-router';
import * as WebBrowser from 'expo-web-browser';
import Constants from 'expo-constants';
import { makeRedirectUri } from 'expo-auth-session';
import { AntDesign } from '@expo/vector-icons';
import { GhostButton, Screen, ShotSlot, UIText } from '@/components/ui';
import { Colors, Layout, Radii, Spacing } from '@/constants/theme';
import { authApi, meApi } from '@/lib/api';
import { getToken } from '@/lib/auth';

// The real OAuth flow: expo-web-browser opens the backend's provider start
// URL with the talon:// redirect. The backend 302s to the provider, the
// provider lands back on talon://auth/callback?code=…, and the app
// exchanges the single-use code for the JWT, the token never rides a URL.
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
  // A failed fetch is its own state, not an empty list: disabled buttons
  // with no explanation read as a broken app.
  const [loadError, setLoadError] = React.useState(false);
  useEffect(() => {
    let alive = true;
    // Self-heal: a valid token stranded on this screen (deep link raced
    // navigation, stale route) goes straight in. A dead token stays put.
    getToken().then((t) => {
      if (!alive || !t) return;
      meApi
        .get()
        .then(() => {
          if (alive) router.replace('/(tabs)/build');
        })
        .catch(() => {});
    });
    // Android warms the browser so consent opens instantly; cooled on exit.
    if (Platform.OS === 'android') {
      void WebBrowser.warmUpAsync();
    }
    authApi
      .providers()
      .then((data) => alive && setProviders(data.providers ?? []))
      .catch(() => {
        if (!alive) return;
        setProviders([]);
        setLoadError(true);
      });
    return () => {
      alive = false;
      if (Platform.OS === 'android') {
        void WebBrowser.coolDownAsync();
      }
    };
  }, [router]);

  async function signIn(providerID: string) {
    setBusy(true);
    setError('');
    try {
      // The redirect is built, not hardcoded: makeRedirectUri resolves the
      // right scheme per environment, and the backend echoes it back after
      // consent (same string opens the session and receives it). The
      // provider query rides along so the callback knows who answered.
      const redirect = makeRedirectUri({
        scheme: 'talon',
        path: 'auth/callback',
        queryParams: { provider: providerID },
      });
      const result = await WebBrowser.openAuthSessionAsync(
        authApi.startURL(providerID, redirect),
        redirect,
      );
      if (result.type === 'cancel' || result.type === 'dismiss') {
        // The user backed out of consent, not an error worth showing.
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
  // OAuth returns through the talon:// scheme, which only exists in dev and
  // production builds. Inside Expo Go the hop dies in the browser: say so
  // instead of a silent dead end.
  const inExpoGo = Constants.appOwnership === 'expo';

  return (
    <Screen>
      <ScrollView showsVerticalScrollIndicator={false} contentContainerStyle={styles.body}>
        <Animated.View entering={FadeInDown.duration(450)} style={styles.middle}>
          <ShotSlot
            fill
            shot="auth-devices.png"
            size="1170×2532"
            source={require('../../assets/auth-devices.png')}
          />
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(80)}>
          <UIText variant="heading" tone="hero">
            Sign in to Talon
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(160)}>
          <UIText variant="body" tone="muted">
            Your builds, devices, and memory follow your account. OAuth only. No passwords to lose.
          </UIText>
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(200)} style={styles.buttons}>
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
              {loadError
                ? 'Could not reach the server. Check the API URL in Settings, then retry.'
                : 'No providers configured. An operator must set AUTH_GITHUB_CLIENT_ID/SECRET or AUTH_GOOGLE_CLIENT_ID/SECRET.'}
            </UIText>
          )}
          {!!error && (
            <UIText variant="caption" tone="muted" style={styles.errorText}>
              {error}
            </UIText>
          )}
          {busy && (
            <View style={styles.busyRow}>
              <ActivityIndicator color={Colors.acidLime} />
              <UIText variant="caption" tone="muted" style={styles.errorText}>
                Finish signing in, in the browser. You return here automatically.
              </UIText>
            </View>
          )}
          {inExpoGo && (
            <UIText variant="caption" tone="muted" style={styles.errorText}>
              Expo Go cannot return from sign-in. Install the Talon dev build instead.
            </UIText>
          )}
        </Animated.View>
        <Animated.View entering={FadeInDown.duration(450).delay(280)}>
          <UIText variant="caption" tone="faint">
            Nothing leaves your device until you sign in. Sessions stay on this phone.
          </UIText>
        </Animated.View>
        <View style={styles.foot}>
          {/* Guest enters the app with no session: tabs render signed-out
              states and the first real action routes here. */}
          <GhostButton title="Explore without an account" onPress={() => router.replace('/(tabs)/build')} />
        </View>
      </ScrollView>
    </Screen>
  );
}

const styles = StyleSheet.create({
  body: { flexGrow: 1, gap: Spacing.s16, paddingBottom: Spacing.s32 },
  middle: { flex: 1, justifyContent: 'center' },
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
  foot: { marginTop: 'auto', paddingTop: Spacing.s8 },
  errorText: { textAlign: 'center' },
  busyRow: { flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: Spacing.s8 },
});
