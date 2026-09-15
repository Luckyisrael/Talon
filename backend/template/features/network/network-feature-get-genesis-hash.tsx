import { Text, View } from 'react-native'
import { ellipsify } from '@/utils/ellipsify'
import { useNetworkGetGenesisHash } from './use-network-get-genesis-hash'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import React from 'react'

export function NetworkFeatureGetGenesisHash() {
  const { data, isLoading } = useNetworkGetGenesisHash()
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.row}>
      <Text style={s.label}>Genesis</Text>
      <Text style={s.mono}>{isLoading ? 'Loading…' : ellipsify(data, 8)}</Text>
    </View>
  )
}
