import { useEffect, useRef } from 'react'
import { DeviceEventEmitter } from 'react-native'
import { Stack, useRouter, useSegments } from 'expo-router'
import { StatusBar } from 'expo-status-bar'
import * as WebBrowser from 'expo-web-browser'
import 'react-native-reanimated'
import { AppProviders } from '@/components/app-providers'
import { initPush, pushAvailable, registerPushWithBackend } from '@/lib/push'

// Dismisses the OAuth session when the provider redirects back: without
// this the consent browser lingers open over the app.
WebBrowser.maybeCompleteAuthSession()

export default function RootLayout() {
  const router = useRouter()
  const segments = useSegments()
  // The guard reads segments through a ref so the listener binds once
  // (a death can arrive at any time) while always seeing the screen the
  // user is actually on — not the one at mount.
  const segmentsRef = useRef(segments)
  segmentsRef.current = segments

  // Push initializes once per app start; dark (no-op) until the operator
  // sets EXPO_PUBLIC_ONESIGNAL_APP_ID.
  useEffect(() => {
    initPush()
  }, [])

  // Re-bind the device id on every cold start. Registration used to run only
  // at the OAuth callback and the opt-in screen, so once the id moved — a
  // reinstall, cleared app data, a different app id baked into the build —
  // the backend kept the dead one forever and every push targeted a device
  // OneSignal no longer knew (the stored id answered "No player with this id
  // found" while the live phone had a different one). SetOneSignalID is
  // idempotent and registerPushWithBackend is a courtesy that never blocks:
  // the same seam auth/callback uses, just not only after a sign-in.
  // The delay gives OneSignal a beat to attach the id before it is read.
  useEffect(() => {
    if (!pushAvailable()) return
    const t = setTimeout(() => {
      void registerPushWithBackend().catch(() => {})
    }, 2_000)
    return () => clearTimeout(t)
  }, [])

  // L67 / FM-SESSa — the app's single auth-death observer. The transport
  // announces `auth:expired` when a refresh dies (after clearToken, once
  // per session); this is the ONE place that acts on it, so a death lands
  // sign-in from any screen instead of stranding the user where every
  // request now fails — before L67 the rescue was scattered across
  // splash / auth self-heal / settings, each screen only saving itself.
  // The guard: the auth stack announcing its own death must not stack a
  // second navigation. Web's handleExpiredSession hard-redirect stays
  // the model, untouched.
  useEffect(() => {
    const sub = DeviceEventEmitter.addListener('auth:expired', () => {
      if (segmentsRef.current[0] !== 'auth') {
        router.replace('/auth')
      }
    })
    return () => sub.remove()
  }, [router])
  return (
    <AppProviders>
      <Stack>
        <Stack.Screen name="index" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/welcome" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/features" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/connect" options={{ headerShown: false }} />
        <Stack.Screen name="auth/index" options={{ headerShown: false }} />
        {/* auth/callback receives the talon://auth/callback deep link carrying
           the single-use OAuth code. It must be declared here or expo-router
           cannot resolve the scheme to the screen: the link falls through to
           the splash, which sees no token and routes to sign-in, the code is
           discarded, and the browser is left hanging — the exact
           "sign-in succeeds but never returns to the app" symptom. */}
        <Stack.Screen name="auth/callback" options={{ headerShown: false }} />
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="menu/profile" options={{ headerShown: false }} />
        <Stack.Screen name="menu/projects" options={{ headerShown: false }} />
        <Stack.Screen name="menu/upgrade" options={{ headerShown: false }} />
        <Stack.Screen name="menu/history" options={{ headerShown: false }} />
        <Stack.Screen name="menu/notifications" options={{ headerShown: false }} />
        <Stack.Screen name="notification-setup" options={{ headerShown: false }} />
        <Stack.Screen name="menu/settings" options={{ headerShown: false }} />
        <Stack.Screen name="menu/help" options={{ headerShown: false }} />
        <Stack.Screen name="menu/docs" options={{ headerShown: false }} />
      </Stack>
      <StatusBar style="light" />
    </AppProviders>
  )
}
