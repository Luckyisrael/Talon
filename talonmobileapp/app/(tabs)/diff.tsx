import React from 'react'
import { Pressable, ScrollView, StyleSheet, Text, View } from 'react-native'
import { GhostButton, Screen, UIText } from '@/components/ui'
import { Colors, Spacing, Type } from '@/constants/theme'
import { useActiveProject } from '@/hooks'
import { conversationApi } from '@/lib/api'

// Diff tab: the working tree against the provision-time template baseline,
// the same endpoint the web files view reads. One row per file with its own
// delta; tapping a row folds its hunks open inline. No boxes around rows,
// tone steps only.

type Line = { kind: 'add' | 'del' | 'hunk' | 'ctx'; text: string }

interface FileDiff {
  path: string
  add: number
  del: number
  lines: Line[]
}

function classify(line: string): Line {
  if (line.startsWith('@@')) return { kind: 'hunk', text: line }
  if (line.startsWith('+') && !line.startsWith('+++')) return { kind: 'add', text: line }
  if (line.startsWith('-') && !line.startsWith('---')) return { kind: 'del', text: line }
  return { kind: 'ctx', text: line }
}

function parseDiff(diff: string): FileDiff[] {
  const files = new Map<string, string[]>()
  let current: string | null = null
  for (const line of diff.split('\n')) {
    if (line.startsWith('+++ ')) {
      const path = line.slice(4).replace(/^b\//, '')
      if (path !== '/dev/null') {
        current = path
        if (!files.has(current)) files.set(current, [])
      }
      continue
    }
    if (current) files.get(current)!.push(line)
  }
  const out: FileDiff[] = []
  for (const [path, raw] of files) {
    const lines = raw.map(classify).filter((l) => l.text !== '' || l.kind !== 'ctx')
    let add = 0
    let del = 0
    for (const l of lines) {
      if (l.kind === 'add') add++
      else if (l.kind === 'del') del++
    }
    out.push({ path, add, del, lines })
  }
  return out
}

const lineColor = {
  add: Colors.pulseGreen,
  del: Colors.coralRed,
  hunk: Colors.signalTeal,
  ctx: Colors.fog,
} as const

export default function DiffScreen() {
  const project = useActiveProject()
  const projectId = project?.id ?? null
  const [diff, setDiff] = React.useState('')
  const [note, setNote] = React.useState<string | null>(null)
  const [loading, setLoading] = React.useState(false)
  const [expanded, setExpanded] = React.useState<string | null>(null)

  const refresh = React.useCallback(async () => {
    if (projectId == null) return
    setLoading(true)
    try {
      const res = await conversationApi.diff(projectId)
      setDiff(res.diff ?? '')
      setNote(res.note ?? null)
    } catch {
      setNote('The diff could not be loaded.')
    } finally {
      setLoading(false)
    }
  }, [projectId])

  React.useEffect(() => {
    void refresh()
  }, [refresh])

  const files = React.useMemo(() => parseDiff(diff), [diff])
  const totalAdd = files.reduce((a, f) => a + f.add, 0)
  const totalDel = files.reduce((a, f) => a + f.del, 0)

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <UIText variant="micro" tone="faint">
          {project ? project.name.toUpperCase() : 'DIFF'}
        </UIText>
        <UIText variant="heading" tone="hero">
          Changes
        </UIText>
        {files.length > 0 && (
          <UIText variant="monoSm" tone="muted">
            {files.length} {files.length === 1 ? 'file' : 'files'} ·{' '}
            <UIText variant="monoSm" style={{ color: Colors.pulseGreen }}>
              +{totalAdd}
            </UIText>{' '}
            <UIText variant="monoSm" style={{ color: Colors.coralRed }}>
              −{totalDel}
            </UIText>{' '}
            · against the template baseline
          </UIText>
        )}
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {projectId == null && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              No project yet. Create one from the Build tab.
            </UIText>
          )}
          {projectId != null && loading && files.length === 0 && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Reading the workspace changes…
            </UIText>
          )}
          {projectId != null && !loading && note && files.length === 0 && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              {note}
            </UIText>
          )}
          {projectId != null && !loading && !note && files.length === 0 && (
            <UIText variant="body" tone="muted" style={styles.empty}>
              Nothing has changed yet. Describe what to build and the changes appear here.
            </UIText>
          )}
          {files.map((f) => {
            const open = expanded === f.path
            return (
              <View key={f.path}>
                <Pressable
                  onPress={() => setExpanded(open ? null : f.path)}
                  accessibilityState={{ expanded: open }}
                  style={({ pressed }) => [styles.row, pressed && styles.pressed]}
                >
                  <Text style={styles.path} numberOfLines={1}>
                    {f.path}
                  </Text>
                  <Text style={[styles.delta, styles.deltaAdd]}>+{f.add}</Text>
                  <Text style={[styles.delta, styles.deltaDel]}>−{f.del}</Text>
                  <Text style={styles.chev}>{open ? '−' : '+'}</Text>
                </Pressable>
                {open && (
                  <View style={styles.hunks}>
                    {f.lines.map((l, i) => (
                      <Text key={i} style={[styles.line, { color: lineColor[l.kind] }]}>
                        {l.text}
                      </Text>
                    ))}
                  </View>
                )}
              </View>
            )
          })}
          {projectId != null && (
            <GhostButton title={loading ? 'Refreshing…' : 'Refresh'} onPress={refresh} disabled={loading} />
          )}
        </View>
      </ScrollView>
    </Screen>
  )
}

const styles = StyleSheet.create({
  head: { gap: 8, marginBottom: Spacing.s4 },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s8, paddingBottom: Spacing.s32 },
  empty: { textAlign: 'center', paddingVertical: Spacing.s16 },
  row: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    backgroundColor: Colors.carbon,
    borderRadius: 10,
    paddingHorizontal: 12,
    paddingVertical: 11,
  },
  pressed: { opacity: 0.7 },
  path: { flex: 1, color: Colors.paper, fontSize: Type.mono.fontSize, fontFamily: 'JetBrainsMono_400Regular' },
  delta: { fontSize: Type.monoSm.fontSize, fontFamily: 'JetBrainsMono_400Regular' },
  deltaAdd: { color: Colors.pulseGreen },
  deltaDel: { color: Colors.coralRed },
  chev: { color: Colors.ash, fontSize: Type.label.fontSize, width: 14, textAlign: 'center' },
  hunks: {
    backgroundColor: Colors.obsidian,
    borderRadius: 10,
    padding: 10,
    marginTop: 4,
  },
  line: {
    fontFamily: 'JetBrainsMono_400Regular',
    fontSize: Type.monoSm.fontSize,
    lineHeight: 17,
  },
})
