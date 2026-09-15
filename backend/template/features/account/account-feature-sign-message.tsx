import { useState } from 'react'
import { ActionButton } from '@/components/action-button'
import { useMobileWallet } from '@wallet-ui/react-native-kit'
import { Address } from '@solana/kit'

export function AccountFeatureSignMessage({ address }: { address: Address }) {
  const { signMessages } = useMobileWallet()
  const [pending, setPending] = useState(false)
  const [outcome, setOutcome] = useState<'idle' | 'signed' | 'failed'>('idle')

  async function submit() {
    setPending(true)
    setOutcome('idle')
    try {
      await signMessages(new TextEncoder().encode(`Signing a message with ${address}`))
      setOutcome('signed')
    } catch {
      // A rejected signature is the user's choice; the button says so and stays usable.
      setOutcome('failed')
    } finally {
      setPending(false)
    }
  }

  const label = outcome === 'signed' ? 'Message signed' : outcome === 'failed' ? 'Signature rejected — retry' : 'Sign message'

  return (
    <ActionButton
      label={label}
      loading={pending}
      variant={outcome === 'failed' ? 'danger' : 'ghost'}
      onPress={submit}
    />
  )
}
