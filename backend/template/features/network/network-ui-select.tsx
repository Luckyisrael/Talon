import { Pressable, Text, View } from 'react-native'
import React from 'react'
import { SolanaCluster } from '@wallet-ui/react-native-kit'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import { MaterialIcons } from '@expo/vector-icons'

export function NetworkUiSelect({
  networks,
  selectedNetwork,
  setSelectedNetwork,
}: {
  networks: SolanaCluster[]
  selectedNetwork: SolanaCluster
  setSelectedNetwork: (network: SolanaCluster) => void
}) {
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.card}>
      {networks.map((network, i) => {
        const active = network.id === selectedNetwork.id
        return (
          <React.Fragment key={network.id}>
            {i > 0 && <View style={s.rowSeparator} />}
            <Pressable
              onPress={() => setSelectedNetwork(network)}
              disabled={active}
              hitSlop={{ top: 6, bottom: 6 }}
              style={({ pressed }) => [s.row, pressed && { opacity: 0.6 }]}
              accessibilityRole="button"
              accessibilityState={{ selected: active }}
            >
              <Text style={[s.body, active && { color: t.accent }]}>{network.label}</Text>
              {active ? (
                <MaterialIcons name="check-circle" size={20} color={t.accent} />
              ) : (
                <MaterialIcons name="radio-button-unchecked" size={20} color={t.textTertiary} />
              )}
            </Pressable>
          </React.Fragment>
        )
      })}
    </View>
  )
}
