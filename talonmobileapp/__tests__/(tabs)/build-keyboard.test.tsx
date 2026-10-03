import React from 'react'
import { render, screen, within } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import BuildScreen from '@/app/(tabs)/build'
import { useActiveProject, useExportJob, usePromptStream, useStartExport } from '@/hooks'

// L50 / FM-KBDa — fails-first: the composer block must be wrapped in
// keyboard-controller's KeyboardStickyView so the field rides the IME edge
// (the manifest's adjustResize resizes the window; the sticky view closes
// whatever overlap remains — a whole-screen avoider would double-offset).
// The package is replaced at the jest seam with a marker; the wrap and its
// contents are what this suite pins.

jest.mock('react-native-keyboard-controller', () => {
  const React_ = require('react')
  return {
    KeyboardProvider: ({ children }: { children?: React.ReactNode }) => children,
    KeyboardStickyView: ({ children }: { children?: React.ReactNode }) =>
      React_.createElement('View', { testID: 'keyboard-sticky' }, children),
  }
})

// Scaffolding mirrored from build-export.test.tsx (same screen, same seams):
// the screen's own queries go through real request(), so the network is
// disabled outright — a thrown fetch keeps jest off live sockets.
global.fetch = (async () => {
  throw new Error('network is disabled in this suite')
}) as unknown as typeof fetch

jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api')
  return {
    ...actual,
    skrApi: { quote: jest.fn(), verify: jest.fn() },
    exportApi: {
      ...actual.exportApi,
      start: jest.fn(async () => ({ job_id: 41 })),
      get: jest.fn(),
    },
  }
})

jest.mock('@/hooks', () => ({
  useActiveProject: jest.fn(),
  usePromptStream: jest.fn(),
  useStartExport: jest.fn(),
  useExportJob: jest.fn(),
}))

jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async () => null),
  setItemAsync: jest.fn(async () => undefined),
  deleteItemAsync: jest.fn(async () => undefined),
}))

jest.mock('@react-native-async-storage/async-storage', () => ({
  default: {
    getItem: async () => null,
    setItem: async () => undefined,
    removeItem: async () => undefined,
    getAllKeys: async () => [],
  },
  getItem: async () => null,
  setItem: async () => undefined,
  removeItem: async () => undefined,
  getAllKeys: async () => [],
}))

// Shared seam — see test/native-seams.ts for why reanimated's own mock does
// not work (it boots native Worklets at import time through build.tsx).
jest.mock('react-native-reanimated', () => require('@/test/native-seams').reanimated())

jest.mock('@wallet-ui/react-native-kit', () => ({
  useMobileWallet: jest.fn(() => ({
    account: undefined,
    connect: jest.fn(async () => ({ address: '7jAqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP' })),
    sendTransactions: jest.fn(async () => 'sig'),
    client: { rpc: { getTokenSupply: jest.fn() } },
    chain: 'solana:mainnet',
  })),
}))

jest.mock('react-native-safe-area-context', () => {
  const frame = { x: 0, y: 0, width: 390, height: 844 }
  const insets = { top: 0, right: 0, bottom: 0, left: 0 }
  const passthrough = ({ children }: { children?: React.ReactNode }) => children
  return {
    __esModule: true,
    SafeAreaProvider: passthrough,
    SafeAreaView: passthrough,
    useSafeAreaInsets: () => insets,
    useSafeAreaFrame: () => frame,
    initialWindowMetrics: { frame, insets },
  }
})

jest.mock('@/lib/history-cache', () => ({
  loadSnapshot: jest.fn(async () => null),
  saveSnapshot: jest.fn(async () => undefined),
}))

jest.mock('expo-router', () => ({
  useRouter: () => ({ push: jest.fn(), back: jest.fn(), replace: jest.fn() }),
  useFocusEffect: () => undefined,
}))

beforeEach(() => {
  jest.clearAllMocks()
  ;(useActiveProject as jest.Mock).mockReturnValue({ id: 7, name: 'Talon' })
  ;(usePromptStream as jest.Mock).mockReturnValue({
    turn: { phase: 'idle' },
    send: jest.fn(),
    answer: jest.fn(),
    answerPrompt: jest.fn(),
    resume: jest.fn(),
    discard: jest.fn(),
    steer: jest.fn(),
    parkQueue: jest.fn(),
    stop: jest.fn(),
  })
  ;(useStartExport as jest.Mock).mockReturnValue({ mutate: jest.fn(), isPending: false })
  ;(useExportJob as jest.Mock).mockReturnValue({ data: null })
})

it('the composer rides the keyboard via the sticky view', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  await render(
    <QueryClientProvider client={client}>
      <BuildScreen />
    </QueryClientProvider>,
  )

  const sticky = screen.getByTestId('keyboard-sticky')
  expect(await within(sticky).findByPlaceholderText('Tell the agent what to do…')).toBeTruthy()
})
