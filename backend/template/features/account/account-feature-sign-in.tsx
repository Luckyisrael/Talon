import { useState } from 'react'
import { ActionButton } from '@/components/action-button'
import { Account, useMobileWallet } from '@wallet-ui/react-native-kit'

export function AccountFeatureSignIn({ account }: { account?: Account }) {
  const { chain, identity, signIn } = useMobileWallet()
  const [pending, setPending] = useState(false)

  async function submit() {
    setPending(true)
    try {
      await signIn({
        address: account?.address.toString(),
        chainId: chain,
        uri: identity.uri,
      })
    } catch {
      // A cancelled sign-in is a normal wallet outcome, not an error screen.
    } finally {
      setPending(false)
    }
  }

  return (
    <ActionButton
      label={account ? `Sign in with ${account.label}` : 'Sign in and connect'}
      loading={pending}
      onPress={submit}
    />
  )
}
