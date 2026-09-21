import React from 'react'
import { ScrollView, StyleSheet, View } from 'react-native'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Badge, Card, GhostButton, LimeButton, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'
import { useMe, useUsage } from '@/hooks'

// Upgrade: the mobile paywall, mirroring web pricing. Free is real, Pro is
// early access with no charge in test mode. Checkout wires up later, so the
// Pro CTA says exactly that instead of pretending to sell: an inline note,
// never a dead button and never a fake price.
const FREE_FEATURES = [
  '20 AI prompts per cycle',
  '60 minutes of live device time',
  'Agent trace, diff, and Metro on your phone',
  'Solana Mobile scaffolding built in',
]

const PRO_FEATURES = ['Everything in Free', '500 AI prompts per cycle', '25 hours of live device time']

const FAQS = [
  {
    q: 'Do I need a credit card to start?',
    a: 'No. The Free tier is open. We never ask for a card up front.',
  },
  {
    q: 'What counts as an AI prompt?',
    a: 'Each message you send the agent that it acts on. Reading files, running builds, and streaming the preview do not count.',
  },
  {
    q: 'How is device time measured?',
    a: 'Wall-clock minutes a virtual device runs for your project. Stopping the device stops the meter.',
  },
  {
    q: 'When do quotas reset?',
    a: 'Usage tracks against your current billing cycle and refreshes when a new one begins.',
  },
]

function FeatureList({ items }: { items: string[] }) {
  return (
    <View style={styles.features}>
      {items.map((f) => (
        <View key={f} style={styles.featureRow}>
          <View style={styles.tick}>
            <UIText variant="caption" tone="accent">
              ✓
            </UIText>
          </View>
          <UIText variant="bodySm" tone="body" style={styles.featureText}>
            {f}
          </UIText>
        </View>
      ))}
    </View>
  )
}

export default function UpgradeScreen() {
  const { data: me } = useMe()
  const { data: usage } = useUsage()
  const [notice, setNotice] = React.useState(false)
  const tier = me?.tier ?? 'free'
  const isPro = tier === 'pro'

  const used = usage?.prompts_used ?? 0
  const limit = usage?.prompts_limit ?? 0
  const unlimited = limit < 0

  return (
    <MenuScreen title="Upgrade">
      <ScrollView showsVerticalScrollIndicator={false}>
        <View style={styles.stack}>
          <UIText variant="body" tone="muted">
            Start free with a real allowance, not a trial. Upgrade when your builds outgrow it.
          </UIText>

          <Card>
            <View style={styles.tierHead}>
              <UIText variant="title" tone="hero">
                Free
              </UIText>
              <Badge label={isPro ? 'previous' : 'current plan'} tone={isPro ? 'neutral' : 'success'} />
            </View>
            <UIText variant="heading" tone="hero" style={styles.price}>
              $0
            </UIText>
            <UIText variant="monoSm" tone="faint">
              no credit card required
            </UIText>
            <UIText variant="bodySm" tone="muted" style={styles.tagline}>
              Everything you need to ship your first build.
            </UIText>
            <FeatureList items={FREE_FEATURES} />
            <UIText variant="monoSm" tone="faint">
              {unlimited ? `${used} prompts used · unlimited` : `${used} of ${limit} prompts used this cycle`}
            </UIText>
          </Card>

          <Card>
            <View style={styles.tierHead}>
              <UIText variant="title" tone="hero">
                Pro
              </UIText>
              <Badge label={isPro ? 'current plan' : 'early access'} tone={isPro ? 'success' : 'info'} />
            </View>
            <UIText variant="heading" tone="hero" style={styles.price}>
              Early access
            </UIText>
            <UIText variant="monoSm" tone="faint">
              no charge in test mode
            </UIText>
            <UIText variant="bodySm" tone="muted" style={styles.tagline}>
              Higher limits for heavier builds.
            </UIText>
            <FeatureList items={PRO_FEATURES} />
            {isPro ? (
              <GhostButton title="Current plan" disabled onPress={() => {}} />
            ) : (
              <LimeButton title="Upgrade to Pro" onPress={() => setNotice(true)} />
            )}
            {notice && !isPro && (
              <UIText variant="caption" tone="muted" style={styles.notice}>
                Checkout is not connected yet. Your tier stays Free, nothing is charged.
              </UIText>
            )}
          </Card>

          {FAQS.map((f) => (
            <Card key={f.q}>
              <View style={styles.faq}>
                <UIText variant="body" tone="hero">
                  {f.q}
                </UIText>
                <UIText variant="bodySm" tone="muted">
                  {f.a}
                </UIText>
              </View>
            </Card>
          ))}
        </View>
      </ScrollView>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s12, paddingBottom: Spacing.s32 },
  tierHead: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  price: { marginTop: Spacing.s12 },
  tagline: { marginTop: Spacing.s8 },
  features: { gap: Spacing.s8, marginVertical: Spacing.s12 },
  featureRow: { flexDirection: 'row', alignItems: 'flex-start', gap: Spacing.s8 },
  tick: {
    width: 18,
    height: 18,
    borderRadius: 9,
    backgroundColor: 'rgba(228,242,34,0.12)',
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 1,
  },
  featureText: { flex: 1 },
  notice: { marginTop: Spacing.s8 },
  faq: { gap: Spacing.s8 },
})
