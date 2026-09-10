import { Stack } from 'expo-router'
import { StatusBar } from 'expo-status-bar'
import 'react-native-reanimated'
import { AppProviders } from '@/components/app-providers'

export default function RootLayout() {
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
