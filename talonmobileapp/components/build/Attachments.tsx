import React from 'react'
import { Modal, Pressable, StyleSheet, View } from 'react-native'
import { Image } from 'expo-image'
import { UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { attachmentsApi } from '@/lib/api'
import type { LocalAttachment, SentAttachment } from '@/lib/attachments'

// Attachment chips, mobile side. StagedAttachments renders composer files
// (local URIs need no auth); TurnAttachments renders a sent turn, loading
// bytes through the authed endpoint into expo-image. Tapping an image opens
// it full-screen. Docs always render as name chips, their content never
// expands inline.

export function StagedAttachments({
  items,
  onRemove,
  error,
}: {
  items: LocalAttachment[]
  onRemove: (key: string) => void
  error: string | null
}) {
  if (items.length === 0 && !error) return null
  return (
    <View style={styles.row}>
      {items.map((a) => (
        <View key={a.key} style={styles.chip}>
          {a.kind === 'image' ? (
            <Image source={{ uri: a.uri }} style={styles.thumb} contentFit="cover" />
          ) : (
            <View style={styles.docBadge}>
              <UIText variant="monoSm" tone="muted">
                MD
              </UIText>
            </View>
          )}
          <View style={styles.meta}>
            <UIText variant="label" tone="body" numberOfLines={1}>
              {a.name}
            </UIText>
            <UIText variant="monoSm" tone="faint">
              {a.sizeLabel}
            </UIText>
          </View>
          <Pressable onPress={() => onRemove(a.key)} accessibilityLabel={`Remove ${a.name}`} style={styles.x}>
            <UIText variant="label" tone="hero">
              ×
            </UIText>
          </Pressable>
        </View>
      ))}
      {error ? (
        <UIText variant="caption" tone="danger" style={styles.err}>
          {error}
        </UIText>
      ) : null}
    </View>
  )
}

function RemoteThumb({ projectId, item }: { projectId: number; item: SentAttachment }) {
  const [source, setSource] = React.useState<{ uri: string; headers: Record<string, string> } | null>(null)
  const [failed, setFailed] = React.useState(false)
  const [zoomed, setZoomed] = React.useState(false)

  React.useEffect(() => {
    let alive = true
    attachmentsApi
      .imageSource(projectId, item.id)
      .then((s) => {
        if (alive) setSource(s)
      })
      .catch(() => {
        if (alive) setFailed(true)
      })
    return () => {
      alive = false
    }
  }, [projectId, item.id])

  if (failed || !source) {
    return (
      <View style={styles.docBadge}>
        <UIText variant="monoSm" tone="muted">
          IMG
        </UIText>
      </View>
    )
  }
  return (
    <>
      <Pressable onPress={() => setZoomed(true)} accessibilityLabel={`View ${item.name}`}>
        <Image source={source} style={styles.thumb} contentFit="cover" />
      </Pressable>
      <Modal visible={zoomed} transparent animationType="fade" onRequestClose={() => setZoomed(false)}>
        <Pressable style={styles.zoomBack} onPress={() => setZoomed(false)}>
          <Image source={source} style={styles.zoomImg} contentFit="contain" />
        </Pressable>
      </Modal>
    </>
  )
}

export function TurnAttachments({ projectId, items }: { projectId: number; items: SentAttachment[] }) {
  if (items.length === 0) return null
  return (
    <View style={styles.row}>
      {items.map((a) => (
        <View key={a.id} style={[styles.chip, styles.chipReadonly]}>
          {a.mime.startsWith('image/') ? (
            <RemoteThumb projectId={projectId} item={a} />
          ) : (
            <View style={styles.docBadge}>
              <UIText variant="monoSm" tone="muted">
                MD
              </UIText>
            </View>
          )}
          <View style={styles.meta}>
            <UIText variant="label" tone="body" numberOfLines={1}>
              {a.name}
            </UIText>
          </View>
        </View>
      ))}
    </View>
  )
}

const styles = StyleSheet.create({
  row: { flexDirection: 'row', flexWrap: 'wrap', gap: Spacing.s8 },
  chip: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    backgroundColor: Colors.obsidian,
    borderRadius: 10,
    padding: 6,
    maxWidth: '100%',
  },
  chipReadonly: { paddingRight: 10 },
  thumb: { width: 56, height: 56, borderRadius: 6 },
  docBadge: {
    width: 56,
    height: 56,
    borderRadius: 6,
    backgroundColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  meta: { gap: 2, minWidth: 0, maxWidth: 160 },
  x: {
    width: 24,
    height: 24,
    borderRadius: 12,
    backgroundColor: 'rgba(0,0,0,0.6)',
    alignItems: 'center',
    justifyContent: 'center',
  },
  err: { alignSelf: 'center' },
  zoomBack: {
    flex: 1,
    backgroundColor: 'rgba(0,0,0,0.9)',
    alignItems: 'center',
    justifyContent: 'center',
    padding: Spacing.s16,
  },
  zoomImg: { width: '100%', height: '80%' },
})
