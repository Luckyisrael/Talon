import React from 'react'
import { StyleSheet } from 'react-native'
import { render, screen, fireEvent, waitFor } from '@testing-library/react-native'
import { Drawer } from '@/components/nav/Drawer'
import { meApi } from '@/lib/api'
import { linkWalletNow } from '@/lib/walletlink'
import { useMobileWallet } from '@wallet-ui/react-native-kit'

// The approved address, shown truncated (first 4 … last 4) wherever the
// drawer claims a connection.
const OWNER = '7jAqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP'

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
// The real `latestWallet` (L78) pulls the flow module, which imports
// AsyncStorage at load — one shared fake (test/async-storage-seam.ts).
jest.mock('@react-native-async-storage/async-storage', () => require('@/test/async-storage-seam').asyncStorage())
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
jest.mock('@/lib/walletlink', () => ({
  linkWalletNow: jest.fn(async () => {}),
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

// ---------------------------------------------------------------------------
// FM-WALLc / L73 — the drawer half of the connect truth.
//
// L65 expressed linkage only as the ABSENCE of its reminder; L73 demands
// the positive side. Four reds, one per clause:
//   (a) server-linked → a Connected row that carries the approved address;
//   (b) the profile fetch never confirmed it (me==null) but the local
//       authorization holds an account → still Connected (the setMe no-op
//       used to drop exactly this fact);
//   (c) a successful reminder press flips the row in the same drawer;
//   (d) a refused press states its reason instead of swallowing it.
//
// Red before L73 (observed): no "Wallet connected" exists in the drawer at
// any state (Drawer.tsx renders only the reminder, catch {} at :83).

it('L73 — a linked wallet gets a positive Connected state carrying the address (FM-WALLc(ii))', async () => {
  ;(meApi.get as jest.Mock).mockResolvedValue({
    id: 7,
    email: 'dev@talon.app',
    tier: 'free',
    wallet_linked: true,
  })
  ;(useMobileWallet as jest.Mock).mockReturnValue({
    account: { address: OWNER },
    connect: jest.fn(),
    signMessages: jest.fn(),
  })

  await render(<Drawer open width={300} onClose={() => {}} />)

  expect(await screen.findByText('Wallet connected')).toBeTruthy()
  expect(screen.getByText('7jAq…KPFP')).toBeTruthy()
})

it('L73 — a link the profile fetch never confirmed still shows (me==null, FM-WALLc(iii))', async () => {
  ;(meApi.get as jest.Mock).mockRejectedValue(new Error('401'))
  ;(useMobileWallet as jest.Mock).mockReturnValue({
    account: { address: OWNER },
    connect: jest.fn(),
    signMessages: jest.fn(),
  })

  await render(<Drawer open width={300} onClose={() => {}} />)

  // The local authorization is the fact; a failed /v1/me read must not
  // erase it (the old setMe(prev => prev ? … : prev) dropped it silently).
  expect(await screen.findByText('Wallet connected')).toBeTruthy()
})

it('L73 — the reminder press flips the row to Connected in the same drawer (FM-WALLc(iii))', async () => {
  ;(meApi.get as jest.Mock).mockResolvedValue({
    id: 7,
    email: 'dev@talon.app',
    tier: 'free',
    wallet_linked: false,
  })

  await render(<Drawer open width={300} onClose={() => {}} />)

  fireEvent.press(await screen.findByText('Connect your wallet'))
  await waitFor(() => expect(linkWalletNow).toHaveBeenCalledTimes(1))

  expect(await screen.findByText('Wallet connected')).toBeTruthy()
  expect(screen.queryByText('Connect your wallet')).toBeNull()
})

it('L73 — a refused link says why instead of swallowing it (FM-WALLc(i))', async () => {
  ;(meApi.get as jest.Mock).mockResolvedValue({
    id: 7,
    email: 'dev@talon.app',
    tier: 'free',
    wallet_linked: false,
  })
  ;(linkWalletNow as jest.Mock).mockRejectedValueOnce(new Error('cancelled in the wallet'))

  await render(<Drawer open width={300} onClose={() => {}} />)
  fireEvent.press(await screen.findByText('Connect your wallet'))

  // The reminder survives its own failure (L65) and now carries the reason.
  expect(await screen.findByText(/cancelled in the wallet/)).toBeTruthy()
  expect(await screen.findByText('Connect your wallet')).toBeTruthy()
})
