import React from 'react'
import { Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useActiveProject } from '@/hooks'
import { promptsApi } from '@/lib/api'
import { useQuery } from '@tanstack/react-query'

// History: every turn on the active project, newest first. Tapping one
// returns to the build tab, where the transcript restores from the server.
export default function HistoryScreen() {
  const router = useRouter()
  const project = useActiveProject()
  const projectId = project?.id ?? null
  const { data: turns, isLoading } = useQuery({
    queryKey: ['prompts', projectId],
    enabled: projectId != null,
    queryFn: () => promptsApi.list(projectId!),
  })

  return (
    <MenuScreen title="History">
      {projectId == null && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          No project yet. Turns list here once you create one.
        </UIText>
      )}
      {projectId != null && isLoading && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          Loading turns…
        </UIText>
      )}
      {projectId != null && !isLoading && (turns ?? []).length === 0 && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          No turns yet. Send the first prompt from Build.
        </UIText>
      )}
      <ScrollView showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {(turns ?? []).map((t) => (
            <Pressable
              key={t.id}
              onPress={() => router.replace('/(tabs)/build')}
              accessibilityRole="button"
              style={({ pressed }) => [styles.row, pressed && styles.pressed]}
            >
              <View style={styles.meta}>
                <UIText variant="body" tone="body" numberOfLines={1}>
                  {t.title || `Turn #${t.id}`}
                </UIText>
                <UIText variant="caption" tone="faint">
                  {t.mode} · {t.status}
                  {t.round > 0 ? ` · round ${t.round}` : ''}
                </UIText>
              </View>
              <Badge
                label={t.status}
                tone={t.status === 'completed' ? 'success' : t.status === 'failed' ? 'error' : 'running'}
              />
            </Pressable>
          ))}
        </View>
      </ScrollView>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  stack: { gap: Spacing.s8, paddingBottom: Spacing.s32 },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    minHeight: 52,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: Colors.carbon,
  },
  meta: { gap: 2, flex: 1 },
  pressed: { opacity: 0.7 },
})
