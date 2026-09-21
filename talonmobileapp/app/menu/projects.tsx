import React from 'react'
import { Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, Field, LimeButton, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useActiveProject, useCreateProject, useProjects, useSetActiveProject } from '@/hooks'

// Projects: every app on this account, the mobile dashboard. Tapping one
// pins it active across Build, Diff, and Device, then returns to Build.
export default function ProjectsScreen() {
  const router = useRouter()
  const { data: projects, isLoading } = useProjects()
  const active = useActiveProject()
  const setActiveProject = useSetActiveProject()
  const createProject = useCreateProject()
  const [name, setName] = React.useState('')

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
})
