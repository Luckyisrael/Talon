import React from 'react'
import { Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, Field, LimeButton, SubtleCard, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useActiveProject, useCreateProject, useProjects, useSetActiveProject } from '@/hooks'
import { memoryApi } from '@/lib/api'
import { useQuery } from '@tanstack/react-query'

// Projects: every app on this account, the mobile dashboard. Tapping one
// pins it active across Build, Diff, and Device, then returns to Build.
export default function ProjectsScreen() {
  const router = useRouter()
  const { data: projects, isLoading } = useProjects()
  const active = useActiveProject()
  const setActiveProject = useSetActiveProject()
  const createProject = useCreateProject()
  const [name, setName] = React.useState('')
  const activeId = active?.id ?? null
  const { data: memory } = useQuery({
    queryKey: ['memory', activeId],
    enabled: activeId != null,
    queryFn: () => memoryApi.get(activeId!),
  })
  const memories = memory?.memories ?? []

  async function create() {
    const trimmed = name.trim()
    if (!trimmed || createProject.isPending) return
    try {
      const p = await createProject.mutateAsync(trimmed)
      setName('')
      await setActiveProject(p.id)
      router.replace('/(tabs)/build')
    } catch {
      // The mutation surfaces the error; the row stays for a retry.
    }
  }

  return (
    <MenuScreen title="Projects">
      <View style={styles.createRow}>
        <View style={styles.createField}>
          <Field
            value={name}
            onChangeText={setName}
            placeholder="Name a new app"
            returnKeyType="done"
            onSubmitEditing={create}
          />
        </View>
        <LimeButton
          title={createProject.isPending ? '…' : 'Create'}
          disabled={!name.trim() || createProject.isPending}
          onPress={create}
          style={styles.createBtn}
        />
      </View>
      {createProject.isError && (
        <UIText variant="caption" style={{ color: Colors.coralRed }}>
          {createProject.error instanceof Error ? createProject.error.message : 'could not create the project'}
        </UIText>
      )}
      {isLoading && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          Loading projects…
        </UIText>
      )}
      {!isLoading && (projects ?? []).length === 0 && (
        <UIText variant="body" tone="muted" style={styles.empty}>
          No apps yet. Name one above to create it.
        </UIText>
      )}
      <ScrollView showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {(projects ?? []).map((p) => {
            const isActive = active?.id === p.id
            return (
              <Pressable
                key={p.id}
                accessibilityRole="button"
                onPress={async () => {
                  await setActiveProject(p.id)
                  router.replace('/(tabs)/build')
                }}
                style={({ pressed }) => [styles.row, pressed && styles.pressed]}
              >
                <View style={styles.meta}>
                  <UIText variant="body" tone="hero" numberOfLines={1}>
                    {p.name}
                  </UIText>
                  <UIText variant="caption" tone="faint" numberOfLines={1}>
                    {p.status || 'active'}
                  </UIText>
                </View>
                {isActive && <Badge label="active" tone="running" />}
              </Pressable>
            )
          })}
        </View>
      </ScrollView>
      {/* Agent memory belongs to the active project, so it lives here with
          it: what the agent knows, readable anywhere, written only by turns.
          Labeled "Memory" — the same word the IDE dock uses; "Agent memory"
          read like an internal subsystem name. */}
      {activeId != null && (
        <UIText variant="title" tone="hero" style={styles.memHead}>
          Memory
        </UIText>
      )}
      {activeId != null && memories.length === 0 && (
        <UIText variant="bodySm" tone="muted" style={styles.empty}>
          The agent has not written any memories for this project yet.
        </UIText>
      )}
      {memories.map((m) => (
        <SubtleCard key={m.id}>
          <UIText variant="label" tone="faint" style={styles.memType}>
            {m.kind.toUpperCase()}
          </UIText>
          <UIText variant="bodySm" tone="body">
            {m.text}
          </UIText>
        </SubtleCard>
      ))}
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  createRow: { flexDirection: 'row', gap: Spacing.s8 },
  createField: { flex: 1 },
  createBtn: { minWidth: 88 },
  stack: { gap: Spacing.s8, paddingBottom: Spacing.s32 },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    minHeight: 60,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: Colors.carbon,
  },
  meta: { gap: 2, flex: 1 },
  pressed: { opacity: 0.7 },
  memHead: { marginTop: Spacing.s8 },
  memType: { marginBottom: 2 },
})
