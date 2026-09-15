import { useState } from 'react'
import { ActionButton } from '@/components/action-button'
import { useMobileWallet } from '@wallet-ui/react-native-kit'

export function AccountFeatureConnect() {
  const { account, connect } = useMobileWallet()
  const [pending, setPending] = useState(false)

  async function submit() {
    setPending(true)
    try {
      await connect()
    } finally {
      // A cancelled wallet sheet is a normal outcome, not an error state — the
      // button returns to ready either way.
      setPending(false)
    }
  }

  return (
    <ActionButton
      label="Connect wallet"
      variant="primary"
      loading={pending}
      disabled={!!account}
      onPress={submit}
    />
  )
}
