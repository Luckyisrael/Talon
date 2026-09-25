import React from 'react'
import { StyleSheet, View } from 'react-native'
import { Card, GhostButton, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'
import type { PlanData } from '@/lib/api'

// PlanCard is the plan turn's reply surface: the parsed plan the `plan` frame
// carried, message + screens + features + packages, and the approve action
// that hands it to a build turn — the web's PlanViewer + Approve in one card.
// It renders from the live tail AND from a restored plan block, so a plan
// survives a reload until the person approves it.
export function PlanCard({
  plan,
  message,
  onApprove,
}: {
  plan: PlanData
  message?: string
  onApprove?: (plan: PlanData) => void
}) {
  const screens = plan.screens ?? []
  const features = plan.features ?? []
  const packages = plan.packages ?? []
  return (
    <Card>
      <View style={styles.body}>
        <UIText variant="caption" tone="accent">
          PLAN
        </UIText>
        <UIText variant="bodyLg" tone="hero">
          {message || 'Plan ready.'}
        </UIText>
        {screens.length > 0 && (
          <View style={styles.group}>
            <UIText variant="label" tone="faint">
              SCREENS
            </UIText>
            {screens.map((s, i) => (
              <View key={`${s.name}-${i}`} style={styles.row}>
                <UIText variant="bodySm" tone="hero">
                  {s.name}
                </UIText>
                {s.description ? (
                  <UIText variant="caption" tone="muted">
                    {s.description}
                  </UIText>
                ) : null}
              </View>
            ))}
          </View>
        )}
        {features.length > 0 && (
          <View style={styles.group}>
            <UIText variant="label" tone="faint">
              FEATURES
            </UIText>
            {features.map((f, i) => (
              <UIText key={`${f}-${i}`} variant="bodySm" tone="muted">
                {f}
              </UIText>
            ))}
          </View>
        )}
        {packages.length > 0 && (
          <View style={styles.group}>
            <UIText variant="label" tone="faint">
              PACKAGES
            </UIText>
            {packages.map((p, i) => (
              <UIText key={`${p}-${i}`} variant="monoSm" tone="muted">
                {p}
              </UIText>
            ))}
          </View>
        )}
        {onApprove ? <GhostButton title="Approve & build" onPress={() => onApprove(plan)} /> : null}
      </View>
    </Card>
  )
}

const styles = StyleSheet.create({
  body: { gap: Spacing.s8 },
  group: { gap: 4 },
  row: { gap: 2 },
})
