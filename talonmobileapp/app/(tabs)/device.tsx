import React from 'react'
import { Linking, Pressable, ScrollView, StyleSheet, View } from 'react-native'
import Clipboard from '@react-native-clipboard/clipboard'
import QRCodeStyled from 'react-native-qrcode-styled'
import { Badge, LimeButton, Screen, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { useActiveProject, useCreateSession, useDestroySession, useDeviceSession, useLiveSession, useMetro, useTestInfo } from '@/hooks'
import { getSessionId, setSessionId } from '@/lib/session'
import { downloadAndInstallDevClient } from '@/lib/devclient'
import { type MetroEvent } from '@/lib/api'

// Device tab: Metro-first connection details for the phone in your hand.
// This phone runs the installed dev client and reloads from Metro, so there
// is no streamed preview here and no phone mockup. What this tab shows is
// what helps: Metro state, the session behind the relay, the deep link with
// copy and open actions, the APK for first installs, the pairing QR for
// other phones, and the recent Metro events. Every value comes from the
// backend.

function metroLabel(metro: MetroEvent | null): string {
  if (!metro) return 'metro idle'
  switch (metro.kind) {
    case 'bundle_progress':
      return `bundling ${metro.progress}%`
    case 'bundle_start':
      return 'bundling…'
    case 'bundle_done':
      return 'bundle ready'
    case 'reloaded':
      return 'reloaded'
    case 'error':
      return metro.text.slice(0, 40) || 'bundler error'
    default:
      return metro.text.slice(0, 30) || metro.kind
  }
}

export default function DeviceScreen() {
  const project = useActiveProject()
  const projectId = project?.id ?? null
  const createSession = useCreateSession()
  const destroySession = useDestroySession()

  // The session the device tab shows. The server is the source of truth, not
  // this device's SecureStore: a session started from the web IDE holds an id
  // that never reached this phone, and without asking the backend the tab
  // would report "no session" while a device is running. Local storage only
  // short-circuits the first paint; the live query then corrects it.
  const [sessionId, setLocalSessionId] = React.useState<number | null>(null)
  React.useEffect(() => {
    let alive = true
    if (projectId != null) {
      getSessionId(projectId).then((id) => {
        if (alive && id != null) setLocalSessionId(id)
      })
    }
    return () => {
      alive = false
    }
  }, [projectId])

  // Ask the server what is actually running. The id discovered here replaces
  // the local one, and is persisted so the next cold start opens on it.
  useLiveSession(projectId, (live) => {
    if (live == null) {
      setLocalSessionId(null)
      if (projectId != null) void setSessionId(projectId, null)
    } else if (live.id !== sessionId) {
      setLocalSessionId(live.id)
      if (projectId != null) void setSessionId(projectId, live.id)
    }
  })

  const { data: session } = useDeviceSession(sessionId)
  const { data: testInfo } = useTestInfo(projectId)

  // Metro events: the bundler's voice, kept as a short recent list. One
  // subscription implementation — useMetro's, the leak-free one — so this
  // screen only ever slices the feed and never opens a socket itself
  // (FM-DEVc: the hand-rolled effect here leaked its stream when the screen
  // unmounted before the open resolved).
  const metroEvents = useMetro(projectId)
  const metro = metroEvents.length > 0 ? metroEvents[metroEvents.length - 1] : null
  const metroError = metro?.kind === 'error' ? metro : null

  const [copied, setCopied] = React.useState(false)
  function copyLink() {
    if (!testInfo?.deep_link) return
    Clipboard.setString(testInfo.deep_link)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  // L55 / FM-APKi — the APK installs from inside the app. The button used
  // to hand the URL to the system browser, which carries no session (the
  // re-auth wall the user hit on camera) and resolves localhost URLs to
  // the phone itself. The helper downloads with the session token and
  // fires the system install intent; a failure surfaces here as its own
  // honest line, never a silent no-op.
  const [apkWorking, setApkWorking] = React.useState(false)
  const [apkError, setApkError] = React.useState<string | null>(null)
  async function installDevClient() {
    const url = testInfo?.dev_client_url
    if (!url || apkWorking) return
    setApkWorking(true)
    setApkError(null)
    try {
      await downloadAndInstallDevClient(url)
      setApkWorking(false)
    } catch (e) {
      setApkWorking(false)
      setApkError(e instanceof Error ? e.message : 'could not install the dev client')
    }
  }

  async function startSession() {
    if (projectId == null) return
    const s = await createSession.mutateAsync(projectId)
    setLocalSessionId(s.id)
    await setSessionId(projectId, s.id)
  }

  async function endSession() {
    if (sessionId == null) return
    await destroySession.mutateAsync(sessionId)
    if (projectId != null) await setSessionId(projectId, null)
    setLocalSessionId(null)
  }

  return (
    <Screen topInset={false}>
      <View style={styles.head}>
        <View style={styles.titles}>
          <UIText variant="micro" tone="faint">
            {project ? project.name.toUpperCase() : 'DEVICE'}
          </UIText>
          <UIText variant="heading" tone="hero">
            Device
          </UIText>
        </View>
        <Badge
          label={sessionId == null ? 'no session' : (session?.state ?? 'live')}
          tone={sessionId == null ? 'neutral' : 'success'}
        />
      </View>

      <View style={styles.metroBadge}>
        <UIText variant="monoSm" tone={metroError ? 'danger' : 'faint'}>
          {metroLabel(metro)}
        </UIText>
      </View>

      <ScrollView style={styles.stream} showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          {/* This phone: Metro connects the installed app to the workspace.
              No streaming, the screen in your hand is the render. */}
          <View style={styles.card}>
            <UIText variant="title" tone="hero">
              This phone
            </UIText>
            <UIText variant="bodySm" tone="muted">
              Install the dev client once. Metro connects it to the workspace and reloads after each turn.
            </UIText>
            {testInfo?.metro_url ? (
              <UIText variant="monoSm" tone="faint" numberOfLines={1}>
                {testInfo.metro_url}
              </UIText>
            ) : null}
            {testInfo?.deep_link ? (
              <View style={styles.linkRow}>
                <UIText variant="monoSm" tone="body" numberOfLines={1} style={styles.linkText}>
                  {testInfo.deep_link}
                </UIText>
                <Pressable onPress={copyLink} style={styles.miniBtn} accessibilityLabel="Copy deep link">
                  <UIText variant="label" tone="body">
                    {copied ? 'copied' : 'copy'}
                  </UIText>
                </Pressable>
              </View>
            ) : null}
            <View style={styles.actions}>
              {testInfo?.deep_link ? (
                <LimeButton
                  title="Open on this phone"
                  onPress={() => void Linking.openURL(testInfo.dev_launcher_link || testInfo.deep_link!)}
                />
              ) : null}
              {testInfo?.dev_client_url ? (
                <View>
                  <Pressable
                    onPress={() => void installDevClient()}
                    disabled={apkWorking}
                    accessibilityLabel="Download and install the dev client"
                  >
                    <UIText variant="label" tone={apkWorking ? 'faint' : 'muted'} style={styles.underline}>
                      {apkWorking ? 'preparing the install…' : 'Download the dev client (APK)'}
                    </UIText>
                  </Pressable>
                  {apkError ? (
                    <UIText variant="label" tone="danger" style={styles.apkError}>
                      {apkError}
                    </UIText>
                  ) : null}
                </View>
              ) : null}
            </View>
          </View>

          {/* Session: the relay behind the QR. Optional cloud device for
              turns that need one; your phone does not need it. */}
          <View style={styles.card}>
            <UIText variant="title" tone="hero">
              Session
            </UIText>
            {sessionId == null ? (
              <>
                <UIText variant="bodySm" tone="muted">
                  No session. The pairing QR and Metro relay appear with one.
                </UIText>
                <LimeButton
                  title={createSession.isPending ? 'Claiming a device…' : 'Start session'}
                  disabled={projectId == null || createSession.isPending}
                  onPress={startSession}
                />
                {createSession.isError && (
                  <UIText variant="caption" style={{ color: Colors.coralRed }}>
                    {createSession.error instanceof Error ? createSession.error.message : 'could not start the session'}
                  </UIText>
                )}
              </>
            ) : (
              <>
                <View style={styles.kvRow}>
                  <UIText variant="caption" tone="muted">
                    Session
                  </UIText>
                  <UIText variant="monoSm" tone="body">
                    #{sessionId}
                  </UIText>
                </View>
                <View style={styles.kvRow}>
                  <UIText variant="caption" tone="muted">
                    State
                  </UIText>
                  <UIText variant="monoSm" tone="body">
                    {session?.state ?? 'live'}
                  </UIText>
                </View>
                <Pressable
                  onPress={endSession}
                  disabled={destroySession.isPending}
                  style={styles.destroyBtn}
                  accessibilityLabel="Destroy session"
                >
                  <UIText variant="label" style={{ color: Colors.coralRed }}>
                    {destroySession.isPending ? 'Destroying…' : 'Destroy session'}
                  </UIText>
                </Pressable>
              </>
            )}
          </View>

          {/* Pairing QR for other phones. */}
          <View style={styles.card}>
            <UIText variant="title" tone="hero">
              Pair another phone
            </UIText>
            {testInfo?.deep_link ? (
              <View style={styles.qrWrap}>
                <QRCodeStyled
                  data={testInfo.deep_link}
                  padding={8}
                  size={168}
                  color={Colors.paper}
                  style={{ borderRadius: 8, backgroundColor: Colors.void }}
                />
                <UIText variant="caption" tone="muted" style={styles.center}>
                  Scan with the other phone camera, or open it in the dev client there.
                </UIText>
              </View>
            ) : (
              <UIText variant="caption" tone="faint" style={styles.center}>
                {testInfo ? testInfo.reason || 'Not available yet. Build the project first.' : 'Loading…'}
              </UIText>
            )}
          </View>

          {/* Recent Metro events: what the bundler just did. */}
          {metroEvents.length > 0 && (
            <View style={styles.card}>
              <UIText variant="title" tone="hero">
                Metro activity
              </UIText>
              {metroEvents
                .slice(-6)
                .reverse()
                .map((ev, i) => (
                  <View key={`${i}-${ev.kind}`} style={styles.metroRow}>
                    <UIText variant="monoSm" tone={ev.kind === 'error' ? 'danger' : 'faint'}>
                      {ev.kind.replaceAll('_', ' ')}
                    </UIText>
                    {ev.text ? (
                      <UIText variant="caption" tone="muted" numberOfLines={1} style={styles.metroText}>
                        {ev.text}
                      </UIText>
                    ) : null}
                  </View>
                ))}
            </View>
          )}

          <UIText variant="caption" tone="faint" style={styles.center}>
            On your own device the agent cannot see the screen or logs. Changes you test there are yours to verify.
          </UIText>
        </View>
      </ScrollView>
    </Screen>
  )
}

const styles = StyleSheet.create({
  head: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: Spacing.s12,
    marginBottom: Spacing.s4,
  },
  titles: { gap: 8, flex: 1 },
  metroBadge: {
    alignSelf: 'flex-start',
    paddingHorizontal: 10,
    paddingVertical: 6,
    borderRadius: 9999,
    backgroundColor: Colors.carbon,
    marginTop: 6,
    marginBottom: 4,
  },
  stream: { flex: 1, marginTop: Spacing.s12 },
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  card: {
    backgroundColor: Colors.carbon,
    borderRadius: 12,
    padding: Spacing.s16,
    gap: Spacing.s12,
  },
  linkRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    backgroundColor: Colors.obsidian,
    borderRadius: 8,
    padding: 6,
  },
  linkText: { flex: 1, paddingHorizontal: 6 },
  miniBtn: {
    minHeight: 36,
    paddingHorizontal: 12,
    borderRadius: 8,
    backgroundColor: Colors.graphite,
    alignItems: 'center',
    justifyContent: 'center',
  },
  actions: { gap: Spacing.s8, alignItems: 'stretch' },
  underline: { textDecorationLine: 'underline', textAlign: 'center' },
  apkError: { marginTop: 4, textAlign: 'center' },
  kvRow: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  destroyBtn: {
    minHeight: 44,
    borderRadius: 10,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
  },
  qrWrap: { alignItems: 'center', gap: Spacing.s12 },
  center: { textAlign: 'center' },
  metroRow: { flexDirection: 'row', alignItems: 'baseline', gap: Spacing.s8 },
  metroText: { flex: 1 },
})
