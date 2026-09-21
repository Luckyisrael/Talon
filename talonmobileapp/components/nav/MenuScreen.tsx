import React from 'react'
import { Pressable, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { Screen, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'

// MenuScreen wraps every drawer destination: back affordance, title, void
// canvas. One chrome for profile, history, settings, help, and docs so the
// side navigation reads as one place, not five one-offs.
export function MenuScreen({ title, children }: { title: string; children: React.ReactNode }) {
  const router = useRouter()
  return (
    <Screen>
      <Pressable
        onPress={() => router.back()}
        accessibilityLabel="Back"
        accessibilityRole="button"
        style={({ pressed }) => [styles.back, pressed && styles.pressed]}
      >
        <UIText variant="body" tone="faint">
          {'‹ Back'}
        </UIText>
      </Pressable>
      <UIText variant="heading" tone="hero" style={styles.title}>
        {title}
      </UIText>
      <View style={styles.body}>{children}</View>
    </Screen>
  )
}

const styles = StyleSheet.create({
  back: { alignSelf: 'flex-start', minHeight: 44, justifyContent: 'center', paddingRight: 16 },
  pressed: { opacity: 0.6 },
  title: { marginTop: Spacing.s4, marginBottom: Spacing.s16 },
  body: { flex: 1, gap: Spacing.s12 },
})
