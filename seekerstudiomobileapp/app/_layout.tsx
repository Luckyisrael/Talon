import { useEffect } from 'react'
import { Stack } from 'expo-router'
import { StatusBar } from 'expo-status-bar'
import 'react-native-reanimated'
import { AppProviders } from '@/components/app-providers'
import { initPush } from '@/lib/push'

export default function RootLayout() {
  // Push initializes once per app start; dark (no-op) until the operator
  // sets EXPO_PUBLIC_ONESIGNAL_APP_ID. This is the only effect in the root:
  // init is idempotent and start-once, matching the fonts/auth-gate
  // exceptions — nothing here reacts to state.
  useEffect(() => {
    initPush()
  }, [])
  return (
    <AppProviders>
      <Stack>
        <Stack.Screen name="index" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/welcome" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/features" options={{ headerShown: false }} />
        <Stack.Screen name="onboarding/connect" options={{ headerShown: false }} />
        <Stack.Screen name="auth/index" options={{ headerShown: false }} />
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
      </Stack>
      <StatusBar style="light" />
    </AppProviders>
  )
}
