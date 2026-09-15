import React, { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, StyleSheet, View } from 'react-native';
import { useRouter, useLocalSearchParams } from 'expo-router';
import { Screen, UIText } from '@/components/ui';
import { Colors, Spacing } from '@/constants/theme';
import { authApi } from '@/lib/api';
import { setToken } from '@/lib/auth';
import { registerPushWithBackend } from '@/lib/push';

// The OAuth landing screen: the provider returned the app via
// expokit://auth/callback?code=… (expo-router parses the query into
// useLocalSearchParams). This screen exchanges the single-use code for the
// JWT and routes into the app. The token never rides a URL.
export default function AuthCallbackScreen() {
  const router = useRouter();
  const params = useLocalSearchParams();
  const [phase, setPhase] = useState<'exchanging' | 'error'>('exchanging');
  const [error, setError] = useState('');
  const exchanged = useRef(false);

  useEffect(() => {
    if (exchanged.current) return;
    exchanged.current = true;
    const code = typeof params.code === 'string' ? params.code : '';
    if (!code) {
      setPhase('error');
      setError('The sign-in response carried no code — start again.');
      return;
    }
    authApi
      .exchange(code)
      .then(async (data) => {
        await setToken(data.token);
        // Bind this device for push now that the JWT exists. Fire-and-
        // forget: a failure here must never block entering the app.
        registerPushWithBackend().catch(() => {});
        router.replace('/(tabs)/build');
      })
      .catch((err) => {
        setPhase('error');
        setError(err instanceof Error ? err.message : 'The code exchange failed — sign in again.');
      });
  }, [router, params]);

  return (
    <Screen>
      <View style={styles.body}>
        {phase === 'exchanging' ? (
          <>
            <ActivityIndicator color={Colors.acidLime} />
            <UIText variant="body" tone="muted" style={styles.text}>
              Finishing sign-in…
            </UIText>
          </>
        ) : (
          <>
            <UIText variant="heading" tone="hero">
              Sign-in failed
            </UIText>
            <UIText variant="body" tone="muted" style={styles.text}>
              {error}
            </UIText>
            <UIText
              variant="body"
              tone="hero"
              style={styles.retry}
              onPress={() => router.replace('/auth')}
            >
              Back to sign-in
            </UIText>
          </>
        )}
      </View>
    </Screen>
  );
}

const styles = StyleSheet.create({
  body: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: Spacing.s16 },
  text: { textAlign: 'center', paddingHorizontal: Spacing.s24 },
  retry: { textDecorationLine: 'underline' },
});
