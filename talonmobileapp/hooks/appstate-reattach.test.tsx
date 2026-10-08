import React from 'react'
import { act, renderHook } from '@testing-library/react-native'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { usePromptStream } from '@/hooks'
import { promptsApi } from '@/lib/api'

// L69 / FM-AI-BG + FM-IDLE-STARVE — the mobile half (row c + f): the app's
// first AppState observer. Backgrounding drops the SSE connection; before L69
// that killed the TURN (backend red: TestBuildTurnSurvivesViewerDisconnect).
// Now the server keeps the turn running with nobody watching, so the phone's
// job is purely transport:
//   a) suspension closes the local view (the turn survives — proven server-
//      side by turn_lifetime_test.go);
//   b) foregrounding with the tail still streaming/waiting re-attaches via
//      GET /v1/prompts/:id/stream;
//   c) a 409 (no turn runs: finished, or a plan turn with no rejoinable
//      stream) settles the tail to `done` — which is what triggers the
//      screen's existing history refetch (build.tsx loadHistory on settle;
//      isTailVisible retires the tail once history carries the turn);
//   d) stop() calls POST /v1/prompts/:id/stop BEFORE dropping the local
//      stream: the turn is cancelled server-side, not just abandoned.
//
// The transport seam: promptsApi is partially mocked so route shapes stay
// real while the test injects frames through the captured onEvent and drives
// the captured onError by hand. mock*-prefixed names satisfy jest's hoist
// rule and are only read at call time.

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

// AppState never changes on its own in jest — the listeners the hook
// registers are collected here and fired by hand. The mock targets the
// module RN's index.js lazily requires for AppState (deep-path mock: never
// spreads react-native's index, whose lazy getters load DevMenu and friends
// that jest-expo does not register).
const mockAppStateListeners: ((state: string) => void)[] = []
jest.mock('react-native/Libraries/AppState/AppState', () => ({
  __esModule: true,
  default: {
    currentState: 'active',
    addEventListener: (_type: string, listener: (state: string) => void) => {
      mockAppStateListeners.push(listener)
      return {
        remove: () => {
          const i = mockAppStateListeners.indexOf(listener)
          if (i >= 0) mockAppStateListeners.splice(i, 1)
        },
      }
    },
  },
}))

const mockStreamCalls: { pid: number; mode: string; prompt: string }[] = []
let mockDeliver: ((ev: { kind: string; data: string }) => void) | null = null
const mockStreamClose = jest.fn()
// L69b-a: resume's onError is driven by hand like attach's — the 409 the
// live-turn refusal answers (prompt 102's four dead-end taps, 23:09–23:11).
let mockResumeError: ((err: Error) => void) | null = null

