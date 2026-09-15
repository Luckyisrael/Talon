import { useState } from 'react'
import { ActionButton } from '@/components/action-button'
import { getAddMemoInstruction } from '@solana-program/memo'
import { useMobileWallet } from '@wallet-ui/react-native-kit'
import { Address, Instruction } from '@solana/kit'

export function AccountFeatureSignTransaction({ address }: { address: Address }) {
  const { sendTransactions } = useMobileWallet()
  const [pending, setPending] = useState(false)

  async function submit() {
    setPending(true)
    try {
      const instructions: Instruction[] = [
        // You can add more instructions here
        getAddMemoInstruction({ memo: `gm from Mobile Wallet Adapter - ${address}` }),
      ]
      await sendTransactions(instructions)
    } catch {
      // The user can decline the signing sheet — that is a normal outcome here.
    } finally {
      setPending(false)
    }
  }

  return <ActionButton label="Sign transaction" loading={pending} onPress={submit} />
}
