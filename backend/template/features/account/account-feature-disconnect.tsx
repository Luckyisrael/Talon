import { useState } from 'react'
import { ActionButton } from '@/components/action-button'
import { useMobileWallet } from '@wallet-ui/react-native-kit'

export function AccountFeatureDisconnect() {
  const { account, disconnect } = useMobileWallet()
  const [pending, setPending] = useState(false)

  async function submit() {
    setPending(true)
    try {
      await disconnect()
    } finally {
      setPending(false)
    }
  }

  return (
    <ActionButton
      label="Disconnect"
      variant="danger"
      loading={pending}
      disabled={!account}
      onPress={submit}
    />
  )
}