jest.mock('@/lib/api', () => {
  const actual = jest.requireActual('@/lib/api') as typeof import('@/lib/api')
  return {
    ...actual,
    promptsApi: {
      ...actual.promptsApi,
      stream: jest.fn(
        (pid: number, mode: string, prompt: string, onEvent: (ev: { kind: string; data: string }) => void) => {
          mockStreamCalls.push({ pid, mode, prompt })
          mockDeliver = onEvent
          return Promise.resolve(mockStreamClose)
        },
      ),
      resume: jest.fn((_pid: number, _onEvent: unknown, onError?: (err: Error) => void) => {
        mockResumeError = onError ?? null
        return Promise.resolve(mockStreamClose)
      }),
      attachPrompt: jest.fn(() => Promise.resolve(jest.fn())),
      stopPrompt: jest.fn(() => Promise.resolve({ status: 'stopping' })),
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

function fireAppState(state: string) {
  mockAppStateListeners.slice().forEach((listener) => listener(state))
}

beforeEach(() => {
  mockStreamCalls.length = 0
  mockDeliver = null
  mockResumeError = null
  mockStreamClose.mockClear()
  jest.clearAllMocks()
  mockAppStateListeners.length = 0
})

/** Drive a named build turn into phase `streaming` with prompt id 42. */
async function streamingTurn(result: { current: ReturnType<typeof usePromptStream> }) {
  await act(async () => {
    await result.current.send('build', 'build a streak app', [], 7)
  })
  await act(async () => {
    emit('phase', { phase: 'GENERATE', prompt_id: 42 })
  })
}

test('L69a — suspension closes the view; foregrounding a streaming turn re-attaches it', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await streamingTurn(result)
  expect(result.current.turn.phase).toBe('streaming')

  // (a) The connection dies with the app — closing the local handle just
  // makes that honest. The turn itself keeps running server-side.
  await act(async () => {
    fireAppState('background')
  })
  expect(mockStreamClose).toHaveBeenCalled()

  // (b) Back to active with the tail still streaming: re-attach through the
  // route, addressed by the turn the phase event named.
  await act(async () => {
    fireAppState('active')
  })
  expect(promptsApi.attachPrompt).toHaveBeenCalledWith(42, expect.any(Function), expect.any(Function))
})

test('L69b — a waiting turn re-attaches too, and a 409 settles the tail through history', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await streamingTurn(result)
  await act(async () => {
    emit('question', { question: 'Ship it?', prompt_id: 9, choices: ['Yes'] })
    emit('done', { status: 'waiting' })
  })
  expect(result.current.turn.phase).toBe('waiting')

  await act(async () => {
    fireAppState('background')
    fireAppState('active')
  })
  expect(promptsApi.attachPrompt).toHaveBeenCalledWith(42, expect.any(Function), expect.any(Function))

  // (c) Nothing runs server-side — the loop paused on the question (or the
  // turn finished): 409. The tail settles to `done`, and the screen's
  // existing settle effect refetches history, where the waiting prompt and
  // its restored question card live.
  const call = (promptsApi.attachPrompt as jest.Mock).mock.calls[0] as [
    number,
    (ev: { kind: string; data: string }) => void,
    (err: Error) => void,
  ]
  await act(async () => {
    call[2](Object.assign(new Error('stream failed (409)'), { status: 409 }))
  })
  expect(result.current.turn.phase).toBe('done')
})

test('L69c — a settled turn never re-attaches (keep-green: no churn on a dead turn)', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await streamingTurn(result)
  await act(async () => {
    emit('done', { status: 'success' })
  })
  expect(result.current.turn.phase).toBe('done')

  await act(async () => {
    fireAppState('background')
    fireAppState('active')
  })
  expect(promptsApi.attachPrompt).not.toHaveBeenCalled()
})

test('L69d — stop() cancels the turn server-side before it drops the local stream', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })
  await streamingTurn(result)

  await act(async () => {
    result.current.stop()
  })
  // (d) The turn runs on the server now — closing the local stream alone
  // would leave it burning. The stop route is called FIRST, then the view
  // closes and the tail settles.
  expect(promptsApi.stopPrompt).toHaveBeenCalledWith(42)
  expect(result.current.turn.phase).toBe('done')
  expect(mockStreamClose).toHaveBeenCalled()
  const stopAt = (promptsApi.stopPrompt as jest.Mock).mock.invocationCallOrder[0]
  const closeAt = mockStreamClose.mock.invocationCallOrder[0]
  expect(stopAt).toBeLessThan(closeAt)
})

// L69b / FM-AI-BG(v) — the doors back in. The server's Resume 409 is not a
// failure to report: it is the proof a loop is already running (or the turn
// just finished). Either truth means the correct door is ATTACH — join the
// live stream, seed intact — not a raw `stream failed (409)` dead-end.
test('L69b-a — resume 409 joins the live turn instead of dead-ending', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })

  await act(async () => {
    await result.current.resume(42)
  })
  expect(result.current.turn.phase).toBe('streaming')

  // The exact refusal prompt 102 answered four times on 2026-10-08 23:09–23:11:
  // the turn is live, Resume refuses a second loop.
  await act(async () => {
    mockResumeError?.(Object.assign(new Error('stream failed (409)'), { status: 409 }))
  })
  expect(promptsApi.attachPrompt).toHaveBeenCalledWith(42, expect.any(Function), expect.any(Function))
  expect(result.current.turn.phase).not.toBe('error')
})

test('L69b-a2 — a non-409 resume failure still lands phase error (keep-green)', async () => {
  const { result } = await renderHook(() => usePromptStream(7), { wrapper })

  await act(async () => {
    await result.current.resume(42)
  })
  await act(async () => {
    mockResumeError?.(new Error('network request failed'))
  })
  expect(result.current.turn.phase).toBe('error')
  expect(promptsApi.attachPrompt).not.toHaveBeenCalled()
})
