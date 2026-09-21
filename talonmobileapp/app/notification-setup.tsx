import React from 'react'
import { StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { GhostButton, LimeButton, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'
import { pushAvailable, pushPermissionGranted, requestPushPermission } from '@/lib/push'

// Push permission, explained before it is asked. The app only pushes two
// things: a turn paused for input, and a build that finished. The dialog
// is the OS one; the choice stays reversible in Settings either way.
export default function NotificationSetupScreen() {
  const router = useRouter()
  const [state, setState] = React.useState<'unknown' | 'granted' | 'denied' | 'unconfigured' | 'asking'>('unknown')

  React.useEffect(() => {
    let alive = true
    if (!pushAvailable()) {
      setState('unconfigured')
      return
    }
    void pushPermissionGranted().then((g) => {
      if (alive) setState(g ? 'granted' : 'unknown')
    })
    return () => {
      alive = false
    }
  }, [])

  async function enable() {
    setState('asking')
    const ok = await requestPushPermission()
    setState(ok ? 'granted' : 'denied')
  }

  return (
    <MenuScreen title="Push notifications">
      <View style={styles.stack}>
        <UIText variant="title" tone="hero">
          Know the moment the agent needs you
        </UIText>
        <UIText variant="body" tone="muted">
          Talon pushes twice, and only twice. A turn paused with a question, so it never waits unseen. A build that
          finished, so you can look when it matters.
        </UIText>
        <UIText variant="body" tone="muted">
          Tapping enable shows the system dialog. Saying no just keeps everything in the Notifications screen; nothing
          else changes, and you can flip it later in Settings.
        </UIText>

        {state === 'unconfigured' && (
          <UIText variant="bodySm" tone="muted">
            Push is not configured on this build yet, so there is nothing to enable. New events still land in
            Notifications.
          </UIText>
        )}
        {state === 'denied' && (
          <UIText variant="bodySm" tone="muted">
            Permission was not granted. Open Settings, allow notifications for Talon, then come back and tap enable
            again.
          </UIText>
        )}
        {state === 'granted' && (
          <UIText variant="bodySm" tone="body">
            Push is on. Pauses and finishes will reach your lock screen.
          </UIText>
        )}

        {state === 'granted' ? (
          <GhostButton title="Back" onPress={() => router.back()} />
        ) : (
          <LimeButton
            title={state === 'asking' ? 'Asking…' : 'Enable notifications'}
            disabled={state === 'asking' || state === 'unconfigured'}
            onPress={enable}
          />
        )}
      </View>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
})
