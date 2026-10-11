import React from 'react'
import { render, fireEvent, waitFor, screen } from '@testing-library/react-native'
import { ConnectScreen } from '@/app/onboarding/screens'
import { beginWalletLink } from '@/lib/walletlink'

// FM-WALLa(iii) — the connect control lies. The onboarding screen's own
// copy promises a Mobile Wallet Adapter handshake ("Talon asks your wallet
// app to sign…"), but the button routes to /auth and never asks for a
// signature — a control that promises connection and delivers navigation.
//
// Harness: expo-router captured through a mock-prefixed spy (jest's module
// factory hoisting allows `mock*` references), the wallet kit and the link
// flow mocked at their seams — the press must DRIVE the flow, so the flow
// seam is what gets asserted.
//
// Red before L63 (observed): press "Connect wallet" → router.push('/auth'),
// beginWalletLink never called.

jest.mock('react-native-safe-area-context', () =>
  jest.requireActual('react-native-safe-area-context/jest/mock').default,
)
// Reanimated boots native Worklets at import — the shipped seam renders
// plain views instead (see test/native-seams.ts).
jest.mock('react-native-reanimated', () => require('@/test/native-seams').reanimated())
// The real `latestWallet` (L78) pulls the flow module, which imports
// AsyncStorage at load — one shared fake (test/async-storage-seam.ts).
jest.mock('@react-native-async-storage/async-storage', () => require('@/test/async-storage-seam').asyncStorage())
const mockPush = jest.fn()
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: mockPush }),
}))
jest.mock('@/lib/walletlink', () => ({
  beginWalletLink: jest.fn(),
  // The real binding law runs here: the flow receives the CURRENT hook
  // object (L78/FM-WALLd(v)), only the flow itself is stubbed.
  latestWallet: jest.requireActual('@/lib/walletlink').latestWallet,
}))
jest.mock('@wallet-ui/react-native-kit', () => ({
  useMobileWallet: jest.fn(() => ({
    connect: jest.fn(),
    signMessages: jest.fn(),
    account: undefined,
    disconnect: jest.fn(),
  })),
}))
jest.mock('@/stores/app', () => ({
  useAppStore: (selector: (s: unknown) => unknown) =>
    selector({ markOnboarded: jest.fn(async () => {}) }),
}))

beforeEach(() => {
  jest.clearAllMocks()
})

it('L63 — "Connect wallet" runs the signature flow before continuing', async () => {
  await render(<ConnectScreen />)
  fireEvent.press(await screen.findByText('Connect wallet'))

  await waitFor(() => expect(beginWalletLink).toHaveBeenCalledTimes(1))
  // The handshake comes first; sign-in still follows it.
  await waitFor(() => expect(mockPush).toHaveBeenCalledWith('/auth'))
})

it('L63 — "Later" stays an honest skip: no signature demanded', async () => {
  await render(<ConnectScreen />)
  fireEvent.press(await screen.findByText('Later'))

  await waitFor(() => expect(mockPush).toHaveBeenCalledWith('/auth'))
  expect(beginWalletLink).not.toHaveBeenCalled()
})

// FM-WALLc(i) / L73(b) — the catch that swallowed every failure had a
// `finally` that pushed /auth whatever the wallet answered: no wallet
// installed, declined, challenge dead — all three walked into sign-in as
// if linked. The refusal must land its reason and stay put; "Later"
// remains the deliberate skip.
//
// Red before L73 (observed): press "Connect wallet" with a rejected
// handshake → no reason is rendered, and router.push('/auth') runs anyway.
it('L73 — a refused connect states why and stays put (FM-WALLc(i))', async () => {
  ;(beginWalletLink as jest.Mock).mockRejectedValueOnce(new Error('No wallet found'))

  await render(<ConnectScreen />)
  fireEvent.press(await screen.findByText('Connect wallet'))

  // The failure lands its reason…
  expect(await screen.findByText(/No wallet found/)).toBeTruthy()
  // …and never navigates past it.
  await waitFor(() => expect(beginWalletLink).toHaveBeenCalledTimes(1))
  expect(mockPush).not.toHaveBeenCalled()
})
