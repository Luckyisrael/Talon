import React from 'react'
import { Pressable, StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, Card, GhostButton, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useMe } from '@/hooks'
import { authApi } from '@/lib/api'
import { clearToken } from '@/lib/auth'

function formatWhen(iso?: string | null): string {
  if (!iso) return 'never used yet'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return iso
  const mins = Math.max(0, Math.round((Date.now() - t) / 60000))
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.round(hours / 24)}d ago`
}

// Profile: identity from /v1/me, the same truth the More tab reads, plus
// every live login on the account. Revoking a row ends that device; if it
// was this one, the next call lands back on sign-in through the normal
// refresh-fails flow. Sign out everywhere ends all of them, then this one.
export default function ProfileScreen() {
  const router = useRouter()
  const qc = useQueryClient()
  const { data: me, isLoading } = useMe()
  const { data: sessions } = useQuery({
    queryKey: ['sessions'],
    queryFn: () => authApi.sessions(),
  })
  const [revoking, setRevoking] = React.useState<number | null>(null)

  async function signOut() {
    await authApi.revoke().catch(() => {})
    await clearToken()
    router.replace('/auth')
  }

  async function signOutEverywhere() {
    await authApi.logoutAll().catch(() => {})
    await clearToken()
    router.replace('/auth')
  }

  async function revoke(id: number) {
    setRevoking(id)
    try {
      await authApi.revokeSession(id)
      await qc.invalidateQueries({ queryKey: ['sessions'] })
    } finally {
      setRevoking(null)
    }
  }

  return (
    <MenuScreen title="Profile">
      <Card>
        {isLoading ? (
          <UIText variant="body" tone="muted">
            Loading…
          </UIText>
        ) : (
          <View style={styles.stack}>
            <View style={styles.row}>
              <UIText variant="title" tone="hero" numberOfLines={1}>
                {me?.email ?? 'Signed out'}
              </UIText>
              <Badge label={me?.tier ?? 'free'} tone="info" />
            </View>
            <UIText variant="bodySm" tone="muted">
              {me?.display_name ? me.display_name : 'GitHub identity'}
            </UIText>
          </View>
        )}
      </Card>
      <UIText variant="title" tone="hero">
        Sessions
      </UIText>
      {(sessions ?? []).length === 0 && (
        <UIText variant="bodySm" tone="muted">
          No other logins. Each sign-in appears here with its last activity.
        </UIText>
      )}
      {(sessions ?? []).map((s) => (
        <View key={s.id} style={styles.sessionRow}>
          <View style={styles.sessionMeta}>
            <UIText variant="bodySm" tone="body" numberOfLines={1}>
              {s.device_label || 'Unknown device'}
            </UIText>
            <UIText variant="caption" tone="faint">
              active {formatWhen(s.last_used_at)}
            </UIText>
          </View>
          <Pressable
            onPress={() => revoke(s.id)}
            disabled={revoking === s.id}
            accessibilityLabel={`Revoke session ${s.id}`}
            style={styles.revokeBtn}
          >
            <UIText variant="label" style={{ color: Colors.coralRed }}>
              {revoking === s.id ? '…' : 'Revoke'}
            </UIText>
          </Pressable>
        </View>
      ))}
      {(sessions ?? []).length > 0 && <GhostButton title="Sign out everywhere" onPress={signOutEverywhere} />}
      <GhostButton title="Sign out" onPress={signOut} />
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s8 },
  row: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: Spacing.s12 },
  sessionRow: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    minHeight: 52,
    paddingHorizontal: 14,
    borderRadius: 10,
    backgroundColor: Colors.carbon,
  },
  sessionMeta: { gap: 2, flex: 1 },
  revokeBtn: { minHeight: 44, justifyContent: 'center', paddingHorizontal: 8 },
})
