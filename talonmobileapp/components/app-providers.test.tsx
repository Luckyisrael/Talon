// L50 / FM-KBDa — fails-first: the app root must mount keyboard-controller's
// KeyboardProvider. Without it the package's KeyboardAvoidingView has no
// keyboard context and the composer sits under the IME — the user's
// "keyboard blocks the input" report.
//
// The native package is replaced at the jest seam with marker components, so
// this suite asserts the WIRING (provider mounted, provider wraps children),
// not the native behavior — the phone's rebuilt dev client owns that half.

import React from 'react'
import { render } from '@testing-library/react-native'
import { Text } from 'react-native'

jest.mock('react-native-keyboard-controller', () => {
  const React_ = require('react')
  return {
    KeyboardProvider: ({ children }: { children?: React.ReactNode }) =>
      React_.createElement('View', { testID: 'keyboard-provider' }, children),
    KeyboardAvoidingView: ({ children }: { children?: React.ReactNode }) => children,
  }
})

// AppProviders composes the network and wallet providers, none of which is
// under test here — replaced with pass-throughs so the root mounts in node.
jest.mock('@/features/network/network-provider', () => ({
  NetworkProvider: ({ render: r, children }: { render?: (v: unknown) => unknown; children?: React.ReactNode }) =>
    r ? (r({ selectedNetwork: { identity: {} } }) as React.ReactNode) : children,
}))
jest.mock('@wallet-ui/react-native-kit', () => ({
  MobileWalletProvider: ({ children }: { children?: React.ReactNode }) => children,
  // constants/app-config.ts builds its network list from these at import —
  // values are inert here, only the shape matters for the root to mount.
  createSolanaDevnet: jest.fn(() => ({ id: 'devnet' })),
  createSolanaTestnet: jest.fn(() => ({ id: 'testnet' })),
}))
jest.mock('@/stores/app', () => ({
  useAppStore: { getState: () => ({ hydrate: jest.fn() }) },
}))

import { AppProviders } from '@/components/app-providers'

it("mounts keyboard-controller's provider at the app root", async () => {
  const { getByTestId } = await render(
    <AppProviders>
      <Text testID="child">talon</Text>
    </AppProviders>,
  )

  const provider = getByTestId('keyboard-provider')
  expect(provider).toBeTruthy()
  // The provider must WRAP the app, not sit beside it.
  expect(provider.props.children).toBeTruthy()
})
