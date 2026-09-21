import React from 'react'
import { StyleSheet, View } from 'react-native'
import { useRouter } from 'expo-router'
import Animated, { FadeIn, FadeInUp, useReducedMotion } from 'react-native-reanimated'
import * as Haptics from 'expo-haptics'
import { MessageQuestion, Notification, TickCircle } from 'iconsax-react-native'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { GhostButton, LimeButton, UIText } from '@/components/ui'
import { Colors, Spacing } from '@/constants/theme'
import { pushAvailable, pushPermissionGranted, requestPushPermission } from '@/lib/push'

// Push permission, staged as a first-run moment instead of a settings row:
// one hero, the two pushes it covers, one action. Seen once, so the
// entrance earns a short stagger; reduced motion fades without travel.
export default function NotificationSetupScreen() {
  const router = useRouter()
  const reduceMotion = useReducedMotion()
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
    if (ok) void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success)
    setState(ok ? 'granted' : 'denied')
  }

  // Rare screen, so the entrance staggers: 70ms apart, ease-out, never
  // from nothing (opacity plus a short rise). Reduced motion keeps the
  // fade and drops the travel.
  const rise = (i: number) => (reduceMotion ? FadeIn.duration(160) : FadeInUp.duration(280).delay(90 + i * 70))

  return (
    <MenuScreen title="Push notifications">
      <View style={styles.stage}>
        <Animated.View entering={rise(0)} style={styles.orbWrap}>
          <View style={styles.orbGlow} />
          <View style={styles.orbRing} />
          <View style={styles.orbCore}>
            <Notification size={44} color={Colors.mist} variant="Bulk" />
          </View>
        </Animated.View>

        <Animated.View entering={rise(1)} style={styles.copy}>
          <UIText variant="heading" tone="hero" style={styles.headline}>
            Never miss the moment
          </UIText>
          <UIText variant="body" tone="muted" style={styles.sub}>
            Talon pushes twice, and only twice. Everything else stays in the app.
          </UIText>
        </Animated.View>

        <Animated.View entering={rise(2)} style={styles.cards}>
          <View style={styles.card}>
            <View style={styles.cardIcon}>
              <MessageQuestion size={20} color={Colors.acidLime} variant="Bulk" />
            </View>
            <View style={styles.cardText}>
              <UIText variant="body" tone="hero">
                Pauses that need you
              </UIText>
              <UIText variant="bodySm" tone="muted">
                A turn stopped with a question answers from the lock screen.
              </UIText>
            </View>
          </View>
          <View style={styles.card}>
            <View style={styles.cardIcon}>
              <TickCircle size={20} color={Colors.acidLime} variant="Bulk" />
            </View>
            <View style={styles.cardText}>
              <UIText variant="body" tone="hero">
                Finishes worth seeing
              </UIText>
              <UIText variant="bodySm" tone="muted">
                A build that passed lands the moment it is done.
              </UIText>
            </View>
          </View>
        </Animated.View>

        <Animated.View entering={rise(3)} style={styles.action}>
          {state === 'granted' ? (
            <View style={styles.granted}>
              <TickCircle size={22} color={Colors.acidLime} variant="Bulk" />
              <UIText variant="body" tone="body">
                Push is on. Pauses and finishes will reach you.
              </UIText>
            </View>
          ) : (
            <LimeButton
              title={state === 'asking' ? 'Asking…' : 'Enable notifications'}
              disabled={state === 'asking' || state === 'unconfigured'}
              onPress={enable}
            />
          )}
          {state === 'denied' && (
            <UIText variant="bodySm" tone="muted" style={styles.note}>
              Permission was not granted. Allow notifications for Talon in Settings, then tap enable again.
            </UIText>
          )}
          {state === 'unconfigured' && (
            <UIText variant="bodySm" tone="muted" style={styles.note}>
              Push is not configured on this build yet. New events still land in Notifications.
            </UIText>
          )}
          <UIText variant="caption" tone="faint" style={styles.note}>
            The system dialog asks once. Saying no changes nothing else.
          </UIText>
          {state === 'granted' && <GhostButton title="Done" onPress={() => router.back()} />}
        </Animated.View>
      </View>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stage: { flex: 1, gap: Spacing.s20, paddingTop: Spacing.s8, paddingBottom: Spacing.s32 },
  orbWrap: { alignItems: 'center', justifyContent: 'center', marginVertical: Spacing.s8 },
  orbGlow: {
    position: 'absolute',
    width: 168,
    height: 168,
    borderRadius: 84,
    backgroundColor: 'rgba(228, 242, 34, 0.08)',
  },
  orbRing: {
    position: 'absolute',
    width: 128,
    height: 128,
    borderRadius: 64,
    borderWidth: StyleSheet.hairlineWidth,
    borderColor: 'rgba(228, 242, 34, 0.28)',
  },
  orbCore: {
    width: 96,
    height: 96,
    borderRadius: 48,
    backgroundColor: Colors.carbon,
    alignItems: 'center',
    justifyContent: 'center',
  },
  copy: { gap: Spacing.s8, alignItems: 'center' },
  headline: { textAlign: 'center' },
  sub: { textAlign: 'center', paddingHorizontal: Spacing.s16 },
  cards: { gap: Spacing.s8 },
  card: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s12,
    backgroundColor: Colors.carbon,
    borderRadius: 14,
    padding: Spacing.s12,
  },
  cardIcon: {
    width: 40,
    height: 40,
    borderRadius: 12,
    backgroundColor: Colors.obsidian,
    alignItems: 'center',
    justifyContent: 'center',
  },
  cardText: { flex: 1, gap: 2 },
  action: { gap: Spacing.s12, marginTop: 'auto' },
  granted: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: Spacing.s8,
    backgroundColor: Colors.carbon,
    borderRadius: 14,
    padding: Spacing.s12,
  },
  note: { textAlign: 'center', paddingHorizontal: Spacing.s16 },
})
