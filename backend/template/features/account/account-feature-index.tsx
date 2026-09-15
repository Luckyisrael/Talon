import { Text, View } from 'react-native'
import React from 'react'
import { AccountFeatureGetBalance } from '@/features/account/account-feature-get-balance'
import { useMobileWallet } from '@wallet-ui/react-native-kit'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import { AccountFeatureSignMessage } from '@/features/account/account-feature-sign-message'
import { AccountFeatureSignTransaction } from '@/features/account/account-feature-sign-transaction'
import { AccountFeatureSignIn } from '@/features/account/account-feature-sign-in'
import { AccountFeatureDisconnect } from '@/features/account/account-feature-disconnect'
import { AccountFeatureConnect } from '@/features/account/account-feature-connect'
import { MaterialIcons } from '@expo/vector-icons'

export function AccountFeatureIndex() {
  const { account } = useMobileWallet()
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.stack}>
      <Text style={s.sectionTitle}>Account</Text>
      {account ? (
        <View style={s.stack}>
          <View style={s.card}>
            <View style={s.row}>
              <View style={[s.stackTight, { flex: 1 }]}>
                <Text style={s.body}>{account.label}</Text>
                <AccountFeatureGetBalance address={account.address} />
              </View>
              <MaterialIcons name="verified" size={20} color={t.success} />
            </View>
          </View>
          <AccountFeatureSignIn account={account} />
          <AccountFeatureSignMessage address={account.address} />
          <AccountFeatureSignTransaction address={account.address} />
          <AccountFeatureDisconnect />
        </View>
      ) : (
        <View style={s.card}>
          <Text style={s.body}>No wallet connected yet.</Text>
          <Text style={s.subtitle}>
            Connect a Mobile Wallet Adapter wallet to sign messages and transactions.
          </Text>
          <AccountFeatureConnect />
        </View>
      )}
    </View>
  )
}
