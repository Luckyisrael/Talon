import React, { useEffect, useRef, useState } from 'react'
import { ActivityIndicator, StyleSheet, View } from 'react-native'
import { useRouter, useLocalSearchParams } from 'expo-router'
import { Screen, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { authApi } from '@/lib/api'
import { getToken, setRefreshToken, setToken } from '@/lib/auth'
import { registerPushWithBackend } from '@/lib/push'
import { completePendingWalletLink } from '@/lib/walletlink'

// The OAuth landing screen: the provider returned the app via
// talon://auth/callback?code=… (expo-router parses the query into
// useLocalSearchParams). This screen exchanges the single-use code for the
// JWT and routes into the app. The token never rides a URL.
export default function AuthCallbackScreen() {
  const router = useRouter()
  const params = useLocalSearchParams()
  const [phase, setPhase] = useState<'exchanging' | 'error'>('exchanging')
  const [error, setError] = useState('')
  const exchanged = useRef(false)

  useEffect(() => {
    if (exchanged.current) return
    exchanged.current = true
    const code = typeof params.code === 'string' ? params.code : ''
    if (!code) {
      setPhase('error')
      setError('The sign-in response carried no code. Start again.')
      return
    }
    // Bounded like the web callback: the single-use code lives 60s, and a
    // fetch that can neither succeed nor fail must not freeze the screen.
    const exchangeWithTimeout = Promise.race([
      authApi.exchange(code),
      new Promise<never>((_, reject) =>
        setTimeout(
          () => reject(new Error('The sign-in exchange timed out. Check the connection, then sign in again.')),
          20000,
        ),
      ),
    ])
    exchangeWithTimeout
      .then(async (data) => {
        await setToken(data.token)
        if (data.refresh_token) await setRefreshToken(data.refresh_token)
        // Read-back: SecureStore can silently fail on devices without a
        // lock screen or with a full keystore. Navigating on an unwritten
        // token is the silent sign-in loop, fail loudly instead.
        const check = await getToken()
        if (check !== data.token) {
          throw new Error(
            'the session did not persist on this device. Set a device lock screen, then sign in again.',
          )
        }
        // Bind this device for push now that the JWT exists. Fire-and-
        // forget: a failure here must never block entering the app.
        registerPushWithBackend().catch(() => {})
        // L63/FM-WALLa: a wallet proven during onboarding attaches to THIS
        // session — the proof carries the signature, the session carries
        // the identity. Fire-and-forget: a refused link (wallet taken,
        // challenge expired) never blocks entering the app; the drawer
        // reminder is the retry path.
        completePendingWalletLink().catch(() => {})
        router.replace('/(tabs)/build')
      })
      .catch((err) => {
        setPhase('error')
        setError(err instanceof Error ? err.message : 'The code exchange failed. Sign in again.')
      })
  }, [router, params])

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
            <UIText variant="body" tone="hero" style={styles.retry} onPress={() => router.replace('/auth')}>
              Back to sign-in
            </UIText>
          </>
        )}
      </View>
    </Screen>
  )
}

const styles = StyleSheet.create({
  body: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: Spacing.s16 },
  text: { textAlign: 'center', paddingHorizontal: Spacing.s24 },
  retry: { textDecorationLine: 'underline' },
})
