import { NetworkFeatureIndex } from '@/features/network/network-feature-index'
import { AccountFeatureIndex } from '@/features/account/account-feature-index'
import { AppConfig } from '@/constants/app-config'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import { ScrollView, Text, View } from 'react-native'
import { SafeAreaView } from 'react-native-safe-area-context'
import React from 'react'

export default function HomeScreen() {
  const t = useTheme()
  const s = createStyles(t)

  return (
    <SafeAreaView style={s.screen}>
      <ScrollView contentContainerStyle={s.stack} showsVerticalScrollIndicator={false}>
        <View>
          <Text style={s.title}>{AppConfig.identity.name}</Text>
          <Text style={s.subtitle}>{AppConfig.identity.uri}</Text>
        </View>

        <AccountFeatureIndex />
        <NetworkFeatureIndex />
      </ScrollView>
    </SafeAreaView>
  )
}
