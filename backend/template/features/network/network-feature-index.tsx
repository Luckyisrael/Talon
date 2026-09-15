import React from 'react'
import { Text, View } from 'react-native'
import { NetworkFeatureGetVersion } from './network-feature-get-version'
import { NetworkFeatureGetGenesisHash } from './network-feature-get-genesis-hash'
import { NetworkUiSelect } from './network-ui-select'
import { useNetwork } from './use-network'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import { MaterialIcons } from '@expo/vector-icons'

export function NetworkFeatureIndex() {
  const { selectedNetwork, networks, setSelectedNetwork } = useNetwork()
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.stack}>
      <Text style={s.sectionTitle}>Network</Text>
      <NetworkUiSelect
        networks={networks}
        selectedNetwork={selectedNetwork}
        setSelectedNetwork={setSelectedNetwork}
      />
      <View style={s.card}>
        <View style={s.row}>
          <Text style={s.label}>Endpoint</Text>
          <View style={[s.stackTight, { flex: 1, alignItems: 'flex-end' }]}>
            <Text style={s.body}>{selectedNetwork.label}</Text>
            <View style={[s.stackTight, { flexDirection: 'row', alignItems: 'center', gap: 4 }]}>
              <MaterialIcons name="bolt" size={14} color={t.success} />
              <Text style={[s.subtitle, { color: t.success }]}>Live</Text>
            </View>
          </View>
        </View>
        <View style={s.rowSeparator} />
        <NetworkFeatureGetVersion />
        <NetworkFeatureGetGenesisHash />
      </View>
    </View>
  )
}
