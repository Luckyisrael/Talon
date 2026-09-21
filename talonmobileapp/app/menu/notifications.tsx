import React from 'react'
import { Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { useFocusEffect, useRouter } from 'expo-router'
import { useQueryClient } from '@tanstack/react-query'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { GhostButton, SubtleCard, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { notificationsApi } from '@/lib/api'
import { useNotifications } from '@/hooks'
import { pushAvailable, pushPermissionGranted } from '@/lib/push'

// Notifications: the in-app history of what was pushed (or would have
// been, while push stays dark). Tapping a row marks it read. Push itself
// is granted on the setup screen, linked below while permission is missing.
export default function NotificationsScreen() {
  const router = useRouter()
  const qc = useQueryClient()
  const { data: notifications, isLoading } = useNotifications()
  const [pushOn, setPushOn] = React.useState<boolean | null>(null)

  useFocusEffect(
    React.useCallback(() => {
      let alive = true
      if (!pushAvailable()) {
        setPushOn(false)
        return
      }
      void pushPermissionGranted().then((g) => {
        if (alive) setPushOn(g)
      })
      return () => {
        alive = false
      }
    }, []),
  )

  async function markRead(id: number) {
    try {
      await notificationsApi.markRead(id)
      await qc.invalidateQueries({ queryKey: ['notifications'] })
    } catch {
      // A failed stamp keeps the row unread; nothing else breaks.
    }
  }

  const rows = notifications ?? []

  return (
    <MenuScreen title="Notifications">
      {pushOn === false && (
        <View style={styles.setup}>
          <UIText variant="bodySm" tone="muted">
            {pushAvailable()
              ? 'Push is off. Turn it on to get pauses and finishes on your lock screen.'
              : 'Push is not configured on this build yet. New events still land here.'}
          </UIText>
          {pushAvailable() && <GhostButton title="Turn on push" onPress={() => router.push('/notification-setup')} />}
        </View>
      )}
      {isLoading && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          Loading…
        </UIText>
      )}
      {!isLoading && rows.length === 0 && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          Nothing yet. Input-needed pauses and build finishes land here.
        </UIText>
      )}
      <ScrollView showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {rows.map((n) => (
            <Pressable
              key={n.id}
              accessibilityRole="button"
              onPress={() => {
                if (!n.read_at) void markRead(n.id)
              }}
              style={({ pressed }) => [pressed && styles.pressed]}
            >
              <SubtleCard>
                <View style={styles.rowHead}>
                  <UIText variant="label" tone="faint" style={styles.memType}>
                    {n.kind.toUpperCase()}
                  </UIText>
                  {!n.read_at && <View style={styles.dot} />}
                </View>
                <UIText variant="bodySm" tone="body" numberOfLines={4}>
                  {n.payload}
                </UIText>
              </SubtleCard>
            </Pressable>
          ))}
        </View>
      </ScrollView>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  setup: { gap: Spacing.s8, marginBottom: Spacing.s4 },
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  stack: { gap: Spacing.s8, paddingBottom: Spacing.s32 },
  rowHead: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  memType: { marginBottom: 2 },
  dot: { width: 8, height: 8, borderRadius: 4, backgroundColor: Colors.acidLime },
  pressed: { opacity: 0.7 },
})
