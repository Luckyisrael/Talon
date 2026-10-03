import React from 'react'
import { fireEvent, render, screen } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import BuildScreen from '@/app/(tabs)/build'
import {
  useActiveProject,
  useExportJob,
  usePromptStream,
  useStartExport,
} from '@/hooks'
import { skrApi } from '@/lib/api'

// Row L37 — FM-SKRb: the payment surface has to be REACHABLE from the build
// tab, not merely exist.
//
// W1 a panel nobody can open is the same as no feature: the panel's own suite
//    proves the decision logic, and this suite proves the build tab offers
//    "build tab → tap Export" and that pressing it mounts that panel. It is
//    the one failure every other test in the row would stay green for.
// W2 the affordance only renders when a project is chosen, so the stub
//    answers with a real active project rather than an empty list.
//
// Harness notes: the screen pulls two streaming hooks and a snapshot cache
// that have nothing to do with the export surface — both are stubbed through
// the mocked barrel, while '@/lib/api' stays partially real (route shapes and
// every other export intact) with only the SKR quote captured. RTL v14
// render() is async.

const mockStart = jest.fn(async () => undefined)

// The screen's own queries (seed prompt, verify-state badge) go through the
// REAL request(), which would dial the backend and leave a socket open — that
// is what kept jest alive after the run. A plain function, not a jest.fn(), so
// jest.clearAllMocks() in beforeEach cannot strip it.
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

// The panel talks to the wallet, which is a native session (and whose
// nanostores dependency is ESM-only, so jest cannot require it at all).
// This suite only mounts the panel and reads its quote — the payment path
// is export-panel.test.tsx's job.
jest.mock('@wallet-ui/react-native-kit', () => ({
  useMobileWallet: jest.fn(() => ({
    account: undefined,
    connect: jest.fn(async () => ({ address: '7jAqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP' })),
    sendTransactions: jest.fn(async () => 'sig'),
    client: { rpc: { getTokenSupply: jest.fn() } },
    chain: 'solana:mainnet',
  })),
}))

// Screen reads useSafeAreaInsets, which throws without a provider (there is no
// device window behind it). Static insets: the tab bar layout is not what any
// assertion here is about.
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
    diag: jest.fn(),
  })
  ;(useStartExport as jest.Mock).mockReturnValue({ mutate: mockStart, isPending: false })
  ;(useExportJob as jest.Mock).mockReturnValue({ data: null })
})

it('the build tab offers Export and pressing it mounts the paywall-aware panel', async () => {
  ;(skrApi.quote as jest.Mock).mockResolvedValue({
    skr_amount: 37,
    credits: 5,
    mint: '315RTJYfgHANxzwrHFodmfi6NJ6ELBN1Z55fFxTEXCtx',
    treasury: '5xJqQXSEWNoNon45Noctg692w9cQJPdmL3vzhxYdKPFP',
    spare_credits: 0,
  })

  // gcTime: 0 — the default schedules a 5-minute garbage-collection timer per
  // query, which holds the event loop open and stops jest from exiting after
  // the run. Same setting the other screen suites use.
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  await render(
    <QueryClientProvider client={client}>
      <BuildScreen />
    </QueryClientProvider>,
  )

  // The affordance itself — not a panel that happens to render in tests.
  fireEvent.press(await screen.findByText('Export'))

  // …and it is the panel, quoting the server rather than assuming a price.
  expect(await screen.findByText('0 of 5 export credits left')).toBeTruthy()
})
