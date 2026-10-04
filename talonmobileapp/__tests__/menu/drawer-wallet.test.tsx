import React from 'react'
import { StyleSheet } from 'react-native'
import { render, screen, fireEvent, waitFor } from '@testing-library/react-native'
import { Drawer } from '@/components/nav/Drawer'
import { meApi } from '@/lib/api'
import { linkWalletNow } from '@/lib/walletlink'

// FM-WALLb — the mobile half of the reminder: while /v1/me reports
// wallet_linked=false the drawer carries a BOLD row whose press RUNS the
// L63 connect flow (session challenge → MWA signature → verify), and the
// row vanishes the moment a wallet is linked.
//
// Harness: the drawer's profile read is mocked at the api seam; the
// wallet kit and link flow are mocked at theirs — the press must DRIVE
// the flow, so the flow seam is what gets asserted.
//
// Red before L65 (observed): the drawer reads email + tier only — the
// reminder row does not exist at any state.

jest.mock('react-native-safe-area-context', () =>
  jest.requireActual('react-native-safe-area-context/jest/mock').default,
)
jest.mock('react-native-reanimated', () => require('@/test/native-seams').reanimated())
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: jest.fn(), replace: jest.fn() }),
}))
jest.mock('@/lib/api', () => ({
  authApi: { revoke: jest.fn(async () => {}) },
  meApi: { get: jest.fn() },
}))
jest.mock('@/lib/auth', () => ({
  clearToken: jest.fn(async () => {}),
  getToken: jest.fn(async () => 'session-token'),
}))
jest.mock('@/lib/walletlink', () => ({ linkWalletNow: jest.fn(async () => {}) }))
jest.mock('@wallet-ui/react-native-kit', () => ({
  useMobileWallet: jest.fn(() => ({ connect: jest.fn(), signMessages: jest.fn() })),
}))

beforeEach(() => {
  jest.clearAllMocks()
})

it('L65 — the drawer reminds in bold while the wallet is unlinked, and the press runs the link flow', async () => {
  ;(meApi.get as jest.Mock).mockResolvedValue({
    id: 7,
    email: 'dev@talon.app',
    tier: 'free',
    wallet_linked: false,
  })

  await render(<Drawer open width={300} onClose={() => {}} />)

  const row = await screen.findByText('Connect your wallet')
  // Bold is the instruction: the text's own style carries the weight.
  const styleProp = (row as unknown as { props: { style: unknown } }).props.style
  const flat = StyleSheet.flatten(styleProp) as { fontWeight?: string }
  expect(flat.fontWeight).toBe('700')

  fireEvent.press(row)
  await waitFor(() => expect(linkWalletNow).toHaveBeenCalledTimes(1))
})

it('L65 — a linked wallet keeps the drawer quiet', async () => {
  ;(meApi.get as jest.Mock).mockResolvedValue({
    id: 7,
    email: 'dev@talon.app',
    tier: 'free',
    wallet_linked: true,
  })

  await render(<Drawer open width={300} onClose={() => {}} />)

  await screen.findByText('Profile')
  expect(screen.queryByText('Connect your wallet')).toBeNull()
})

it('L65 — a guest is never nagged', async () => {
  ;(meApi.get as jest.Mock).mockRejectedValue(new Error('401'))

  await render(<Drawer open width={300} onClose={() => {}} />)

  await screen.findByText('Profile')
  expect(screen.queryByText('Connect your wallet')).toBeNull()
})
