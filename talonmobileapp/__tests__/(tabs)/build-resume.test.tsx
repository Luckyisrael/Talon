import React from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import BuildScreen from '@/app/(tabs)/build'
import { useActiveProject, usePromptStream } from '@/hooks'
import { conversationApi, promptsApi } from '@/lib/api'

// L53 / FM-RSUMa — fails-first: the resumed turn streams ON SCREEN.
//
// The gap, reproduced as a component: history already holds prompt 82
// (the interrupted turn is persisted — that is what makes it resumable),
// the phase frame names it, and `liveVisible`'s historyHasTurn retires
// the tail — so the resume stream reduces into an unmounted screen. The
// user's server saw `POST /v1/prompts/75/resume bytes=69375` while the
// phone showed nothing; this suite pins both halves of the fix:
//   L53a/L53b — the tail renders while streaming WITH the prompt in
//     history, the live prompt's stored assistant rows step aside (the
//     tail carries them), the stored user bubble stays;
//   L53c     — Resume hands the prompt's stored blocks to resume() so the
//     continuation seeds instead of starting blank.
//
// Scaffolding mirrored from build-keyboard.test.tsx (same screen, same
// seams): hooks replaced at the barrel, api partially mocked, network
// disabled outright so jest never reaches a live socket.

global.fetch = (async () => {
  throw new Error('network is disabled in this suite')
}) as unknown as typeof fetch

jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api')
  return {
    ...actual,
    conversationApi: { get: jest.fn() },
    promptsApi: { ...actual.promptsApi, get: jest.fn() },
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
jest.mock('@/lib/history-cache', () => ({
  loadSnapshot: jest.fn(async () => null),
  saveSnapshot: jest.fn(async () => undefined),
}))
jest.mock('expo-router', () => ({
  useRouter: () => ({ push: jest.fn(), back: jest.fn(), replace: jest.fn() }),
  useFocusEffect: () => undefined,
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
jest.mock('react-native-keyboard-controller', () => {
  const React_ = require('react')
  return {
    KeyboardProvider: ({ children }: { children?: React.ReactNode }) => children,
    KeyboardStickyView: ({ children }: { children?: React.ReactNode }) =>
      React_.createElement('View', { testID: 'keyboard-sticky' }, children),
  }
})
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

const mockHistory = conversationApi.get as jest.Mock
const mockPromptRow = promptsApi.get as jest.Mock

function primeStream(turn: Record<string, unknown>) {
  const stream = {
    turn,
    send: jest.fn(),
    answer: jest.fn(),
    answerPrompt: jest.fn(),
    resume: jest.fn(),
    discard: jest.fn(),
    steer: jest.fn(),
    parkQueue: jest.fn(),
    stop: jest.fn(),
    // L69b-c: the screen's own door back into a live turn (cold start —
    // no AppState transition ever fires for a fresh mount to answer).
    reattach: jest.fn(),
  }
  ;(usePromptStream as jest.Mock).mockReturnValue(stream)
  return stream
}

function freshClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
}

async function mount() {
  const client = freshClient()
  return render(
    <QueryClientProvider client={client}>
      <BuildScreen />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  jest.clearAllMocks()
  ;(useActiveProject as jest.Mock).mockReturnValue({ id: 7, name: 'Talon' })
  mockPromptRow.mockResolvedValue({ id: 82, status: 'running', files_touched: [] })
  // History already holds the interrupted prompt — the state that made the
  // live tail retire (and the resume stream invisible) before this row.
  mockHistory.mockResolvedValue({
    turns: [
      { role: 'user', prompt_id: 82, blocks: [{ kind: 'text', text: 'keep building the wallet' }] },
      { role: 'assistant', prompt_id: 82, blocks: [{ kind: 'text', text: 'I was mid-build' }] },
    ],
    latest: { id: 82, mode: 'build', status: 'cancelled', round: 3 },
  })
})

test('L53a/b — streaming with the prompt already in history: tail visible, no double rows', async () => {
  primeStream({
    phase: 'streaming',
    promptId: 82,
    thinking: 'resuming the wallet wiring',
    thinkingActive: true,
    text: '',
    output: '',
    tasks: [],
    toolCalls: [],
    // The reducer's real shape after a thinking.delta: the segment rides
    // the timeline (L52), the legacy `thinking` field keeps it too.
    timeline: [{ key: 'tl-0', kind: 'thinking', text: 'resuming the wallet wiring', streaming: true }],
  })
  await mount()

  // The tail renders — pre-fix historyHasTurn retired it on the first
  // phase frame and every resume frame reduced into this absence.
  expect(await screen.findByText(/Thinking/)).toBeTruthy()

  // The live prompt's stored assistant row steps aside (the tail seeds it
  // from the same blocks in the real flow)…
  expect(screen.queryByText('I was mid-build')).toBeNull()
  // …while the stored user bubble — the prompt itself, never echoed on
  // resume — stays put.
  expect(screen.getByText('keep building the wallet')).toBeTruthy()
})

test('L53c — Resume hands the stored blocks so the continuation seeds, not restarts', async () => {
  const stream = primeStream({ phase: 'error', error: 'provider died', promptId: 82, text: '', thinking: '', output: '', tasks: [], toolCalls: [] })
  await mount()

  await fireEvent.press(await screen.findByText('Resume'))

  expect(stream.resume).toHaveBeenCalledTimes(1)
  const [promptId, seedBlocks] = stream.resume.mock.calls[0]
  expect(promptId).toBe(82)
  // Pre-fix resume(id) carried no blocks — the tail started blank.
  expect(Array.isArray(seedBlocks)).toBe(true)
  expect(seedBlocks).toEqual(
    expect.arrayContaining([expect.objectContaining({ kind: 'text', text: 'I was mid-build' })]),
  )
})

// L69b-c / FM-AI-BG(v) — the cold-start door: a fresh mount never sees an
// AppState *transition*, so the L69 observer cannot be what rejoins a turn
// the server kept running. History's latest says `running`, the tail is
// empty — the screen attaches itself, once per prompt id.
test('L69b-c — a fresh mount attaches the running turn the server kept alive', async () => {
  mockHistory.mockResolvedValue({
    turns: [
      { role: 'user', prompt_id: 82, blocks: [{ kind: 'text', text: 'build the wallet' }] },
      { role: 'assistant', prompt_id: 82, blocks: [{ kind: 'text', text: 'I was mid-build' }] },
    ],
    latest: { id: 82, mode: 'build', status: 'running', round: 3 },
  })
  const stream = primeStream({ phase: 'idle', promptId: 0, text: '', thinking: '', output: '', tasks: [], toolCalls: [] })
  await mount()

  await waitFor(() => expect(stream.reattach).toHaveBeenCalledWith(82))
  // Once: the ref keyed on the id keeps a 409 → settle → reload cycle from
  // turning the attach into a loop.
  expect(stream.reattach).toHaveBeenCalledTimes(1)
})
