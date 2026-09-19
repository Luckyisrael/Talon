import { useEffect, useMemo, useState } from 'react'
import { Pressable, ScrollView, Text, View } from 'react-native'
import { conversationApi, projectsApi, type FileEntry } from '@/lib/api'
import { Colors, Type } from '@/constants/theme'
import { Card, SubtleCard } from '@/components/ui/Primitives'
import { UIText } from '@/components/ui/Text'

function splitByFile(diff: string) {
  const parts = diff.split(/^diff --git /m).filter(Boolean)
  return parts.map(p => {
    const header = p.split('\n')[0] ?? p.slice(0, 80)
    const add = (p.match(/\n\+[^+]/g) ?? []).length
    const del = (p.match(/\n-[^-]/g) ?? []).length
    return { header, add, del, raw: p }
  })
}

export function FileBrowser({ projectId }: { projectId: number | null }) {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [diff, setDiff] = useState<string>('')
  const [selected, setSelected] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const refresh = async () => {
    if (!projectId) return
    setLoading(true)
    try {
      const [f, d] = await Promise.all([
        projectsApi.getFiles(projectId).catch(() => [] as FileEntry[]),
        conversationApi.diff(projectId).catch(() => ({ diff: '' })),
      ])
      setFiles(f)
      setDiff(d.diff ?? '')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void refresh()
  }, [projectId])

  const selectedContent = useMemo(
    () => files.find(f => f.path === selected)?.content ?? null,
    [files, selected],
  )

  if (!projectId) {
    return (
      <View style={{ padding: 16 }}>
        <UIText variant="body" tone="muted">No project yet — create one from the Build tab.</UIText>
      </View>
    )
  }

  if (selected && selectedContent !== null) {
    return (
      <View style={{ flex: 1 }}>
        <Pressable
          onPress={() => setSelected(null)}
          style={{ paddingHorizontal: 16, paddingVertical: 10, flexDirection: 'row', alignItems: 'center', gap: 8 }}
        >
          <Text style={{ color: Colors.mist, fontSize: Type.label.fontSize }}>← Back</Text>
          <Text style={{ color: Colors.fog, fontSize: Type.monoSm.fontSize, flex: 1 }} numberOfLines={1}>{selected}</Text>
        </Pressable>
        <ScrollView style={{ flex: 1, backgroundColor: Colors.carbon }} contentContainerStyle={{ padding: 12 }}>
          <Text style={{ color: Colors.mist, fontFamily: 'JetBrainsMono_400Regular', fontSize: 11, lineHeight: 16 }}>
            {selectedContent}
          </Text>
        </ScrollView>
      </View>
    )
  }

  const diffFiles = diff ? splitByFile(diff) : []

  return (
    <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16, gap: 12 }}>
      <Pressable
        onPress={refresh}
        style={{ alignSelf: 'flex-start', paddingHorizontal: 10, paddingVertical: 6, borderRadius: 6, backgroundColor: Colors.graphite }}
      >
        <Text style={{ color: Colors.mist, fontSize: Type.label.fontSize }}>{loading ? 'Refreshing…' : 'Refresh'}</Text>
      </Pressable>

      <Card>
        <UIText variant="title">Files · {files.length}</UIText>
        <View style={{ height: 8 }} />
        {files.length === 0 ? (
          <UIText variant="bodySm" tone="muted">No files yet. Prompt the builder and files appear here.</UIText>
        ) : (
          <View style={{ gap: 6 }}>
            {files.slice(0, 80).map(f => (
              <Pressable
                key={f.path}
                onPress={() => setSelected(f.path)}
                style={{ paddingVertical: 8, paddingHorizontal: 10, borderRadius: 6, backgroundColor: Colors.obsidian, borderWidth: 1, borderColor: Colors.graphite }}
              >
                <Text style={{ color: Colors.paper, fontSize: Type.body.fontSize }} numberOfLines={1}>{f.path}</Text>
                <Text style={{ color: Colors.fog, fontSize: Type.monoSm.fontSize }} numberOfLines={1}>{f.content.slice(0, 80).trim() || 'empty file'}</Text>
              </Pressable>
            ))}
          </View>
        )}
      </Card>

      <SubtleCard>
        <UIText variant="title">Diff · {diffFiles.length ? `${diffFiles.length} file(s)` : 'no changes'}</UIText>
        <View style={{ height: 8 }} />
        {!diff ? (
          <UIText variant="bodySm" tone="muted">Against the template baseline — appears after the first turn finishes.</UIText>
        ) : (
          <View style={{ gap: 8 }}>
            {diffFiles.map((r, i) => (
              <View key={i} style={{ backgroundColor: Colors.carbon, borderRadius: 6, padding: 10, borderWidth: 1, borderColor: Colors.graphite }}>
                <Text style={{ color: Colors.mist, fontSize: Type.monoSm.fontSize }} numberOfLines={1}>{r.header}</Text>
                <Text style={{ color: Colors.fog, fontSize: Type.label.fontSize }}>+{r.add} −{r.del}</Text>
              </View>
            ))}
          </View>
        )}
      </SubtleCard>
    </ScrollView>
  )
}
