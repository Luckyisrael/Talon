import { Text, View } from 'react-native'
import { useAccountGetBalance } from '@/features/account/use-account-get-balance'
import { lamportsToSol } from '@/utils/lamports-to-sol'
import { ellipsify } from '@/utils/ellipsify'
import { Address } from '@solana/kit'
import { createStyles } from '@/constants/app-styles'
import { useTheme } from '@/constants/theme'
import React from 'react'

export function AccountFeatureGetBalance({ address }: { address: Address }) {
  const { data, isLoading } = useAccountGetBalance({ address })
  const t = useTheme()
  const s = createStyles(t)

  return (
    <View style={s.stackTight}>
      <View style={s.row}>
        <Text style={s.label}>Address</Text>
        <Text style={s.mono}>{ellipsify(address.toString(), 6)}</Text>
      </View>
      <View style={s.row}>
        <Text style={s.label}>Balance</Text>
        <Text style={s.numeric}>
          {isLoading ? 'Loading…' : `${lamportsToSol(data?.value ?? 0n).toLocaleString()} SOL`}
        </Text>
      </View>
    </View>
  )
}
