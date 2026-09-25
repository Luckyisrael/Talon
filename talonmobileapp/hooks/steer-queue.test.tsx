import React from 'react'
import { act, renderHook } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { usePromptStream } from '@/hooks'
import { promptsApi } from '@/lib/api'

// H2 delivery parity: what a submitted message DOES while a turn runs.
//
// S1 steer gate: a message aimed at the running build must reach the turn the
//    phase event NAMED — steering before that has no address, so it returns
//    false and the composer keeps the draft (never a POST into the void, never
//    a swallow). After the phase event, steer POSTs to /prompts/:id/steer and
//    the row appears in the live tail.
// S2 steer failure: delivery that fails lands an error on its own row — the
//    web's "shows the error on the bubble rather than silently dropping the
//    user's words".
// S3 park: a message typed during a PLAN call is parked as a visible row and
//    NOT delivered while the call runs (or while it pauses on a question —
//    the question owns the conversation).
// S4 delivery: the settled call (done) fires the parked message as its OWN
//    plan turn — one per settle — with the queue empty afterwards. planStream
//    marks which composer rule applies (park on plan, steer on build).
//
// Harness notes: the stream transport is the seam — promptsApi is partially
// mocked so route shapes and normalizePlan stay real while tests inject frames
// through the captured onEvent. Variables the mock factory touches are
// mock*-prefixed (jest hoist rule) and only read at call time.

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

const mockStreamCalls: { pid: number; mode: string; prompt: string }[] = []
let mockDeliver: ((ev: { kind: string; data: string }) => void) | null = null

jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api') as Record<string, any>
  return {
    ...actual,
    promptsApi: {
      ...actual.promptsApi,
      stream: jest.fn(
        (pid: number, mode: string, prompt: string, onEvent: (ev: { kind: string; data: string }) => void) => {
          mockStreamCalls.push({ pid, mode, prompt })
          mockDeliver = onEvent
          return Promise.resolve(() => {})
        },
      ),
      steer: jest.fn(() => Promise.resolve({ status: 'ok' })),
      answer: jest.fn(() => Promise.resolve(() => {})),
      resume: jest.fn(() => Promise.resolve(() => {})),
      discard: jest.fn(() => Promise.resolve({ status: 'ok' })),
    },
  }
})

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function emit(kind: string, data: Record<string, unknown> = {}) {
  if (mockDeliver === null) throw new Error('no stream open to deliver into')
  mockDeliver({ kind, data: JSON.stringify(data) })
}

beforeEach(() => {
  mockStreamCalls.length = 0
  mockDeliver = null
  jest.clearAllMocks()
})

test('S1 — steer is gated on the phase event naming the turn, then routes there', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await act(async () => {
    await result.current.send('build', 'build a wallet', [], 7)
  })
  expect(result.current.turn.planStream).toBe(false)

  // No prompt id yet: refuse, keep the caller's draft, touch nothing.
  let delivered = true
  await act(async () => {
    delivered = result.current.steer('also add send')
  })
  expect(delivered).toBe(false)
  expect(promptsApi.steer).not.toHaveBeenCalled()
  expect(result.current.turn.steers ?? []).toHaveLength(0)

  await act(async () => {
    emit('phase', { phase: 'GENERATE', prompt_id: 42 })
  })
  expect(result.current.turn.promptId).toBe(42)

  await act(async () => {
    delivered = result.current.steer('also add send')
  })
  expect(delivered).toBe(true)
  expect(promptsApi.steer).toHaveBeenCalledWith(42, 'also add send')
  expect(result.current.turn.steers).toEqual([{ id: 1, text: 'also add send' }])
})

test('S2 — a failed steer lands an error on its row, never silently', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await act(async () => {
    await result.current.send('build', 'build a wallet', [], 7)
  })
  await act(async () => {
    emit('phase', { phase: 'GENERATE', prompt_id: 42 })
  })
  ;(promptsApi.steer as jest.Mock).mockRejectedValueOnce(new Error('turn already finished'))
  await act(async () => {
    result.current.steer('nudge left')
  })
  await act(async () => {})
  expect(result.current.turn.steers).toEqual([{ id: 1, text: 'nudge left', error: 'turn already finished' }])
})

test('S3 — a plan call parks the message visibly and never delivers mid-call or mid-question', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await act(async () => {
    await result.current.send('plan', 'design a dex', [], 7)
  })
  expect(result.current.turn.planStream).toBe(true)

  await act(async () => {
    result.current.parkQueue('also dark mode')
  })
  expect(result.current.turn.queue).toEqual([{ text: 'also dark mode' }])
  expect(mockStreamCalls).toHaveLength(1)

  // Paused on a question: the question owns the conversation — no delivery.
  await act(async () => {
    emit('question', { question: 'Ship it?', prompt_id: 9, choices: ['Yes'] })
    emit('done', { status: 'waiting' })
  })
  expect(result.current.turn.phase).toBe('waiting')
  expect(result.current.turn.queue).toHaveLength(1)
  expect(mockStreamCalls).toHaveLength(1)
})

test('S4 — the settled call fires the parked message as its own plan turn, one per settle', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await act(async () => {
    await result.current.send('plan', 'design a dex', [], 7)
  })
  await act(async () => {
    result.current.parkQueue('first idea')
    result.current.parkQueue('second idea')
  })
  expect(result.current.turn.queue).toHaveLength(2)

  await act(async () => {
    emit('done', { status: 'success' })
  })
  expect(mockStreamCalls).toHaveLength(2)
  expect(mockStreamCalls[1]).toMatchObject({ pid: 7, mode: 'plan', prompt: 'first idea' })
  // The first parked row went out; the second waits for THIS delivery to end.
  expect(result.current.turn.queue).toEqual([{ text: 'second idea' }])
  expect(result.current.turn.phase).toBe('streaming')
  expect(result.current.turn.userEcho).toBe('first idea')

  // Settle the delivery's own turn: the second follows, exactly one per settle.
  await act(async () => {
    emit('done', { status: 'success' })
  })
  expect(mockStreamCalls).toHaveLength(3)
  expect(mockStreamCalls[2]).toMatchObject({ mode: 'plan', prompt: 'second idea' })
  expect(result.current.turn.queue).toEqual([])
})
