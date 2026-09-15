import { Text, View } from 'react-native'
import { useNetworkGetVersion } from './use-network-get-version'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import React from 'react'

export function NetworkFeatureGetVersion() {
  const { data, isLoading } = useNetworkGetVersion()
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.row}>
      <Text style={s.label}>Version</Text>
      <Text style={s.mono}>{isLoading ? 'Loading…' : `${data?.core} (${data?.features})`}</Text>
    </View>
  )
}
