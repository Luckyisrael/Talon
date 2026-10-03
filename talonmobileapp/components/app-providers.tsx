import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PropsWithChildren, useEffect } from 'react'
import { KeyboardProvider } from 'react-native-keyboard-controller'
import { NetworkProvider } from '@/features/network/network-provider'
import { MobileWalletProvider } from '@wallet-ui/react-native-kit'
import { AppConfig } from '@/constants/app-config'
import { useAppStore } from '@/stores/app'

const queryClient = new QueryClient()
export function AppProviders({ children }: PropsWithChildren) {
  useEffect(() => {
    void useAppStore.getState().hydrate()
  }, [])
  return (
    <QueryClientProvider client={queryClient}>
      <NetworkProvider
        networks={AppConfig.networks}
        render={({ selectedNetwork }) => (
          <MobileWalletProvider cluster={selectedNetwork} identity={AppConfig.identity}>
            {/* L50 / FM-KBDa — keyboard-controller's context for the whole
                app; KeyboardStickyView in the build screen reads it. Static
                import is lawful: the dev client carries the native module
                (rebuilt 2026-10-03 with the package linked). */}
            <KeyboardProvider>{children}</KeyboardProvider>
          </MobileWalletProvider>
        )}
      />
    </QueryClientProvider>
  )
}
