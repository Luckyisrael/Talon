import React from 'react'
import { Pressable, ScrollView, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, Field, LimeButton, SubtleCard, UIText } from '@/components/ui'
import { Colors, Radii, Spacing } from '@/constants/theme'
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
        {/* Two boxed tiles per row: a dashboard of apps, not a list of rows.
            space-between keeps the outer edges flush (a lone last tile stays
            left at half width), and every tile is the same height — the
            active pin sits on the tile's bottom edge either way. */}
        <View style={styles.grid}>
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
                style={({ pressed }) => [
                  styles.tile,
                  isActive && styles.tileActive,
                  pressed && styles.pressed,
                ]}
              >
                <UIText variant="title" tone="hero" numberOfLines={2}>
                  {p.name}
                </UIText>
                <UIText variant="caption" tone="faint" numberOfLines={1} style={styles.tileStatus}>
                  {p.status || 'active'}
                </UIText>
                {isActive && (
                  <View style={styles.tileFoot}>
                    <Badge label="active" tone="running" />
                  </View>
                )}
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
  grid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    justifyContent: 'space-between',
    rowGap: Spacing.s12,
    paddingBottom: Spacing.s32,
  },
  tile: {
    width: '48%',
    minHeight: 104,
    gap: 2,
    padding: 12,
    borderRadius: Radii.cards,
    backgroundColor: Colors.carbon,
  },
  // The active pin is tonal, like the active pill — carbon carries the box,
  // the lime wash says "this one is pinned", the badge says it in words.
  tileActive: { backgroundColor: 'rgba(228,242,34,0.10)' },
  tileStatus: { flexShrink: 1 },
  // marginTop: auto parks the badge on the tile's bottom edge so every tile
  // in a row reads as the same box, active or not.
  tileFoot: { marginTop: 'auto', paddingTop: Spacing.s8 },
  pressed: { opacity: 0.7 },
  memHead: { marginTop: Spacing.s8 },
  memType: { marginBottom: 2 },
})
