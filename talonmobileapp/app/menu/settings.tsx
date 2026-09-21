import React from 'react'
import { StyleSheet, View } from 'react-native'
import Constants from 'expo-constants'
import { MenuScreen } from '@/components/nav/MenuScreen'
import { Card, UIText } from '@/components/ui'
import { Spacing } from '@/constants/theme'
import { pushAvailable } from '@/lib/push'

// Settings: connection truth and device preferences. Nothing here is
// decorative: the API URL is the origin every request in this app uses,
// push state mirrors the OneSignal binding the backend reads.
const API_URL = process.env.EXPO_PUBLIC_API_URL ?? 'http://localhost:8080'

export default function SettingsScreen() {
  return (
    <MenuScreen title="Settings">
      <Card>
        <View style={styles.stack}>
          <UIText variant="title" tone="hero">
            Connection
          </UIText>
          <View style={styles.kvRow}>
            <UIText variant="caption" tone="muted">
              API
            </UIText>
            <UIText variant="monoSm" tone="body" numberOfLines={1} style={styles.kvValue}>
              {API_URL}
            </UIText>
          </View>
          <View style={styles.kvRow}>
            <UIText variant="caption" tone="muted">
              Push
            </UIText>
            <UIText variant="monoSm" tone="body">
              {pushAvailable() ? 'available' : 'off'}
            </UIText>
          </View>
        </View>
      </Card>
      <Card>
        <View style={styles.stack}>
          <UIText variant="title" tone="hero">
            About
          </UIText>
          <View style={styles.kvRow}>
            <UIText variant="caption" tone="muted">
              Version
            </UIText>
            <UIText variant="monoSm" tone="body">
              {Constants.expoConfig?.version ?? '1.0.0'}
            </UIText>
          </View>
          <UIText variant="caption" tone="faint">
            Dark only. The system is built on darkness as substrate.
          </UIText>
        </View>
      </Card>
    </MenuScreen>
  )
}

const styles = StyleSheet.create({
  stack: { gap: Spacing.s8 },
  kvRow: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', gap: Spacing.s12 },
  kvValue: { flex: 1, textAlign: 'right' },
})
